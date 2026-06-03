/* SPDX-License-Identifier: GPL-2.0 */
/* 
 * firewall.bpf.c
 * Centralized eBPF firewall (XDP + tc) - bootstrap implementation per DESIGN-centralized-ebpf-firewall-controller.md
 *
 * Key features implemented in this starter:
 * - Double-buffered rule maps (_0 / _1) + active_idx in config for atomic policy swap
 * - Priority-ordered scan for first-match semantics (independent of prefix length)
 * - LPM for selected broad CIDR fast-paths (post-ordered)
 * - Ringbuf for drop/allow events (observable by agent)
 * - Basic 5-tuple + prefix matching
 *
 * This is a v1 bootstrap. Real version will be generated/expanded in PR5+.
 */

#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>
#include <bpf/bpf_endian.h>
#include <linux/if_ether.h>
#include <linux/ip.h>
#include <linux/tcp.h>
#include <linux/udp.h>
#include <linux/in.h>

#define MAX_RULES 4096
#define MAX_LPM_ENTRIES 65536

/* Action values (must match Go side) */
#define ACTION_DENY  0
#define ACTION_ALLOW 1

/* Protocol numbers */
#define IPPROTO_TCP 6
#define IPPROTO_UDP 17

struct rule {
	__u32 priority;
	__u32 action;
	__u8  proto;
	__u8  pad[3];
	__u32 src_ip;
	__u32 src_prefixlen;
	__u32 dst_ip;
	__u32 dst_prefixlen;
	__u16 src_port_start;
	__u16 src_port_end;
	__u16 dst_port_start;
	__u16 dst_port_end;
} __attribute__((packed));

struct five_tuple_key {
	__u32 src_ip;
	__u32 dst_ip;
	__u16 src_port;
	__u16 dst_port;
	__u8  proto;
	__u8  pad[3];
};

/* Double buffer config (atomic flip) */
struct global_config {
	__u32 active_idx;      /* 0 or 1 */
	__u32 default_action;  /* 0=deny, 1=allow */
	__u32 rule_count;      /* for observability */
};

/* Map definitions - double buffered for atomic swap (see Atomic Update Code Sketch in design) */
struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, 2);
	__type(key, __u32);
	__type(value, struct global_config);
} global_config_map SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, MAX_RULES);
	__type(key, __u32);
	__type(value, struct rule);
} ingress_ordered_rules_v4_0 SEC(".maps");

struct {
	__uint(type, BPF_MAP_TYPE_ARRAY);
	__uint(max_entries, MAX_RULES);
	__type(key, __u32);
	__type(value, struct rule);
} ingress_ordered_rules_v4_1 SEC(".maps");

/* LPM for broad CIDR fast paths (optional, used after ordered scan or for pure-CIDR policies) */
struct lpm_key4 {
	__u32 prefixlen;
	__u32 data;
};

struct {
	__uint(type, BPF_MAP_TYPE_LPM_TRIE);
	__uint(max_entries, MAX_LPM_ENTRIES);
	__type(key, struct lpm_key4);
	__type(value, __u32); /* action or rule index */
	__uint(map_flags, BPF_F_NO_PREALLOC);
} ingress_lpm_src_v4 SEC(".maps");

/* Ring buffer for events (drops, and optionally allows for sampling) */
struct rule_event {
	__u64 ts;
	__u32 src_ip;
	__u32 dst_ip;
	__u16 src_port;
	__u16 dst_port;
	__u8  proto;
	__u8  action;   /* 0=denied, 1=allowed */
	__u8  hook;     /* 0=XDP, 1=tc */
	__u8  pad;
	__u32 rule_prio; /* which priority matched, or 0xffffffff for default */
};

struct {
	__uint(type, BPF_MAP_TYPE_RINGBUF);
	__uint(max_entries, 256 * 1024);
} events SEC(".maps");

/* Helper: prefix match (manual, not LPM, to preserve explicit priority order) */
static __always_inline int prefix_match(__u32 ip, __u32 rule_ip, __u32 prefixlen) {
	if (prefixlen == 0)
		return 1;
	__u32 mask = (prefixlen == 32) ? 0xFFFFFFFF : (~0U << (32 - prefixlen));
	return (ip & mask) == (rule_ip & mask);
}

static __always_inline int ports_match(__u16 pkt_port, __u16 start, __u16 end) {
	if (start == 0 && end == 0)
		return 1; // any
	return pkt_port >= start && pkt_port <= end;
}

static __always_inline int rule_matches(const struct rule *r, __u8 proto,
                                        __u32 src_ip, __u32 dst_ip,
                                        __u16 src_port, __u16 dst_port) {
	if (r->proto && r->proto != proto)
		return 0;

	// Source side (for ingress: the remote; for egress this would be reversed in tc path)
	if (r->src_prefixlen || r->src_ip) {
		if (!prefix_match(src_ip, r->src_ip, r->src_prefixlen))
			return 0;
	}
	if (r->dst_prefixlen || r->dst_ip) {
		if (!prefix_match(dst_ip, r->dst_ip, r->dst_prefixlen))
			return 0;
	}

	if (!ports_match(src_port, r->src_port_start, r->src_port_end))
		return 0;
	if (!ports_match(dst_port, r->dst_port_start, r->dst_port_end))
		return 0;

	return 1;
}

