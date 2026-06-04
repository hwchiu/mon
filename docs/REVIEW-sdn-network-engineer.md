# DFW Design Review — Senior Network & SDN Controller Engineer Perspective

**Date:** 2026-06-04  
**Reviewer:** Grok (acting as senior network engineer + SDN controller architect with experience in large-scale eBPF, Calico, Cilium, NSX, cloud provider SDN, and custom host firewalls)  
**Scope:** Primary source = `docs/index.html` (the current published DFW architecture). Cross-referenced against implementation plan (`docs/plans/2026-06-04-dfw-implementation-plan.md`) and historical DESIGN-centralized-ebpf-firewall-controller.md for context.  
**Goal of review:** Identify gaps, ambiguities, risks, and missing considerations that are critical for real-world deployment, especially in cloud environments like Azure, operational correctness, performance at scale, and testability. Focus on networking fundamentals, zero-trust dual-consent model, eBPF datapath realities, control/data plane split, and Azure-specific challenges.

---

## Executive Summary

The DFW zone-centric model (ground rules as baseline matrix + zone rules as consensual overrides with strict **both egress (source zone owner) AND ingress (destination zone owner)** requirement) is a **strong, clean zero-trust abstraction** that is simpler and more scalable than per-5-tuple general policies for many platform teams. The data-driven eBPF approach (never push bytecode, agents self-compile at boot, only map data pushed with <1s SLA, atomic double-buffer) is excellent for operational safety and kernel compatibility.

**Strengths:**
- Clear separation of concerns and ownership (zone owners control their ingress/egress consent).
- Unidirectional control plane with fail-static agents (last-known-good) is production-grade thinking.
- Explicit "host-level only + inter-zone on admin ifaces" scope avoids the CNI/overlay quagmire.
- Simulation/dry-run + observability hooks mentioned.

**Major Areas of Concern / Things Missed or Under-Specified (ranked by risk/impact):**

1. **Bidirectional / Return Path Semantics (High Risk for Correctness)**
2. **Zone CIDR Population & Pod vs Node IP Reality (High — Azure CNI/Overlay critical)**
3. **verdict_map Scalability & Port Representation (Medium-High — map size & lookup)**
4. **tc Attach Priority / Ordering vs Existing Host Firewall & CNI (High for Coexistence)**
5. **Distribution Channel Details & True <1s at 1000+ Agents (Scale)**
6. **Azure Platform Specifics (Accelerated Networking, NSG interaction, SNAT, Private Link, UDR)**
7. **Ground Rules Governance & Multi-Tenant Delegation (RBAC + Ownership)**
8. **eBPF Datapath Completeness (LPM for zones, protocol handling, fragments, ICMP, performance)**
9. **Testing & Validation Strategy (How do you *prove* both-sides enforcement and SLA in a real network?)**
10. **Operational & Failure Modes (Breakglass, partial rollout asymmetry, key rotation, map pressure)**

