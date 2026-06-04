# DFW — Distributed Firewall Implementation Plan

> **For Claude:** REQUIRED SUB-SKILL: Use superpowers:executing-plans to implement this plan task-by-task.

**Goal:** Build the core of DFW (Distributed Firewall) — a centralized policy management system with decentralized, kernel-level eBPF enforcement across heterogeneous infrastructure (K8s clusters + bare-metal/VMs) using a zone-centric default-deny model with ground rules + zone rules, delivering <1s policy propagation as described in [docs/index.html](../index.html).

**Architecture (high-level, per docs):**
- Central Controller (K8s deployment) owns Policy Registry (ground rules, zone rules, zone registry, versions), runs Policy Engine (collect/merge/validate/compile using OR logic with ground-wins semantics), and drives the Distribution Channel (unidirectional signed+encrypted map data pushes, fan-out via zone distributors if needed).
- Agents (DaemonSet on K8s nodes or standalone Podman/systemd on VMs/BMs) compile a fixed data-driven eBPF program from C source + LLVM at boot (once), then receive only map data updates, verify, and atomically apply via double-buffering to the four core maps. Enforcement is interface-scoped and strictly inter-zone only.
- Data-driven design (no bytecode push ever): eBPF program is a pure map reader + zone lookup + dual (egress+ingress) verdict check. Map updates are hot, atomic, and instant.
- Zone model: entire IPv4 partitioned into non-overlapping zones (CIDRs). Every IP in exactly one zone. Cross-zone traffic requires BOTH source egress allow AND destination ingress allow (AND of the two sides; one DENY wins). Ground rules are the immutable baseline matrix (cannot be overridden to DENY). Zone rules only open paths (DENY→ALLOW) and OR together.
- Feedback is read-only (health, version, audit drops, metrics). No policy-affecting data flows upstream.
- Coexists with (and is AND-ed before) user policies (NetworkPolicy, iptables, CNI). DFW is the outer layer.

**Tech Stack:**
- Go 1.22+ (controller + agent binaries)
- Kubernetes + controller-runtime + client-go (for CRDs, informers, leader election, Managed* inventory)
- github.com/cilium/ebpf (userspace map/program management, CO-RE friendly)
- clang/LLVM (shipped in agent image for runtime compilation of the eBPF C at boot)
- gRPC (controller <-> agent for registration, policy push, status/audit back; proto-defined map data + signatures)
- eBPF (tc-BPF primary per docs; XDP optional for early drop; ringbuf for audit events)
- Standard K8s packaging: Helm charts for controller + agent (DaemonSet + Podman examples + systemd unit)
- Testing: envtest, kind, bpftool + packet generators for kernel e2e, table-driven compiler tests

**Key Constraints & Non-Goals (from docs):**
- Agents never touch CNI interfaces (cilium_*, cni0, etc.) or overlay (VXLAN etc.). Only admin-specified host interfaces (eth0, bond0, ...).
- Enforcement scope: only host-level IPs crossing zone CIDR boundaries. Pod-to-pod inside cluster, intra-zone, user netpols are out of scope for DFW agent (user policies still apply after DFW).
- No pre-built bytecode. Agents ship C source and compile at boot (new kernel = restart + recompile).
- Strict unidirectional policy flow. Simulation/dry-run is separate (mentioned in deployment table).
- Scale target: 100+ clusters, 5000+ servers, 4+ zones, <1s P99 propagation.
- Default bootstrap: safe DENY (or explicit bootstrap-allow window for initial rollout).

**Map Data Structures (exact per docs section 6.5):**
- `zone_cidr_map`: zone_id → list of CIDR prefixes (for IP→zone LPM/ lookup)
- `policy_rule_map`: rule_key → merged rule entry (ground + zone rules)
- `verdict_map`: (src_zone, dst_zone, port) → ALLOW/DENY  (fast path for common case)
- `block_stats_map`: rule_key → counter (audit drops)

Double-buffering (active_idx flip) + atomic apply required for no-downtime updates. Last-known-good persisted for recovery.

---