/* Emit an event to ringbuf (best effort) */
static __always_inline void emit_event(__u8 hook, __u8 action, __u32 prio,
                                       __u32 src_ip, __u32 dst_ip,
                                       __u16 src_port, __u16 dst_port, __u8 proto) {
	struct rule_event *e = bpf_ringbuf_reserve(&events, sizeof(*e), 0);
	if (!e)
		return;

	e->ts = bpf_ktime_get_ns();
	e->src_ip = src_ip;
	e->dst_ip = dst_ip;
	e->src_port = src_port;
	e->dst_port = dst_port;
	e->proto = proto;
	e->action = action;
	e->hook = hook;
	e->rule_prio = prio;

	bpf_ringbuf_submit(e, 0);
}

/* Core lookup using active buffer (double-buf atomic) */
static __always_inline __u32 lookup_action(__u8 proto, __u32 src_ip, __u32 dst_ip,
                                           __u16 src_port, __u16 dst_port) {
	__u32 key = 0;
	struct global_config *cfg = bpf_map_lookup_elem(&global_config_map, &key);
	if (!cfg)
		return ACTION_DENY; // safe default

	__u32 idx = cfg->active_idx & 1;
	void *ordered_map = (idx == 0) ? (void *)&ingress_ordered_rules_v4_0
	                               : (void *)&ingress_ordered_rules_v4_1;

	/* 1. Exact 5-tuple fast path could be added here with another map */

	/* 2. Ordered priority scan (first match wins) */
	__u32 rule_count = cfg->rule_count;
	if (rule_count > MAX_RULES)
		rule_count = MAX_RULES;

	for (__u32 i = 0; i < rule_count; i++) {
		struct rule *r = bpf_map_lookup_elem(ordered_map, &i);
		if (!r)
			continue;

		if (rule_matches(r, proto, src_ip, dst_ip, src_port, dst_port)) {
			__u32 act = r->action;
			emit_event(0 /*XDP*/, act, r->priority, src_ip, dst_ip, src_port, dst_port, proto);
			return act;
		}
	}

	/* 3. Broad LPM (last resort for very wide rules not fully materialized in ordered) */
	struct lpm_key4 lpm_key = { .prefixlen = 32, .data = src_ip }; // example for src
	__u32 *lpm_act = bpf_map_lookup_elem(&ingress_lpm_src_v4, &lpm_key);
	if (lpm_act) {
		emit_event(0, *lpm_act, 0xffffffff, src_ip, dst_ip, src_port, dst_port, proto);
		return *lpm_act;
	}

	/* 4. Default */
	__u32 def = cfg->default_action ? ACTION_ALLOW : ACTION_DENY;
	emit_event(0, def, 0xffffffff, src_ip, dst_ip, src_port, dst_port, proto);
	return def;
}

/* XDP entry (ingress) */
SEC("xdp")
int xdp_firewall(struct xdp_md *ctx) {
	void *data_end = (void *)(long)ctx->data_end;
	void *data = (void *)(long)ctx->data;

	struct ethhdr *eth = data;
	if ((void *)(eth + 1) > data_end)
		return XDP_PASS;

	if (eth->h_proto != bpf_htons(ETH_P_IP))
		return XDP_PASS;

	struct iphdr *iph = (void *)(eth + 1);
	if ((void *)(iph + 1) > data_end)
		return XDP_PASS;

	__u8 proto = iph->protocol;
	__u32 src_ip = iph->saddr;
	__u32 dst_ip = iph->daddr;

	__u16 src_port = 0, dst_port = 0;

	if (proto == IPPROTO_TCP) {
		struct tcphdr *tcph = (void *)(iph + 1);
		if ((void *)(tcph + 1) > data_end)
			return XDP_PASS;
		src_port = bpf_ntohs(tcph->source);
		dst_port = bpf_ntohs(tcph->dest);
	} else if (proto == IPPROTO_UDP) {
		struct udphdr *udph = (void *)(iph + 1);
		if ((void *)(udph + 1) > data_end)
			return XDP_PASS;
		src_port = bpf_ntohs(udph->source);
		dst_port = bpf_ntohs(udph->dest);
	}

	__u32 action = lookup_action(proto, src_ip, dst_ip, src_port, dst_port);

	if (action == ACTION_DENY)
		return XDP_DROP;

	return XDP_PASS;
}

/* tc (clsact) entry for egress - similar logic, can share helpers in real build */
SEC("tc")
int tc_egress_firewall(struct __sk_buff *skb) {
	// Minimal: for now just PASS (full egress implementation in later PR).
	// The design calls for symmetric logic on egress with reversed src/dst in some cases.
	return 0; // TC_ACT_OK
}

char _license[] SEC("license") = "GPL";
