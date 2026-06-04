/* SPDX-License-Identifier: GPL-2.0 */
/*
 * dfw.bpf.c - DFW eBPF program (data-driven)
 *
 * This is a stub. Real version (Task 5 in plan) will implement:
 * - LPM zone lookup (zone_cidr_map)
 * - Dual (egress + ingress) verdict check using verdict_map / policy_rule_map
 * - tc-BPF (and optional XDP) attachment points
 * - Double buffering via active_idx in config map
 * - Ringbuf for audit events (block_stats_map updates)
 *
 * Agents compile this from source at boot using LLVM.
 */

#include <linux/bpf.h>
#include <bpf/bpf_helpers.h>

char LICENSE[] SEC("license") = "GPL";