## Principles for All Work
- TDD: For every behavior, write a failing test first (unit for compiler/engine, integration for controller, kernel-requiring for eBPF attach+maps).
- Small PRs / bite-sized tasks (one logical change, green build+tests, value delivered even if later PRs missing).
- Exact file paths in every task.
- Update docs/index.html or add notes only when user-facing spec changes; keep implementation docs in this plan or code comments.
- YAGNI: implement only what enables the described DFW flows (start with 4 zones example + ground+zone rules; general 5-tuple CFP is out of scope unless it falls out naturally).
- Frequent commits with clear messages.
- Progressive e2e: unit → envtest → kind (controller+agent DS) → baremetal simulation (podman in container or kind worker as "VM") → multi-node packet tests.
- All policy data on wire must be versioned, signed, and verifiable (use simple ed25519 or HMAC for v1; real PKI later).

---

## Task 1: Project Bootstrap (shared layout, go.mod, basic build)

**Files:**
- Create: `go.mod`
- Create: `Makefile` (controller-gen, bpf targets, generate, test, docker-build for controller+agent)
- Create: `README.md` (pointing to docs/index.html + quick start note)
- Create: `.gitignore` (update with current)
- Create: `docs/.nojekyll` (already present, ensure)
- Create: `hack/boilerplate.go.txt`
- Create: `config/crd/kustomization.yaml` etc. scaffolding (or use controller-gen later)

**Step 1: Write initial go.mod and basic package structure test**
```bash
# After manual creation of go.mod with module github.com/hwchiu/mon , go 1.22, key deps (controller-runtime, cilium/ebpf, grpc, etc.)
go mod tidy
go test ./... -run NONE || true
```
Expected: compiles as module; no code yet.

**Step 2: Create Makefile with targets for manifests, generate, test, docker (controller + agent with LLVM stage)**
Write minimal Makefile that:
- Has controller-gen + kustomize installers
- `make generate manifests`
- `make test`
- `make docker-build-controller docker-build-agent` (multi-stage: one with clang for agent)
Run:
```bash
make test
```
Expected: PASS (empty).

**Step 3: Add basic cmd/ skeletons that compile**
Create `cmd/dfw-controller/main.go` (just prints version + exits) and `cmd/dfw-agent/main.go` (same).
Add build targets.
```bash
make build
./bin/dfw-controller --help || true
```
Expected: builds, runs stub.

**Step 4: Commit**
```bash
git add go.mod Makefile cmd/ hack/ README.md
git commit -m "chore: bootstrap monorepo layout + build for DFW (per docs/index.html)"
```

**Value delivered:** Clean greenfield project that builds two binaries (controller, agent). Ready for API + eBPF work. Matches "greenfield bootstrap" spirit from prior design.

---

## Task 2: API Types + CRDs for Zone Model (Policy Registry foundation)

**Files:**
- Create: `api/dfw/v1alpha1/zz_generated.deepcopy.go` (bootstrap or let controller-gen)
- Create: `api/dfw/v1alpha1/groupversion_info.go`
- Create: `api/dfw/v1alpha1/zone_types.go`
- Create: `api/dfw/v1alpha1/groundrule_types.go` (or groundpolicyset)
- Create: `api/dfw/v1alpha1/zonerule_types.go`
- Create: `api/dfw/v1alpha1/policyversion_types.go` (immutable compiled snapshot)
- Create: `config/crd/bases/dfw.example.com_zones.yaml` etc. (or generate)
- Create: `config/samples/zone-dmz.yaml` + ground + zone-rule samples matching the 4-zone example in docs
- Modify: `Makefile` to include api paths for controller-gen

**Spec decisions (to support docs exactly):**
- `Zone`: spec.id (string, e.g. "zone-001"), spec.name, spec.cidrs []string (validate non-overlap at webhook later)
- `GroundRule`: spec.fromZone, spec.toZone, spec.ports (port or range or "all"), spec.protocol (tcp/udp/all), spec.action (must be consistent with matrix; we store as list of entries)
- `ZoneRule`: spec.srcZone, spec.dstZone, spec.ports, spec.protocol, spec.direction? (or infer), spec.action (ALLOW only; validation)
- `DFWPolicyVersion`: status.version (immutable hash or ULID), status.mapData (serialized bytes for the 4 maps, or structured), status.createdAt, status.groundHash, status.zoneRuleHash. Controller creates these as output of compile.

Ground rules can be modeled as a singleton `GroundPolicy` CR (or multiple + merge) that contains the full matrix or list of from/to allows. For v1, use list of GroundRule CRs + a GroundPolicyConfig singleton for defaults.

