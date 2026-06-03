# mon — Centralized Firewall Designs & Plans

This repository collects high-quality design documents and implementation plans for **centralized firewall controllers**.

The goal is a single source of truth for "firewall intent" (source/destination IP + port, protocol, action) that can be realized on heterogeneous targets (Kubernetes clusters via native policies, bare-metal/VM hosts via high-performance eBPF, future targets).

All designs are the result of a rigorous write → review → revise loop (multiple rounds until 0 open issues from a senior staff engineer reviewer persona).

## Design Documents

### 1. Multi-Cluster Global Firewall Policy Controller (Kubernetes / CNI Native)

- **File**: [DESIGN-multi-cluster-global-firewall-policy-controller.md](./DESIGN-multi-cluster-global-firewall-policy-controller.md)
- **Focus**: Kubernetes controller (controller-runtime + multicluster-runtime) that owns `GlobalFirewallPolicy` (GFP) + `ManagedCluster` CRs. Translates rules to standard `NetworkPolicy` (vanilla K8s) or `CiliumNetworkPolicy`/`CiliumClusterwideNetworkPolicy` (Cilium clusters). One-way authoritative propagation, per-cluster status, drift detection, finalizers.
- **Key Features**:
  - Portable L3/L4 5-tuple + label selectors (subject + peers).
  - Cluster selectors for targeting fleets.
  - No spoke CRDs/agents — pure hub-to-spoke via least-privilege kubeconfigs.
  - 8 incremental PR plan (types → MC integration → translator → controller + status + finalizer → drift → full vanilla ns support → packaging/obs/Helm → post-v1 webhook/ANP/L7).
- **Status**: Design complete (0 open issues after review). Ready for implementation starting with PR 1 (shared API types + CRDs).

### 2. Centralized eBPF Firewall Controller (High-Performance Data Plane)

- **File**: [DESIGN-centralized-ebpf-firewall-controller.md](./DESIGN-centralized-ebpf-firewall-controller.md)
- **Focus**: Complementary high-performance enforcement plane. Central controller (K8s operator or standalone) distributes compiled policy to node agents that load eBPF programs (XDP for ingress, tc for egress, cgroup for container scoping) and enforce via maps. Targets bare-metal, VMs, and K8s nodes (host + pod traffic) with very low overhead and high pps.
- **Key Features**:
  - Reuses/adapts patterns and the `GlobalFirewallPolicy` (GFP) from Design #1 where possible (eBPF controller can watch GFP for K8s nodes and perform host/pod materialization + compilation).
  - New `CentralizedFirewallPolicy` (CFP) + `ManagedHost` for broader host/VM/bare-metal fleets (labels, external ID, cloud metadata, pending approval for non-K8s).
  - Concrete eBPF: CO-RE + bpf2go/cilium/ebpf, priority-ordered rule arrays + LPM for CIDRs, ringbuf for drops, double-buffering for atomic policy swap (`active_idx` + `_0`/`_1` maps), fail-static + bootstrap allow + breakglass.
  - gRPC distribution (outbound from agents), local materialization on agents for K8s pod IPs.
  - 10 incremental PR plan (greenfield shared api bootstrap → controller + ManagedHost + gRPC skeleton → agent comm skeleton → pure-Go compiler → minimal eBPF C + loader + ringbuf attach → full distribution → atomic/restart/recovery → K8s pod resolver + GFP adapter + DaemonSet + MC sync → packaging/obs/coexistence/Helm/systemd → post-v1 bpfman + stateful + webhook + sharding).
- **Status**: Design complete (0 open issues after full review). Explicitly greenfield-aware (PR 1 creates the shared `api/firewall/v1alpha1/` package containing types from both designs for co-existence). Ready for implementation starting with PR 1 (shared API) and PR 5 (minimal XDP/tc + ringbuf loader).

## Relationship Between the Two Designs

- **Complementary, not replacement**:
  - Design #1: Excellent for pure Kubernetes environments (leverages existing CNI policy engines, no extra privileged agents on nodes for basic enforcement).
  - Design #2: High-performance host/node-level enforcement (bare metal, VMs, or as an outer host firewall layer in K8s that can coexist with Cilium/Calico via tc priority or bpfman). Can consume the same `GlobalFirewallPolicy` CRs via an adapter path on K8s nodes (materializes pod IPs locally on the agent).
- Both can run side-by-side on the same management cluster.
- Shared API group (`firewall.networking.example.com/v1alpha1`) and patterns (Managed* inventory CRs, per*Status arrays, clusterSelector, ownership labels, finalizers, partial failure handling) for operational consistency.
- Future: a single "compiler" library or a unified control plane that targets multiple backends (CNI policy objects + eBPF agents).

## Getting Started (Implementation)

Each design document contains a detailed `## PR Plan` section at the bottom with:
- Ordered, independently reviewable/mergeable PRs.
- Exact files/components affected.
- "Value delivered (standalone)" for each.
- "Builds and tests green (even without later PRs)" requirement.
- Progressive e2e (kind clusters for K8s design; kind + bare-metal simulation for eBPF).
- Tests (unit, integration, kernel-requiring for eBPF).

Recommended first steps (greenfield monorepo):
1. Read both full design docs.
2. Start with the shared API package (see PR 1 of the eBPF design, which explicitly bootstraps types from both designs + groupversion_info.go).
3. Follow the PR plans in order.

## Repository Layout (Current)

- `DESIGN-*.md` — the reviewed design documents (source of truth for implementation).
- `README.md` — this file (index + relationship).

Future (after PRs land):
- `api/`, `cmd/`, `pkg/`, `bpf/`, `charts/`, `hack/`, etc. per the PR plans.
- Possibly a `docs/` directory for rendered diagrams or additional guides.

## Prior Art & References

See the References sections inside each design document (Cilium, bpfman, controller-runtime, multicluster-runtime, AdminNetworkPolicy, Calico GlobalNetworkPolicy, xdp-tutorial, kernel BPF docs, etc.).

## Contributing / Status

These are living design documents. Issues or PRs against the designs (or the future code) are welcome once implementation begins.

Designs were produced using a structured AI-assisted review loop to maximize quality and reduce downstream rework.

---

*Last updated: 2026-06-04 (both designs approved with 0 open issues).*