The current implementation plan is a solid skeleton that maps well to the spec but inherits many of the above ambiguities (it calls out "direction" on zone rules and dual checks, but doesn't resolve the hard networking questions). The Azure test env (requested separately) is the **highest-leverage next step** to surface these issues empirically.

Below are detailed findings + recommendations. Many can (and should) be clarified in an updated design doc before heavy coding.

---

## 1. Bidirectional Traffic & Return Path (Critical Gap)

**Current State (docs/index.html):**
- Explicit "both source_allows && dest_allows".
- Ground matrix is asymmetric (e.g., Internal row→DMZ col = ALLOW; DMZ row→Internal col = DENY).
- Zone rules have a `direction` field.
- Flowchart shows only the forward packet path.
- No discussion of TCP 3-way handshake, return packets, or UDP "replies".

**What's Missing:**
- For a *successful TCP session* from zone S (client) to zone D (server) on port P:
  - Forward (S→D:P): needs S-egress-allow + D-ingress-allow.
  - Return (D→S:ephemeral): needs D-egress-allow + S-ingress-allow.
- In the example ground matrix, opening "Internal clients reach DMZ on 443" via zone rules would require setting the *ingress* permission at DMZ for src=Internal (affecting ground[DMZ][Internal] in the dest check for forward).
- But for the ACKs/SYN-ACKs to come back, you also need DMZ zone to permit *egress* to Internal (or Internal to permit ingress from DMZ).
- Stateless design (per non-goals in history: "stateful conntrack ... future extension") means **all four directions in the matrix must be considered or explicitly allowed**.
- Common pattern in such systems: "allow client-to-service" helper that creates the necessary pair (or four) entries, or a "reply" concept even if stateless (e.g., allow established by broad reverse port range 1024-65535).

**Risk:** Teams will define "one way" rules and be surprised that return traffic (or even the SYN-ACK) is dropped. Or they will over-allow (e.g., full zone-pair any-port) to make TCP work.

**Recommendations:**
- Add a dedicated "Connection Lifecycle & Return Traffic" section with concrete matrix examples for a full TCP session (client in Internal, server in DMZ on 443 + ephemeral return).
- In ZoneRule CRD and engine, support a `symmetric: true` or `create_reverse: true` (with port range for replies, e.g. high ports).
- In the compiler, when emitting verdict_map or policy_rule, clearly separate "egress declaration" vs "ingress declaration" normalization.
- In simulation/dry-run tool: "explain this flow including return packets".
- Update the pseudocode and flowchart to show a full session.
- For v1, document "TCP requires explicit bidirectional consent in the model; use zone rules or ground updates for return high ports if needed."

**Impact on Impl Plan:** Task 3 (engine) and Task 10 (simulation) need explicit bidirectional test cases and helpers. Add to verification checklist.

---

## 2. Zone Definition, CIDRs, and the Pod IP / Node IP / Overlay Reality (Very High for Azure)

**Current State:**
- "The entire IPv4 space is partitioned into non-overlapping security zones. Every IP belongs to exactly one zone."
- "Each Kubernetes cluster belongs to exactly one zone, but zones can span multiple clusters."
- "Zone membership is determined by IP address alone."
- Agent scope: "Host-level source / destination IPs", "Traffic crossing zone CIDR boundaries". Pod-to-pod *within cluster* out of scope.
- In agent: "only inter-zone on admin-specified host interfaces".

**What's Missing / Ambiguous:**
- **How are pod CIDRs folded into the zone?** If a cluster in "DMZ" zone has nodes in 10.1.1.0/24 but pods allocated from a separate 10.240.0.0/12 (common in Azure CNI overlay or Calico), do the zone CIDRs in the Zone CR *must* include the pod ranges?
- On the wire (host iface): 
  - With **Azure CNI no-overlay** (traditional): pods often get IPs from the node subnet or VNet; traffic can leave with pod src IP.
  - With **Azure CNI overlay** (default in many AKS now): pod traffic is encapsulated between nodes. On the host primary/VF interface, you see **node IPs** as src/dst for east-west pod traffic. Pod IPs live only inside the overlay.
  - SNAT/masquerade for north-south or to external.
- Result: DFW will classify most cross-zone pod traffic by the *node's* zone (the IP visible on eth0/azure0), not the pod's identity. This matches the "host-level" and "pod-to-pod within cluster out of scope" statements — good intention.
- **But the spec must be explicit:** "Zone CIDR declarations for a zone **must cover all node IPs and any routable pod IPs** that should be treated as belonging to that zone. In overlay CNIs, enforcement granularity is effectively per-node/zone."
- Clusters "belong to one zone" — the controller/agent must know the full set of IPs (node + pod) that belong to each zone's clusters for accurate `zone_cidr_map`.
- In multi-cluster: different AKS clusters in different VNets/subscriptions may have overlapping pod CIDRs internally, but as long as the *node* subnets (the ones visible for inter-cluster) are in the zone CIDR, it works for host-level.

**Azure-Specific:**
- AKS node subnets live in the VNet.
- You can (and should) make the declared Zone CIDR == the Azure VNet address_space for that zone's VNet.
- Then place the AKS node subnet and VM subnets inside it.
- For pod CIDRs in non-overlay: ensure they are also carved from the same VNet space or explicitly added to the zone's CIDR list in the Zone CR.

**Recommendations:**
- In Zone CRD + docs: `spec.cidrs` must be the **complete** set of prefixes for all node subnets + (if direct-routable) pod prefixes for clusters/VMs in that zone. Provide a helper or controller that can discover from ManagedHost / AKS node status.
- In agent + engine: zone lookup is strictly longest-prefix on the *packet IP seen on the protected host interface*.
- Add a design note: "DFW is a *node/VM boundary* enforcement point. It does not replace CNI identity-aware policy inside the cluster."
- In the 4-zone example, explicitly show the Azure VNet/subnet allocation that maps to the 10.1.0.0/16 etc.

**Impact on Impl Plan + Test Env:** This is why a proper Azure multi-VNet test env (with real AKS + overlay option + standalone VMs) is mandatory. The plan's samples must use CIDRs that match the provisioned Azure address spaces.

---

## 3. verdict_map Design & Scalability (Port Explosion Risk)

**Current State (docs 6.5):**
- `verdict_map`: Key `(src_zone, dst_zone, port)` → ALLOW/DENY. Promoted as "direct lookup for fast per-packet decisions".
- Also `policy_rule_map` for merged rules.
- Zones are small (#4 in example; real target probably dozens, not thousands).
- Ground rules support "different port ranges can have different ground rules".

**Analysis:**
- Naive array or hash of (src_zone_id, dst_zone_id, port) for 64 zones × 65536 ports = ~4M entries per "side" — manageable in memory (few MB if packed), but update cost during atomic apply + map memory in eBPF (many kernels have ~256MB- few GB limit for maps, but per-map limits and pre-alloc matter).
- "all ports" or "0-65535" or large ranges (e.g. 1024-65535 for replies) will either require 64k entries or special "wildcard port" encoding.
- The old scaffolding used ordered array + first-match scan + LPM for CIDRs. That scales better for sparse + range rules (scan a few hundred rules is fine at 10-100ns with small MAX_RULES=4k-16k).
- `verdict_map` is an *optimization* for the common "zone-pair + specific port" case after the compiler has exploded the rules.

**Misses:**
- No description of how the compiler decides what goes into `verdict_map` vs falls back to `policy_rule_map` scan + LPM.
- No port range / "any" encoding strategy (e.g., special port=0 means "any for this zonepair", or a separate "zonepair_defaults" map + exceptions hash).
- Update latency for large maps during <1s propagation (atomic batch update in Go + eBPF `map_update_batch`).
- Max zones assumption (hardcode 64? 128? use u8 or u16 for zone id).

**Recommendations:**
- Define in design + eBPF headers the exact key structs and map types (probably `BPF_MAP_TYPE_HASH` for verdict with composite key, or `ARRAY` of size zones* zones with per-zonepair a pointer to a port-set structure).
- Compiler must "materialize" port ranges intelligently (normalize overlapping rules, emit ranges or discrete for small, wildcard for full).
- Add map utilization metrics (already in plan Task 10) + alerts.
- Consider keeping the ordered-rule scan path as primary (more flexible for future priority or richer matches) and verdict as fast-path cache for hot zone-pairs.

**Impact:** Task 5 (eBPF C) and Task 3 (engine compile/serialize) need a concrete map emission strategy before the C code is written. The current plan lists the maps but not the representation details.

---

## 4. tc-BPF Attach Ordering & Coexistence (Coexistence is Stated but Not Engineered)

**Current State:**
- "DFW runs before user policies in the network stack (outer security layer)".
- "tc-BPF Attachment".
- "Attach ordering is explicit (see ...)" — referenced from history but not in the current HTML.
- Out of scope: user policies, CNI ifaces.

**Reality in Linux + Azure + AKS + common CNIs:**
- Host has: Azure platform hooks, netfilter (iptables/nftables chains from kube-proxy, CNI, host fw, Azure Firewall agent if present), then tc qdiscs, then XDP on some paths.
- Multiple tc filters can be attached to the same qdisc (clsact). Order is by priority (lower number = earlier?).
- CNI (Cilium, Calico eBPF, flannel) often attach their own tc or XDP programs with specific priorities.
- To guarantee "DFW first" (outer), the agent must attach with a **high priority** (early in the chain) on the ingress/egress hooks of the specified host interfaces only.
- If a later stage (CNI or host nft) also drops, the packet is still blocked — "AND" with user policies as stated.

**Misses in Current Design:**
- No concrete guidance on tc filter priority values to use (e.g., 49152 or lower than typical CNI 40000-50000? Cilium docs have specific numbers).
- No handling for "already attached" or "multiple DFW instances".
- No interaction with Azure Accelerated Networking VF representors (sometimes you attach on the VF netdev, sometimes the host side).
- No mention of cgroup_skb (mentioned in history) for container scoping if desired later.
- In AKS DaemonSet: the pod runs with hostNetwork? or how does it see the host's primary interfaces cleanly?

**Recommendations:**
- In agent config + Helm values: explicit `tc_priority_ingress: 100`, `tc_priority_egress: 100` (or documented values that are "before" common CNIs).
- Agent should log the attach order / existing filters on startup (`tc filter show dev eth0` equivalent via netlink).
- Coexistence matrix in docs (Cilium, Calico, vanilla kube-proxy, Azure host firewall, nftables).
- Support "insert before" or "replace" semantics.
- For AKS privileged DaemonSet: document the exact `securityContext`, `hostNetwork: true` or hostPID + volumeMounts for /sys/fs/bpf, and node selector for zone nodes.

**Impact on Plan:** Strengthen Task 6 (loader/attach) and Task 9 (deployment + coexistence tests). The kind e2e must include a CNI (Cilium or Azure CNI with some host rules) and assert DFW verdict happens first (e.g., by seeing DFW ringbuf event even if later stage would have allowed).

---

## 5. Distribution, Fan-Out, Delta, and True <1s SLA at Scale

**Current State:**
- "Fan-Out Distribution" diagram shows Policy Engine → Distribution Channel → Zone-X Distributor → Agent.
- Targets: <1s full, P50 ~200ms, P99 ~900ms for "all devices".
- Delta updates, atomic apply, version tracking, retry with backoff.
- gRPC implied (from history).

**Analysis & Misses:**
- For 5,000 agents, a single gRPC server in the central controller pods will struggle with 5k persistent streams (memory, CPU for per-stream signing/encryption, head-of-line blocking if one slow agent).
- "Zone-X Distributor" is mentioned but not designed (is it a Deployment per zone? Daemon? separate binary? how does it get the map data?).
- Delta: requires agents to report their current version accurately, and the pusher to compute/send only changes. For map data this can be "full new blob for the zone" (still small if zones are coarse) or true diff of the 4 maps.
- Propagation measurement: agents must report "time I received + time I applied" with good clocks (or use controller timestamps + one-way latency estimate).
- Connection model: agents initiate outbound to controller (better for NAT/firewalls). Controller pushes on the stream.
- Auth: mTLS or token + signature on payload.

**Recommendations:**
- Clarify the distribution architecture in a new subsection: central "pusher" service (can be scaled horizontally behind LB) that fans out to per-zone "zone distributor" Deployments (or just sharded goroutines). Agents register with their zone; distributors only handle agents for "their" zone.
- Make delta optional or "full for small zones".
- Add a "propagation test" mode or built-in canary (controller canary-updates a test zone rule and measures via agent reports).
- Expose p99 propagation as a metric.
- For v1, support 100-500 agents easily; document the path to 5k (sharding + zone distributors).

**Impact:** Task 7 (gRPC) needs to produce a design for the distributor layer, not just "one server". The Azure test env should include enough nodes/VMs (say 8-12 total across zones) + artificial latency or many agents to start exercising fanout code.

---

## 6-10. Other Notable Gaps (Summarized)

**6. Azure Platform Nuances (Critical for the requested test env):**
- Accelerated Networking (SR-IOV VF): eBPF tc/XDP attach works on the VF netdev in recent Azure + kernel combos, but sometimes requires specific ethtool offloads disabled or attach on the "enP..." VF name vs "eth0".
- NSGs still apply *after* or in parallel with host eBPF in some paths; test that DFW drop is visible even if NSG would allow.
- UDR + Azure Firewall in the path: traffic may be forced through a central appliance (source IP becomes the FW IP → zone lookup fails or misclassifies). For pure DFW test, peer VNets directly with no UDR or use UDR only for mgmt.
- Private Link / Service Endpoints: some traffic bypasses VNet routing.
- AKS node images & kernel matrix (must test on current AKS Ubuntu 22.04/24.04 and Azure Linux).
- Cost & quota: AKS clusters have minimums; use 1 nodepool with system+user or small B-series for test.

**7. Governance & RBAC for Ground vs Zone Rules:**
- Ground rules are "immutable baseline" (platform team).
- Zone rules are per-zone-owner overrides.
- Need namespace-per-zone or labels + ValidatingAdmissionPolicy / webhook to prevent a DMZ team from writing a zone rule that affects Prod→Internal.
- Or separate CRDs + aggregated RBAC.

**8. eBPF Datapath Low-Level (Task 5 needs this before coding):**
- Exact LPM_TRIE layout for `zone_cidr_map` (key = prefixlen + data, value = zone_id; or multiple entries).
- Two LPM lookups per packet (src + dst). Early exit if src_zone == dst_zone.
- Protocol: at minimum TCP/UDP; ICMP (type/code? treat as "port 0" or special)?
- IPv4 only for v1 (docs examples are IPv4).
- Checksum? No, just verdict.
- XDP vs tc return codes (XDP_DROP vs TC_ACT_SHOT).
- Pinning maps at a well-known bpffs path for recovery.
- Ringbuf event format must include enough for useful audit (src/dst IP+port, protocol, src_zone, dst_zone, verdict reason, rule id or priority).

**9. Testing Strategy (Biggest Gap for "did we build the right thing?"):**
- The impl plan has good progressive e2e, but the *design* itself has almost no "how we will validate" section.
- Needed: synthetic traffic generators that can run in host net ns or on VMs, spoof or use real cross-zone IPs, and assert on the *ringbuf events from both src and dst agents* that the zones were computed correctly and the dual verdict applied.
- Chaos: kill controller, measure continued enforcement + recovery time on reconnect.
- Partial update: during a rollout, have some agents on vN, some on vN+1; verify that a flow is only allowed if *both current agents* would allow it (asymmetric during transition is expected and must be safe).
- Performance microbench: packets/sec with DFW attached vs not, on a test VM.
- The Azure env (see below) is the place to run these against *real* cross-VNet routed traffic.

**10. Ops & Day-2 (Some covered in history, light in current docs):**
- Breakglass / "force allow all for zone X for 30min" (annotation or special CR that the engine honors with short TTL).
- "dfw-agent dump-maps", "test-packet <fake src dst port>", "force-apply version".
- Last-known-good persistence path and encryption at rest on disk.
- Upgrade story: agent image upgrade → new LLVM → recompile on next boot (or on signal).
- Map pressure / "too many rules for zonepair" → compiler should warn + truncate gracefully + alert.

---

## Cross-Reference to Implementation Plan

The 10-task plan is well-structured and TDD-oriented. It will produce a working skeleton quickly. However, it should be **updated** with:
- Explicit bidirectional test cases and helpers (Task 3, 10).
- Concrete map representation + port wildcard/range strategy (new sub-task in 3/5).
- Attach priority + "show current filters" + coexistence test in kind with a real CNI (Task 6 + 9).
- Stronger "Azure reality" acceptance criteria in the verification checklist.
- A dedicated "distribution sharding & zone distributor" design spike before or in Task 7.
- Task for "Zone CIDR discovery / population helper" (integrate with AKS Node status or explicit in Zone CR).

Many of the above gaps are best validated by building the **Azure test environment** (next section) and running the full matrix of ground + zone rule scenarios against real routed traffic.

---

## Conclusion & Prioritized Recommendations

1. **Clarify bidirectional consent + return traffic examples immediately** (update docs/index.html and add to engine tests).
2. **Formalize zone CIDR semantics for nodes vs pods vs overlay** (critical before any Azure deployment).
3. **Define the exact eBPF map layouts and compiler emission strategy** for verdict vs rules (before writing bpf/dfw.bpf.c).
4. **Specify attach priority numbers and coexistence behavior** with popular CNIs.
5. **Build the Azure multi-VNet + multi-AKS + multi-VM testbed** (see separate plan) as the primary validation vehicle. It will force the above issues into the open.
6. Add a "Threat Model & Failure Modes" and "Operational Runbooks" section (breakglass, partial rollout, key mgmt).
7. Consider whether the current zone model is the *only* policy language or if a lower-level "raw rule" escape hatch is needed for complex port ranges / priorities.

The foundation is sound and the zone + dual-consent idea is elegant for the stated scale and "user-agnostic outer layer" goal. The misses are typical at this stage of a design (networking details and cloud provider realities surface late). Addressing them now will save significant rework.

**Next immediate actions recommended:**
- Update the published docs + implementation plan with the bidirectional and Azure CIDR clarifications.
- Implement the Terraform test env (see companion plan).
- Run a design review sync with the broader team using this document + the Azure topology.

---

*Review performed by reading the full current docs/index.html, the implementation plan, and historical DESIGN documents. No external web search was required for this internal architecture review.*