**Step 1: Write failing tests for types (validation, deepcopy)**
Create `api/dfw/v1alpha1/zone_types_test.go` with:
```go
func TestZoneCIDRValidation(t *testing.T) { ... }
func TestZoneNonOverlapping(t *testing.T) { ... }
```
Run to see failures (no types yet).

**Step 2: Implement the four type files + register in groupversion_info.go**
Include markers for CRD, validation tags.
Implement basic Validate (e.g. CIDR parse, zone id unique naming).

**Step 3: Run controller-gen + manifests + test**
```bash
make manifests generate
make test
```
Expected: CRDs generated under config/crd/bases, tests pass, samples apply-able (kubectl --dry-run).

**Step 4: Add sample data matching docs 4-zone + ground matrix + one zone rule override**
`config/samples/dfw-zone-dmz.yaml`, `ground-dmz-internal-allow-443.yaml` etc. + a deny-override attempt (should be rejected or ignored per "cannot override ALLOW to DENY").

**Step 5: Commit**
```bash
git add api/ config/ 
git commit -m "feat(api): add DFW CRDs for Zone, GroundRule, ZoneRule, PolicyVersion (foundation for Policy Registry)"
```

**Value delivered:** Users (or admins) can now `kubectl apply` the zone model and rules described in docs sections "Zone Model" + "Policy Rules". Types are the source of truth for later engine. CRDs installable.

---

## Task 3: Policy Engine Core (merge, compile, version, conflict detection)

**Files:**
- Create: `pkg/engine/engine.go`
- Create: `pkg/engine/merge.go` (OR logic, ground wins)
- Create: `pkg/engine/compile.go` (to map data structs)
- Create: `pkg/engine/version.go` (immutable version ID + metadata)
- Create: `pkg/engine/testdata/` (ground matrix YAML + zone rules + expected verdict_map etc.)
- Create: `pkg/engine/engine_test.go` (table driven for the exact matrix in docs + examples)
- Create: `pkg/types/mapdata.go` (Go structs mirroring the 4 eBPF maps: ZoneCIDREntry, VerdictKey, etc.)

**Core logic to implement (per docs 5.x + 6.x):**
- Load all Zones → build zone_id <-> CIDR LPM index + reverse.
- Load GroundRules → build baseline [src_zone][dst_zone][port] = ALLOW/DENY (normalize ports; support "all" as 0-65535 or special).
- Load ZoneRules (only for DENY ground entries) → for each (src,dst,port) OR in the ALLOWs. Detect+warn on redundant or ground-ALLOW override attempts.
- For each zone pair + relevant ports, produce verdict entries.
- Serialize to "map data blob" (or structured for the four maps). Assign version (e.g. sha256 of inputs + timestamp or ULID).
- Conflict: zone rule trying to DENY where ground ALLOW → error or ignore+warn (per "ground always wins").

**Step 1: Write failing engine tests using the exact ground matrix from docs (DMZ/Internal/Prod/Mgmt)**
E.g. test that Internal→DMZ 443 is ALLOW (ground), DMZ→Internal 443 is DENY (ground), and after adding a zone rule opening DMZ→Internal 80 it becomes ALLOW while 443 stays DENY.
```go
func TestGroundMatrix(t *testing.T) { ... }
func TestZoneRuleOverrideOnlyDenyToAllow(t *testing.T) { ... }
func TestBothEgressAndIngressRequired(t *testing.T) { ... }
```
Run `go test ./pkg/engine/... -run TestGroundMatrix` → FAIL (no impl).

**Step 2: Implement minimal merge + compile that makes the tests pass**
Pure Go, no k8s. Use in-memory lists of rules. Produce internal `CompiledPolicy` + `MapData` structs.
Support port ranges, tcp/udp, "all".

**Step 3: Add version assignment + serialization roundtrip test**
`TestCompileProducesVersionAndRoundtrippableMapData`

**Step 4: Run full package tests + add a "conflict" test case**
```bash
go test ./pkg/engine/... -v
```
Expected: all green.

**Step 5: Commit**
```bash
git add pkg/engine/ pkg/types/
git commit -m "feat(engine): implement ground+zone rule merge (OR, ground-wins) + compilation to map data + versioning"
```

**Value delivered:** Standalone `pkg/engine` that can take registry contents and produce exactly the compiled artifacts (verdicts + supporting maps) the agents will load. Usable for dry-run / simulation immediately. Tests document the semantics from docs "Enforcement Logic" and "OR Logic Examples".

---

## Task 4: Controller Skeleton + Policy Engine Integration + Version CR Controller

**Files:**
- Create: `cmd/dfw-controller/main.go` (real: manager + leader + reconcilers)
- Create: `controllers/dfw_controller.go` or split: `zone_controller.go`, `groundrule_controller.go`, `zonerule_controller.go`, `policyversion_controller.go`
- Create: `controllers/suite_test.go` (envtest boilerplate)
- Modify: `pkg/engine/...` if needed for k8s list interfaces
- Create: `internal/controller/policy_compiler.go` (watches rules/zones → calls engine → creates PolicyVersion)
- RBAC: config/rbac/ for watching the DFW CRs and creating PolicyVersion

**Step 1: Add envtest + controller tests skeleton that fail on reconcile missing**
Standard controller-runtime test that creates a Zone + GroundRule, expects a PolicyVersion to appear (or status update).
Run test → FAIL.

**Step 2: Implement manager setup + one reconciler (e.g. a "compilation trigger" that lists all and calls engine on any change)**
On create/update/delete of Zone/Ground/ZoneRule, enqueue a "compile" work item. The compile reconciler runs engine, computes new version, creates immutable PolicyVersion CR (or updates a "current" one with new version).

**Step 3: Make tests pass (envtest creates objects, asserts PolicyVersion appears with mapData or reference)**
Add test for the 4-zone matrix producing a version.

**Step 4: Add leader election, healthz, metrics in main. Stub gRPC server.**
```bash
make test
KUBEBUILDER_ASSETS=... go test ./controllers/... 
```
Expected: PASS.

**Step 5: Commit**
```bash
git add cmd/dfw-controller/ controllers/ config/rbac/ internal/controller/
git commit -m "feat(controller): controller-runtime skeleton + reconciliation that drives pkg/engine to produce PolicyVersion CRs"
```

**Value delivered:** Deployable (to kind) dfw-controller that reacts to the new CRDs and produces versioned compiled policy artifacts. Foundation for distribution.

---

## Task 5: eBPF C Program (data-driven, zone-aware, tc attach, double buffer, ringbuf)

**Files:**
- Create: `bpf/dfw.bpf.c` (or firewall.bpf.c renamed for DFW)
- Create: `bpf/include/dfw_maps.h` , helpers
- Create: `bpf/dfw.bpf.c` implementing:
  - Parse eth/ip/tcp/udp (minimal)
  - LPM or linear lookup into zone_cidr_map to get src_zone + dst_zone from IPs (only if different zones → else PASS)
  - Lookup in verdict_map (or policy) for (src_zone, dst_zone, dst_port) for egress
  - Symmetric for ingress check using reverse (dst_zone as "src" for the ingress matrix)
  - If both allow → PASS/OK else DROP + emit to ringbuf (src/dst/zone/port/action/zonepair)
  - Double buffer: active_idx in config map; two copies of verdict/zone maps or indexed
  - tc (clsact) for egress; optional xdp section for early ingress
- Create: `bpf/dfw_test.bpf.c` or use bpftool for later
- Update: Makefile with bpf compile target (clang -O2 -target bpf ... ) producing .o (even if not used by agent yet)

**Step 1: Write the C code + a minimal compile test in Makefile**
`make bpf` should produce bpf/dfw.bpf.o without errors on the host (or note kernel header reqs).

**Step 2: Add unit-like verification (use bpftool or small host test if possible)**
For now, ensure it loads conceptually (will test load in Go later).

**Step 3: Document in code the exact map keys/values matching pkg/types and docs (zone_cidr, verdict_map keyed by src_zone+dst_zone+port, etc.)**
Use __u32 for zone ids (small ints), packed structs for keys.

**Step 4: Commit the bpf/ dir**
```bash
git add bpf/
git commit -m "feat(bpf): data-driven DFW eBPF program (zone lookup + dual egress/ingress verdict, tc primary, double-buf skeleton, ringbuf audit)"
```

**Value delivered:** The fixed eBPF program (source) that agents will compile at boot. Matches "Data-Driven (Not Bytecode-Driven)" and "eBPF Map Data Structures" sections exactly. Can be inspected with bpftool once loaded.

---

## Task 6: Agent Core + LLVM Runtime Compile + eBPF Loader (cilium/ebpf)

**Files:**
- Create: `pkg/agent/config.go`
- Create: `pkg/agent/upstream.go` (gRPC client stub)
- Create: `pkg/agent/compiler.go` (calls clang/llc on the shipped bpf/dfw.bpf.c + headers → produces ELF object in /var/lib/dfw-agent/)
- Create: `pkg/agent/loader.go` (uses cilium/ebpf to load the just-compiled object, create maps, attach to specified ifaces via tc, manage double-buf)
- Create: `pkg/agent/apply.go` (receive mapdata blob for a version → populate inactive maps → flip active_idx → persist last-good)
- Create: `pkg/agent/recover.go`
- Create: `cmd/dfw-agent/main.go` (parse flags: controller addr, interfaces to protect, zone hint or auto, boot compile, connect, report health)
- Create: `Dockerfile.agent` (multi-stage: builder with clang/llvm + go, runtime minimal + copy source + compiled obj if cached)
- Create: `pkg/agent/agent_test.go` (fake loader tests; compile stub tests)

**Step 1: Failing test for boot compile flow**
Test that "CompileEBPFSource(srcDir, outObj)" produces a loadable ELF (or at least non-empty file + clang exit 0). Run on Linux with clang installed in CI.
Expected fail first.

**Step 2: Implement pkg/agent/compiler.go + loader basics (load pinned or fresh, attach tc to veth in test)**
Use `github.com/cilium/ebpf` to LoadAndAttachCollection, pin maps, etc.
For tc attach use netlink or cilium/attach helpers (or link).

**Step 3: Implement Apply + double buffer flip logic in Go + matching C side (active_idx)**
Test the flip: populate v0, set active=0, flip to 1, assert eBPF sees new data (use a side channel counter or test map read).

**Step 4: Wire main.go to do boot compile (or use prebuilt if present), RecoverOnStart, start gRPC client loop (stub), health server.**
Add flags matching deployment table (NET_ADMIN etc assumed by runner).

**Step 5: Run agent tests (some skipped without BTF/kernel) + manual smoke**
```bash
go test ./pkg/agent/... -tags=integration  # or similar
make docker-build-agent
```
Expected: agent binary + image builds; on a node with sufficient caps, agent starts, compiles, attaches (PASS for now).

**Step 6: Commit**
```bash
git add pkg/agent/ cmd/dfw-agent/ Dockerfile.agent
git commit -m "feat(agent): runtime LLVM compile of DFW eBPF + cilium/ebpf loader + double-buf apply + recovery + boot flow"
```

**Value delivered:** A runnable dfw-agent that on start compiles the C (from Task 5) to bytecode using LLVM in its container, loads it, attaches to host ifaces (tc), and can atomically update maps (when distribution wired). Matches Agent sections 8.1-8.5 perfectly. "New kernel = just restart agent" works.

---

## Task 7: gRPC Distribution Channel (proto, server in controller, client in agent)

**Files:**
- Create: `proto/dfw/v1/dfw.proto` (RegisterAgent, StreamPolicyUpdates, ReportStatus, AuditEvent, etc.)
- Generate: `pkg/proto/...` (or use buf / protoc in Makefile)
- Create: `pkg/distribution/server.go` (in controller: per-agent streams, push on new PolicyVersion)
- Create: `pkg/distribution/client.go` (in agent: dial, register with host identity + zone, receive mapdata + version + sig)
- Create: `pkg/distribution/signer.go` (sign/verify policy blobs; simple for v1)
- Update: agent upstream + controller to use real gRPC instead of stubs
- Add: certs or insecure for dev; mTLS notes in docs

**Step 1: Define proto matching the needs (PolicyUpdate { version, zone_cidrs, verdict_entries, ... , signature }) + generate Go**
Write a small test that roundtrips a message.

**Step 2: Implement controller side "pusher": on new PolicyVersion, for each connected+registered agent in the affected zones, send the delta or full mapdata for that zone.**
Support fan-out (for now direct; zone distributor is later optimization).

**Step 3: Implement agent side: on connect, register (sends node info, interfaces, current version), then handle incoming updates → verify sig → call loader.ApplyPolicy**
Add backoff + "last known good" on stream break.

**Step 4: Add integration test (envtest + fake agent or two processes) that creates rules → controller produces version → pushes to connected agent → agent reports applied version back**
Use grpc test server or real.

**Step 5: Add basic encryption note + sig verification (fail closed)**
```bash
go test ./pkg/distribution/... 
# manual: run controller + agent in kind, apply a zone rule, watch agent logs for "applied vX"
```
Expected: <1s (local) propagation visible; version matches on both sides.

**Step 6: Commit**
```bash
git add proto/ pkg/distribution/ pkg/proto/ 
git commit -m "feat(distribution): gRPC unidirectional policy push (signed map data) + registration + status reporting"
```

**Value delivered:** Closed loop: change in Policy Registry (CRs) → engine compile → PolicyVersion → push over gRPC → agent receives + applies. Read-only feedback works. Directly implements "6-Step Policy Lifecycle", "Propagation Architecture", "Performance Targets".

---

## Task 8: Atomicity, Recovery, Version Tracking, Security Hardening

**Files:**
- Extend: loader/apply.go + C side for strict double-buf + stats map updates
- Create: `pkg/agent/recovery_test.go` (simulated controller outage + agent restart)
- Create: `pkg/controller/version_tracker.go` (track per-agent applied versions, detect skew)
- Update: PolicyVersion + agent report to include full metadata (author via annotation, description)
- Add: breakglass / force-apply CLI or annotation for emergency
- Add: signature verification failure tests (tampered blob must be rejected, last-good stays)

**Step 1: Write recovery test that "kills" controller, restarts agent, asserts it re-attaches pinned maps + enforces last version without controller**
Run → will drive fixes in RecoverOnStart + pinning logic.

**Step 2: Implement + harden the flip to be truly atomic from eBPF view (single config write after populating inactive)**
Add counters in block_stats_map updated from eBPF on drops.

**Step 3: Controller side: expose "current versions per zone" + skew metrics**
Agent reports → controller updates ManagedHost-like status or separate AgentStatus CR or just metrics + logs.

**Step 4: Security: require valid signature; on verify fail, log + keep current maps + alert metric**
Test case for bad sig.

**Step 5: Commit**
```bash
git add ... (relevant)
git commit -m "feat(safety): atomic double-buf enforcement, restart recovery with last-known-good, version skew tracking, sig verification fail-closed"
```

**Value delivered:** Meets "Atomic Apply", "Fail-safe: agents retain last-known-good", "Version Tracking", "Security" requirements. Critical for production.

---

## Task 9: K8s Deployment, DaemonSet, Coexistence, Bare-Metal Support

**Files:**
- Create: `charts/dfw-controller/` (full Helm: deployment, RBAC, service, values)
- Create: `charts/dfw-agent/` (DaemonSet with privileged + caps, hostNetwork or hostPID as needed, volume for bpf pins + source, interface list via config)
- Create: `docs/agent-deployment.md` (systemd unit example, Podman run command, env vars)
- Create: `pkg/agent/discovery.go` (optional: for K8s nodes, watch pods? but per scope mostly host; zone assignment)
- Update: samples + README with "apply the 4-zone example"
- Add: coexistence notes (tc prio higher than CNI, attach before/after CNI tc filters)

**Step 1: Helm templates + make helm-lint or dry-run install in kind**
Test: helm template | kubectl apply --dry-run=server

**Step 2: Kind e2e script or target that deploys controller + agent DS on 2+ nodes, creates the docs example zones+ground+one zone-rule, verifies agents report applied version, and (advanced) injects a packet that should be dropped and see ringbuf/audit**
Use `kubectl exec` into agent or a test pod on host net to generate traffic.

**Step 3: Bare metal path: Dockerfile.agent + example systemd + "podman run" instructions that work on a Linux VM without k8s**
Verify agent runs and protects a veth pair representing "host iface".

**Step 4: Document in charts/README or docs/ the "admin-specified interfaces only" + "never touch CNI ifaces" enforcement (config validation + agent log warnings).**

**Step 5: Commit + e2e evidence**
```bash
git add charts/ docs/agent-*.md hack/e2e-kind-dfw.sh
git commit -m "feat(deploy): Helm charts for controller+agent DS, bare-metal examples, kind e2e exercising zone policy end-to-end"
```

**Value delivered:** "Shippable" per docs "Deployment Topology" table. Both K8s and VM paths work. Coexistence story clear.

---

## Task 10: Observability, Audit, Metrics, Simulation/Dry-Run, Polish

**Files:**
- Extend: ringbuf reader in agent → forward drops as structured logs + (optional) gRPC audit stream to controller
- Create: `pkg/metrics/` (prometheus: policy_version, drops by zonepair, propagation latency, map utilization, agent health)
- Create: `pkg/simulation/` (stub engine that computes "what would verdict be" without pushing; used by controller for dry-run requests or a separate SimulationService pod)
- Add: Grafana dashboard json in charts
- Add: alerting examples in docs/ops-runbook.md
- Final: update docs/index.html footer or add "Implemented in v0.1" note? (optional)
- CLI niceties: dfw-agent dump-maps, dfw-agent test-packet (synthetic lookup), dfw-controller get-effective-verdict zoneA zoneB 443

**Step 1: Wire ringbuf → agent logs + metrics for every DROP**
Test by sending cross-zone denied traffic, assert log line + counter increment.

**Step 2: Add /metrics endpoint on agent + controller; basic controller "per-zone version distribution" gauge**
Scrape in kind, assert values.

**Step 3: Implement simple simulation path: a HTTP or gRPC "dry-run" that takes (srcIP, dstIP, port) and returns the would-be verdict + which rules contributed (using the engine pkg + current compiled version)**
Useful for "Simulation Engine" mentioned in docs table.

**Step 4: Full test matrix + runbook**
Write `docs/ops-runbook.md` covering: locked out (breakglass), map full, attach fail, stale version, emergency global allow (via ground or zone rule), upgrade (agent restart = recompile).

**Step 5: End-to-end verification + commit**
```bash
# Run the kind e2e that exercises metrics, audit, simulation dry-run, and a successful cross-zone allow after zone-rule add
make e2e
```
Update plan with any gaps found.

**Value delivered:** Matches "Report", "block_stats_map", "Performance Targets", "Security", and the operational aspects (auditing, monitoring) called out in later design expansions. Platform teams can observe and trust the system.

---

## Post-v1 / Future Work (not in initial plan)
- Hierarchical zone distributors for larger scale / reduced central fan-out.
- bpfman integration for declarative program lifecycle on K8s.
- Stateful conntrack (for bidirectional convenience).
- Validating admission webhook (prevent dangerous ground changes, overlapping CIDRs).
- Sharding / prog_array for very large port or rule sets per zone.
- IPv6 full support (v1 focuses IPv4 as per docs examples).
- L7 hints passthrough (for future service mesh / Cilium L7).
- GitOps examples (ArgoCD + PolicyVersion promotion).
- Full multi-cluster via ManagedCluster patterns from sibling design (if both live in monorepo).

---

## Verification Checklist (before claiming any milestone complete)
- All unit tests green (`make test`)
- Controller installs via Helm on kind; creates CRDs
- Agent DS deploys with required capabilities; compiles eBPF at startup (visible in logs)
- Applying the exact ground matrix + one zone rule from docs/index.html produces a PolicyVersion
- Agents in affected zones receive + apply the version (reported back)
- Cross-zone traffic behaves exactly per "Connection Verdict" pseudocode (both sides checked)
- Intra-zone and same-zone traffic is untouched (PASS)
- Agent restart without controller still enforces last-good policy
- Tampered policy update is rejected
- Metrics + drop events observable
- No CNI interfaces are attached to (enforced in config + logs)
- <1s propagation in local kind (realistic P99 requires multi-node + load test later)

---

## How to Use This Plan
1. Start at Task 1. Complete fully (green, committed) before Task 2.
2. After each task, run relevant e2e smoke + update this plan with any learnings or deviations.
3. Use `git worktree` or fresh branches per major phase if parallelizing.
4. When a task is done, mark the corresponding item and run full verification checklist subset.
5. For execution with subagents: use subagent-driven-development skill with this plan as prompt.

**Plan created from exhaustive read of docs/index.html (full content) + recovered design context for tech choices and PR structure patterns.**

*Last updated: 2026-06-04 (based on docs last-updated date + current workspace state after cleanup commit).*
