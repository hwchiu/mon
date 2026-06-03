# Design: Multi-Cluster Global Firewall Policy Controller

**Author:** Grok (AI-assisted systems architect, per user request)  
**Date:** 2026-06-03  
**Status:** Draft  
**Version:** 0.1 (initial design for greenfield project)

---

## Overview

This design proposes a central "hub" Kubernetes controller that provides a single source of truth for high-level "Firewall" intent expressed as **GlobalFirewallPolicy** (GFP) custom resources. Each GFP defines L3/L4 firewall rules using a portable 5-tuple-inspired model (source peers via CIDR or pod/namespace label selectors, source/destination ports, protocol, action=Allow/Deny) plus an optional subject selector for targeted pods.

The controller discovers registered remote Kubernetes clusters via **ManagedCluster** CRs (backed by labeled kubeconfig Secrets), selects matching clusters via label selectors, translates each GFP into the appropriate target representation—standard `networking.k8s.io/v1 NetworkPolicy` (for vanilla K8s clusters) or `cilium.io/v2 CiliumNetworkPolicy` / `CiliumClusterwideNetworkPolicy` (for Cilium-enabled clusters)—and propagates the resulting objects to the remotes.

Translation is one-way (hub → remotes). The hub controller is authoritative: it overwrites drift on remotes. Per-cluster status (success/failure, observedGeneration, applied remote policy references) is reported back on the GFP. The design supports 10s–low-100s of clusters, hundreds of GFPs, and thousands of rules through workqueues, per-cluster rate limiting, exponential backoff, and leader election.

CRDs live only on the management (hub) cluster. No spoke-side agents or CRDs are required. Remote access uses least-privilege kubeconfigs (dedicated per-cluster ServiceAccounts with narrow RBAC on policy resources).

This addresses the need for platform/SRE teams to define consistent security posture centrally while accommodating heterogeneous target clusters (some running upstream kube-proxy + NetworkPolicy, others Cilium CNI).

## Background & Motivation

Platform teams today manage security policies across fleets of clusters (dev/staging/prod, multi-region, multi-cloud, some vanilla K8s, some Cilium). Current approaches suffer from:

- **Duplication and drift**: Manually authoring `NetworkPolicy` or `CiliumNetworkPolicy` per cluster leads to copy-paste errors, version skew, and configuration drift.
- **No unified intent**: Simple firewall concepts (src IP/CIDR + port, dst IP/CIDR + port, action) must be re-expressed in CNI-specific syntax. Kubernetes NetworkPolicy lacks native src-IP for ingress in many cases, explicit Deny, L7, and cluster-wide scope. CiliumNetworkPolicy is far richer but only available where Cilium is installed.
- **Multi-cluster selection**: No first-class way to say "apply this rule only to clusters labeled `region=eu,env=prod`".
- **Operational toil**: SREs must log into each cluster, apply YAML, monitor status, and handle partial failures. GitOps tools (Argo, Flux) can push identical manifests but do not perform *translation* between NetworkPolicy ↔ CiliumNetworkPolicy forms, nor do they provide per-cluster feedback on a central object.
- **Prior art gaps**:
  - Cilium ClusterMesh provides identity-aware cross-cluster policy *when all clusters run Cilium* and are meshed; it does not solve vanilla K8s targets or central translation/ownership.
  - Calico `GlobalNetworkPolicy` (projectcalico.org/v3) is excellent for Calico fleets but is CNI-specific and not portable.
  - Kubernetes SIGs `AdminNetworkPolicy` / `BaselineAdminNetworkPolicy` (policy.networking.k8s.io/v1alpha1) provide cluster-scoped admin policies with priority + Allow/Deny/Pass, but are intra-cluster only, alpha, and not universally supported.
  - Submariner/Karmada focus primarily on L3 connectivity + service export; they can propagate resources but lack built-in 5-tuple-to-CNP translation and mixed-CNI support.
  - GitOps-centric tools (e.g. Plural) treat policies as opaque manifests and push the same YAML everywhere; they do not adapt to per-cluster CNI capabilities.
  - No existing open-source controller (as of research) owns a portable "Firewall" abstraction and emits the right CRD variant per remote cluster while reporting aggregate status.

The controller fills this gap for mixed environments while staying compatible with existing CNI policy engines.

## Goals & Non-Goals

### Goals
- Define a single, portable central abstraction (`GlobalFirewallPolicy`) capturing common L3/L4 firewall intent (peers via CIDR + label selectors for pods/namespaces, ports, protocol, action).
- Support cluster selection via label selectors on `ManagedCluster` objects.
- Automatically translate and propagate to the correct target type per `ManagedCluster.spec.policyType` ("kubernetes" → NetworkPolicy; "cilium" → CNP/CCNP).
- Provide per-cluster status and observedGeneration on the central GFP for observability and GitOps.
- Be authoritative on remotes: detect and correct drift (manual edits are overwritten).
- Handle partial failures gracefully (one cluster down does not affect others).
- Scale to ~100 clusters, ~500 GFPs, thousands of rules (with appropriate batching/rate limits).
- Package as a single Helm chart; CRDs only on hub; pure hub-to-spoke via kubeconfigs.
- Use established patterns: controller-runtime + multicluster-runtime (kubeconfig provider), standard workqueues, leader election.

### Non-Goals (for v1)
- Bidirectional sync (remotes → hub) or "import" of existing remote policies.
- Full L7 / HTTP / DNS / Kafka policy in the central model (Cilium-specific extensions may be added later as optional fields that are ignored on non-Cilium targets).
- Automatic CNI detection on remotes (explicit `policyType` on ManagedCluster; auto can be a future enhancement).
- Cross-cluster identity or service mesh integration (this is *policy distribution*, not connectivity).
- Support for host endpoints, nodes as subjects (focus on workload pods), or non-K8s targets.
- In-cluster enforcement (the controller does not replace CNI; it feeds the CNI's policy engine).
- Direct support for overlapping CIDR management or Globalnet-style IP translation (assume non-overlapping or handled by underlying network).
- Multi-tenancy / RBAC isolation inside the hub beyond standard K8s (assumes trusted platform team users).
- Migration tooling from existing per-cluster policies.

## Proposed Design

### High-Level Architecture

```mermaid
graph TD
    subgraph Hub["Hub / Management Cluster (single cluster)"]
        GFP[GlobalFirewallPolicy CRs<br/>firewall.networking.example.com/v1alpha1]
        MC[ManagedCluster CRs<br/>firewall.networking.example.com/v1alpha1]
        Secrets[(Kubeconfig Secrets<br/>labeled for multicluster-runtime)]
        Controller[GlobalFirewallPolicy + ManagedCluster<br/>Controllers<br/>(leader-elected Deployment)]
        MCController[ManagedCluster Controller<br/>(separate but same binary; sets Ready)]
        Translator[pkg/translator<br/>NetworkPolicyTranslator + CiliumTranslator]
        MCProvider[multicluster-runtime<br/>kubeconfig.Provider]
    end

    subgraph "Remote Clusters (N = 10s–100s)"
        direction LR
        C1["Cluster 'eu-prod'<br/>policyType: cilium<br/>→ CiliumClusterwideNetworkPolicy"]
        C2["Cluster 'us-dev'<br/>policyType: kubernetes<br/>→ NetworkPolicy (per ns; PR4 basic / PR6 full)"]
        C3["Cluster 'apac-staging'<br/>policyType: cilium<br/>→ CiliumNetworkPolicy + CCNP"]
    end

    GFP -->|watch + reconcile| Controller
    MC -->|watch + reconcile + status change enqueue| Controller
    MC -->|watch + reconcile| MCController
    Secrets -->|watch (label selector)| MCProvider
    Controller -->|GetCluster(name)| MCProvider
    Controller -->|list matching MCs via label selector + Ready=True filter| MC
    Controller --> Translator
    Translator -->|emit desired| C1
    Translator -->|emit desired| C2
    Translator -->|emit desired| C3

    Controller -.->|update status<br/>perClusterStatus[] (incl errors, ClusterNotReady)| GFP
    Controller -.->|create/update/delete<br/>owned remote objects<br/>(labels: managed-by, policy-name, policy-uid)| C1 & C2 & C3
    MCController -.->|label secret for provider; set Ready condition| Secrets & MC

    classDef hub fill:#e3f2fd,stroke:#1976d2
    classDef remote fill:#fff3e0,stroke:#f57c00
    class Hub hub
    class C1,C2,C3 remote
```
(Note: error/finalizer paths and "for k8s: list ns on remote (PR6)" elided for brevity; see sequence + Deletion subsections. Deny warning: translator records in status, not emitted.)

The hub runs a single Deployment (leader-elected via controller-runtime). It uses `sigs.k8s.io/multicluster-runtime` with the `kubeconfig` provider. The provider watches `Secret`s in a fixed namespace (e.g. `firewall-system`) bearing label `sigs.k8s.io/multicluster-runtime-kubeconfig: "true"` (key `kubeconfig`). Each such Secret's name becomes the logical cluster name.

A thin `ManagedCluster` controller (same binary) provides a higher-level UX:
- Users create `ManagedCluster` + corresponding Secret (pre-labeled or MC controller ensures label for kubeconfig provider).
- The MC controller (detailed Reconcile steps in "Implementation Details" + PR2):
  1. Get Secret; if absent → set Ready=False / reason=SecretNotFound.
  2. Parse/validate kubeconfig; attempt minimal client call (list Namespaces or Nodes) via engaged cluster.
  3. On success: copy labels from MC to Secret (for provider), set Ready=True / reason=Ready, record versions.
  4. On fail: Ready=False with reason, requeue with backoff.
- GFP's `clusterSelector` matches on `ManagedCluster` labels (not Secret labels directly). GFP reconcile **always filters to Ready=True** (see Reconciliation pseudocode). Label or status changes on MC enqueue affected GFPs. Non-Ready MCs: GFP sets perClusterStatus Applied=False reason="ClusterNotReady"; skips GetCluster/apply.

### Core CRDs (proposed, concrete)

**Group/Version**: `firewall.networking.example.com/v1alpha1`

**1. GlobalFirewallPolicy** (cluster-scoped)

```yaml
# Example
apiVersion: firewall.networking.example.com/v1alpha1
kind: GlobalFirewallPolicy
metadata:
  name: allow-frontend-to-backend-https
  labels:
    env: prod
    team: platform
spec:
  clusterSelector:
    matchLabels:
      region: eu
      env: prod
    # or matchExpressions
  # Subject: the endpoints to which rules are applied (evaluated on targets)
  subject:
    namespaceSelector:
      matchExpressions:
      - key: name
        operator: NotIn
        values: ["kube-system", "firewall-system"]
    podSelector: {}   # or specific labels; empty selects all in matching ns
  ingress:
  - name: "https-from-frontend"
    action: Allow
    from:
    - podSelector:
        matchLabels:
          app: frontend
      namespaceSelector:
        matchLabels:
          app-tier: frontend
    - ipBlocks:
      - "10.0.0.0/8"
      - "192.168.0.0/16"
    ports:
    - protocol: TCP
      port: 443
  egress:
  - name: "dns-egress"
    action: Allow
    to:
    - namespaceSelector:
        matchLabels:
          name: kube-system
      podSelector:
        matchLabels:
          k8s-app: kube-dns
    ports:
    - protocol: UDP
      port: 53
  # Optional: Cilium-only extensions (ignored on kubernetes targets)
  # ciliumExtensions:
  #   enableDefaultDeny:
  #     ingress: true
  #     egress: true
status:
  observedGeneration: 7
  perClusterStatus:
  - clusterName: "eu-prod-01"
    observedGeneration: 7
    conditions:
    - type: Applied
      status: "True"
      lastTransitionTime: "..."
      reason: "Reconciled"
    remotePolicies:
    - kind: CiliumClusterwideNetworkPolicy
      name: gfp-allow-frontend-to-backend-https-7f3a2
      # no namespace (clusterwide)
    lastSyncTime: "..."
  - clusterName: "us-dev-42"
    observedGeneration: 6   # lagging
    conditions:
    - type: Applied
      status: "False"
      reason: "RemoteUnavailable"
      message: "context deadline exceeded contacting apiserver"
    ...
```

**Go type sketch** (api/firewall/v1alpha1/globalfirewallpolicy_types.go; full minimal for implementation; also see groupversion_info.go below):

```go
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=gfp
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type="string",JSONPath=".status.conditions[?(@.type=='Applied')].status"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

type GlobalFirewallPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   GlobalFirewallPolicySpec   `json:"spec,omitempty"`
	Status GlobalFirewallPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type GlobalFirewallPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GlobalFirewallPolicy `json:"items"`
}

type GlobalFirewallPolicySpec struct {
	ClusterSelector metav1.LabelSelector `json:"clusterSelector,omitempty"`
	Subject         EndpointSubject      `json:"subject"`
	Ingress         []FirewallRule       `json:"ingress,omitempty"`
	Egress          []FirewallRule       `json:"egress,omitempty"`
	// Cilium-specific knobs (best-effort on cilium targets only; ignored elsewhere)
	Cilium *CiliumPolicyHints `json:"cilium,omitempty"`
}

type EndpointSubject struct {
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
	PodSelector       *metav1.LabelSelector `json:"podSelector,omitempty"`
}

type FirewallRule struct {
	Name   string   `json:"name,omitempty"`
	Action string   `json:"action"` // "Allow" | "Deny"
	From   []Peer   `json:"from,omitempty"` // for ingress rules
	To     []Peer   `json:"to,omitempty"`   // for egress rules
	Ports  []Port   `json:"ports,omitempty"`
}

type Peer struct {
	IPBlocks          []string                 `json:"ipBlocks,omitempty"`
	NamespaceSelector *metav1.LabelSelector    `json:"namespaceSelector,omitempty"`
	PodSelector       *metav1.LabelSelector    `json:"podSelector,omitempty"`
}

type Port struct {
	Protocol corev1.Protocol      `json:"protocol,omitempty"`
	Port     intstr.IntOrString   `json:"port,omitempty"`
	// EndPort for ranges in future; srcPort intentionally omitted in v1 (ephemeral + limited CNI support)
}

type CiliumPolicyHints struct {
	EnableDefaultDeny *struct {
		Ingress *bool `json:"ingress,omitempty"`
		Egress  *bool `json:"egress,omitempty"`
	} `json:"enableDefaultDeny,omitempty"`
	// Other Cilium L7 etc. can be added here as map or struct; translator ignores on non-cilium
}

type RemotePolicyRef struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"` // empty for cluster-scoped like CCNP
}

type PerClusterStatus struct {
	ClusterName        string                 `json:"clusterName"`
	ObservedGeneration int64                  `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition     `json:"conditions,omitempty"`
	RemotePolicies     []RemotePolicyRef      `json:"remotePolicies,omitempty"`
	LastSyncTime       *metav1.Time           `json:"lastSyncTime,omitempty"`
	ErrorCount         int                    `json:"errorCount,omitempty"`
}

type GlobalFirewallPolicyStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	PerClusterStatus   []PerClusterStatus `json:"perClusterStatus,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"` // top-level for GFP itself
}

// Finalizer const (used in controller + deletion logic)
const (
	GlobalFirewallPolicyFinalizer = "firewall.networking.example.com/finalizer"
)

// Ownership label consts (for drift, list owned, cleanup; see Implementation Details)
const (
	ManagedByLabel     = "firewall.networking.example.com/managed-by"
	PolicyNameLabel    = "firewall.networking.example.com/policy-name"
	PolicyUIDLabel     = "firewall.networking.example.com/policy-uid"
	PolicyGenLabel     = "firewall.networking.example.com/policy-generation"
)
```

**groupversion_info.go (api/firewall/v1alpha1/)** (standard boilerplate):

```go
package v1alpha1

import (
	"k8s.io/apimachinery/pkg/runtime/schema"
	"sigs.k8s.io/controller-runtime/pkg/scheme"
)

var (
	GroupVersion = schema.GroupVersion{Group: "firewall.networking.example.com", Version: "v1alpha1"}

	SchemeBuilder = &scheme.Builder{GroupVersion: GroupVersion}

	AddToScheme = SchemeBuilder.AddToScheme
)

func init() {
	SchemeBuilder.Register(&GlobalFirewallPolicy{}, &GlobalFirewallPolicyList{})
	// Register ManagedCluster etc. here too
}
```

**2. ManagedCluster** (cluster-scoped; parallel full Go sketch in `managedcluster_types.go`):

```yaml
# (YAML unchanged from before; see full example above)
```

```go
// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=mc
// +kubebuilder:subresource:status

type ManagedCluster struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ManagedClusterSpec   `json:"spec,omitempty"`
	Status ManagedClusterStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type ManagedClusterList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagedCluster `json:"items"`
}

type ManagedClusterSpec struct {
	KubeconfigSecretRef     SecretRef             `json:"kubeconfigSecretRef"`
	PolicyType              string                `json:"policyType"` // "cilium" | "kubernetes"
	TargetNamespaceSelector *metav1.LabelSelector `json:"targetNamespaceSelector,omitempty"`
	// paused etc. for rollout
}

type SecretRef struct {
	Name      string `json:"name"`
	Namespace string `json:"namespace"`
}

type ManagedClusterStatus struct {
	Conditions            []metav1.Condition `json:"conditions,omitempty"`
	LastSeenVersion       string             `json:"lastSeenVersion,omitempty"`
	DiscoveredCiliumVersion string           `json:"discoveredCiliumVersion,omitempty"`
}

// Ready condition reason examples (used by MC controller)
const (
	ReasonSecretNotFound = "SecretNotFound"
	ReasonConnectionFailed = "ConnectionFailed"
	ReasonReady = "Ready"
)
```

(zz_generated.deepcopy.go is controller-gen generated; no manual sketch needed beyond types.)

This now provides complete, compilable-minimal types + markers for direct use with controller-gen + code generation. Engineer can `make manifests` from this.

**2. ManagedCluster** (cluster-scoped)

```yaml
apiVersion: firewall.networking.example.com/v1alpha1
kind: ManagedCluster
metadata:
  name: eu-prod-01
  labels:
    region: eu
    env: prod
    cni: cilium
spec:
  kubeconfigSecretRef:
    name: eu-prod-01-kubeconfig   # must exist in firewall-system ns
  policyType: "cilium"            # "cilium" | "kubernetes"
  targetNamespaceSelector:        # optional filter for where to place namespaced policies
    matchExpressions:
    - key: name
      operator: NotIn
      values: ["kube-system"]
status:
  conditions:
  - type: Ready
    status: "True"
  lastSeenVersion: "v1.29.3"
  discoveredCiliumVersion: "1.15.3"  # best effort
```

The corresponding Secret (created by user or external secret operator):

```yaml
apiVersion: v1
kind: Secret
metadata:
  name: eu-prod-01-kubeconfig
  namespace: firewall-system
  labels:
    sigs.k8s.io/multicluster-runtime-kubeconfig: "true"
type: Opaque
data:
  kubeconfig: <base64>
```

### Package & Controller Layout (proposed)

**Note on paths**: `api/` (not `pkg/controllers/api` or similar; root-level standard for Kubebuilder-style). Group `firewall.networking.example.com` is a placeholder/example domain (not registered real domain; replace with your org's in real use, e.g. `policy.internal.example.com`).

```
/cmd/manager/main.go
/api/
  /firewall/v1alpha1/
    globalfirewallpolicy_types.go
    managedcluster_types.go
    zz_generated.deepcopy.go
    groupversion_info.go
  /multicluster/... (if needed for provider extensions)
/controllers/
  globalfirewallpolicy_controller.go   // primary; fan-out per matching MC
  managedcluster_controller.go         // secret labeling, connectivity checks, status
/pkg/
  /translator/
    translator.go                      // interface + common
    networkpolicy.go                   // to vanilla networking.k8s.io/v1
    cilium.go                          // to cilium.io/v2 CNP + CCNP
    util.go                            // selector conversion, name hashing
  /multicluster/
    client.go                          // thin wrapper around mcmanager
  /metrics/
    metrics.go                         // custom prometheus collectors
  /util/
    names.go                           // deterministic remote object naming: "gfp-<slug>-<genhash>"
/config/
  /crd/... (generated)
  /rbac/... 
  /manager/...
/charts/
  firewall-controller/
    templates/
      deployment.yaml
      ...
    crds/
      ...
```

Entry point uses standard `ctrl.NewManager` + `mcmanager.New(..., provider)` (see multicluster-runtime patterns).

The GFP reconciler (pseudocode; full impl in PR4; see also "Deletion and Finalizer" and "Lifecycle Management" for delete/lifecycle paths):

1. Fetch GFP (by name from request).
2. List ManagedClusters: use label selector from `spec.clusterSelector` **AND** filter (post-list or via field indexer on `status.conditions[?(@.type=="Ready")].status == "True"`) for only Ready MCs. (See Issue 3 handling below; MC controller populates Ready.)
3. For each qualifying MC (Ready=True):
   a. `cl, err := mgr.GetCluster(ctx, mc.Name)`; if err (e.g. not engaged or MC lost Ready), record in perClusterStatus with Applied=False / reason="ClusterNotReady" and **continue** (do not abort other MCs).
   b. `desired, err := translator.Translate(gfp, mc)`; handle err by recording + continue.
   c. List existing owned: label selector `firewall.networking.example.com/policy-name=<gfp.Name> AND .../managed-by=...` (field indexer recommended on remote for owned list).
   d. Create/Update/Delete to match (prefer SSA for safety where supported; fall back to CreateOrUpdate; always set ownership labels + source-gen annotation).
   e. **Vanilla note (PR4/PR6 split, see Issue 1)**: In PR4, basic kubernetes support assumes subject selects a single known/target ns (or hardcodes from MC if no selector; emits 1 NP). Full cross-ns discovery + `targetNamespaceSelector` filtering from MC + List Namespaces on remote is in PR6 (enhancement). Translator and reconcile call out "if policyType==kubernetes && PR6 not yet: use minimal single-ns path; else full".
   f. Record per-cluster result (success + remote refs, or error + ErrorCount++ , condition).
4. Aggregate: build final status (merge perClusterStatus entries; use observedGeneration check to avoid unnecessary patches). Patch status with `client.Status().Patch(..., mergePatch)` or SSA (compare generations to avoid races).
5. Decide Result + requeue:
   - Collect `failed := map[clusterName]error{}` during loop (never abort early).
   - If len(failed)>0: requeue with per-cluster backoff (use workqueue rate limiter; key items internally as "gfp:<name>/cluster:<cname>" or separate limiter map + delay based on worst error; circuit breaker: after 5 consecutive fails for a cluster, backoff to 5m).
   - Always requeue on transient (e.g. rate limit exceeded).
   - Success (no failed): no requeue (or long resync).

**Error/partial sync details (addresses Issue 5)**: The loop always continues across MCs for partial success. Overall ctrl.Result is `reconcile.Result{RequeueAfter: computeBackoff(failed)}` if any failed, else empty. Status is patched even on partials (so some clusters show success, others error). Integration tests cover "1 remote down, others succeed".

Watch setup (via builder or mc builder):
- Watch GFP on hub.
- Watch ManagedCluster on hub (enqueue all affected GFPs on MC label *or status condition* change, using predicate or indexer).
- Via multicluster sources: watch the target policy GVKs on engaged clusters; on update/delete of an *owned* remote object, map back to GFP name (via labels) and enqueue GFP for drift correction.
- (PR2+) MC Ready status changes enqueue affected GFPs.

Name generation for remotes (deterministic, stable across reconciles):
- `gfp-<GFP-name>-<short-hash-of-relevant-spec>` (include gen or observed fields that affect output).
- For namespaced targets: same name in each target ns.
- For CCNP: name is cluster-unique.

### Implementation Details & Code Sketches (for Issues 2/7)

**Label consts** (defined in types.go; used everywhere for ownership/drift/list/cleanup):
```go
const (
	ManagedByLabel  = "firewall.networking.example.com/managed-by"  // value: "globalfirewallpolicy-controller"
	PolicyNameLabel = "firewall.networking.example.com/policy-name"
	PolicyUIDLabel  = "firewall.networking.example.com/policy-uid"
	PolicyGenLabel  = "firewall.networking.example.com/policy-generation"
)
```

**Name hash pseudocode** (`pkg/util/names.go`):
```go
func RemotePolicyName(gfp *v1alpha1.GlobalFirewallPolicy, mcName string, policyType string) string {
    h := sha256.New()
    // Hash stable fields that affect emitted content (not status):
    json.NewEncoder(h).Encode(gfp.Spec.Subject)
    json.NewEncoder(h).Encode(gfp.Spec.Ingress) // or sorted canonical
    json.NewEncoder(h).Encode(gfp.Spec.Egress)
    json.NewEncoder(h).Encode(gfp.Spec.Cilium) // if present
    // include observedGeneration? No, use spec gen for name stability; annotation carries source gen
    short := hex.EncodeToString(h.Sum(nil))[:8]
    return fmt.Sprintf("gfp-%s-%s", gfp.Name, short)
}
```
(Exact fields documented in translator tests.)

**Translator interface** (`pkg/translator/translator.go`):
```go
type Translator interface {
    // Translate returns the list of desired client.Objects (NetworkPolicy or CNP/CCNP) for this GFP+MC.
    // Errors are non-recoverable for this MC (e.g. invalid spec for target type).
    Translate(ctx context.Context, gfp *firewallv1alpha1.GlobalFirewallPolicy, mc *multiclusterv1alpha1.ManagedCluster) ([]client.Object, error)
}

type networkPolicyTranslator struct { /* ... */ }
type ciliumTranslator struct { /* ... */ }

func NewTranslatorForPolicyType(policyType string) Translator { ... }
```

**MC controller Reconcile steps** (numbered, in `controllers/managedcluster_controller.go`; PR2):
1. Fetch MC.
2. If deleting: remove provider label from secret if present; remove finalizer if used; return.
3. Get Secret ref; if not found or no kubeconfig key: set condition Ready=False, reason=ReasonSecretNotFound; requeue.
4. Parse kubeconfig to rest.Config; create temp client or use provider.Get if already engaged.
5. Perform smoke: list 1 Namespace or Node (with short timeout ctx).
6. If success: ensure Secret has provider label; copy relevant MC labels/annotations to Secret (for discoverability); set MC status Ready=True + versions; update MC.
7. On any error: Ready=False + reason; requeue with backoff.
8. Enqueue logic: watch MC + owned Secrets; on status change enqueue GFPs via label or separate mapping (or just rely on GFP watching MC status changes).

**Remote update event handler** (in GFP controller, PR5): On remote policy update (from mc source), if has our ownership labels: extract `policy-name` label -> enqueue GFP by that name (use client in hub to Get GFP and re-reconcile for drift).

**GVK consts / owned list selector** (pkg/util or controller):
```go
var (
    OwnedLabelSelector = labels.SelectorFromSet(labels.Set{ManagedByLabel: "globalfirewallpolicy-controller"})
)
```
Use `List(..., client.MatchingLabelsSelector{Selector: ...})` on remote client. Prefer field indexer for policy-name on remotes in PR5+.

**SSA vs CreateOrUpdate**: Prefer SSA (`client.MergeFrom` or `Apply` patch) for owned objects in PR4+ for safety; fall back documented. Chosen per GVK (CCNP benefits from SSA).

**Other**: Exact GVKs registered via AddToScheme; field indexers for MC Ready and remote owned (registered in PR2/5 via provider.IndexField).

These sketches + consts make the design directly implementable.

### Translation Pipeline (detailed)

```mermaid
flowchart TD
    GFP[GlobalFirewallPolicy] --> Subject[subject + clusterSelector]
    Subject --> MCs[List matching ManagedClusters]
    MCs --> Decision{policyType?}
    Decision -->|cilium| CiliumTx[Cilium Translator]
    Decision -->|kubernetes| K8sTx[NetworkPolicy Translator]
    CiliumTx -->|construct endpointSelector from subject<br/>+ from/toPeers → fromEndpoints / toEndpoints / fromCIDR / toCIDR<br/>+ ports → toPorts<br/>+ action=Deny → egressDeny / ingressDeny<br/>+ use CCNP if subject spans ns or empty nsSelector| CCNP[CiliumClusterwideNetworkPolicy<br/>or CNP]
    K8sTx -->|PR4 basic: single/hardcoded ns (or MC target if set)<br/>PR6 full: List ns on remote matching subject.nsSelector + MC.targetNamespaceSelector<br/>  NP in each such ns<br/>  podSelector = subject.podSelector<br/>  from: nsSelector/podSelector/ipBlock (note: src port ignored)<br/>  action=Deny: skipped / warning in status<br/>  policyTypes derived from presence of ingress/egress| NPs[NetworkPolicy objects<br/>(one per target ns)]
    CCNP & NPs --> Apply[Apply with labels/annotations<br/>for ownership + observed gen]
```

**Key translation rules (concrete):**

- **Subject → selector**: For CCNP/CNP: `endpointSelector` built from podSelector + (if present) namespace label match via `k8s:io.kubernetes.pod.namespace` (Cilium convention). For vanilla NP: the NP lives *in* the selected namespace(s); `podSelector` copied verbatim.
- **Peer (ipBlocks)** → `ipBlock` (K8s) or `fromCIDR`/`toCIDR` (Cilium). Support `except` later if needed via CIDRSet.
- **Peer (pod/namespace selectors)** → `namespaceSelector` + `podSelector` in both (Cilium uses `fromEndpoints`/`toEndpoints` containing the selector).
- **Ports**: Map to `ports` array. Protocol defaults to TCP. Source ports: not expressed in v1 (rarely useful + poor support in vanilla NP).
- **Action Deny**: Only emitted for Cilium targets (as `ingressDeny`/`egressDeny` rules). For kubernetes targets: rule is dropped; status condition `Warning` + `reason: DenyUnsupportedOnKubernetes`.
- **Multiple rules**: Each GFP rule becomes a separate entry inside the *single* remote policy object (CNP supports `spec` or `specs`; we use one CNP per GFP for traceability). K8s NP also supports multiple rules inside one object.
- **Cilium hints**: If `spec.cilium` present and target=cilium, merge (e.g. `enableDefaultDeny`).
- **Ordering**: GFP rules appear in the order listed in the YAML. No numeric priority in v1 (Calico-style `order` can be added later). Overlaps between *different* GFPs are resolved by the target CNI (Cilium deny usually wins; vanilla NP is additive allow-list).

**Example translation output (abridged)** for a Cilium target:

```yaml
apiVersion: cilium.io/v2
kind: CiliumClusterwideNetworkPolicy
metadata:
  name: gfp-allow-frontend-to-backend-https-7f3a2
  labels:
    firewall.networking.example.com/managed-by: globalfirewallpolicy-controller
    firewall.networking.example.com/policy-name: allow-frontend-to-backend-https
    firewall.networking.example.com/policy-uid: "xxx"
    firewall.networking.example.com/policy-generation: "7"
  annotations:
    firewall.networking.example.com/source-generation: "7"
spec:
  endpointSelector: { ... constructed from subject ... }
  ingress:
  - fromEndpoints: [ { matchLabels: {app: frontend, ...} } , ... ]
    fromCIDR: ["10.0.0.0/8", ...]
    toPorts:
    - ports: [{port: "443", protocol: TCP}]
  # deny rules if any under ingressDeny / egressDeny
```

For vanilla, multiple NPs (one per target ns) with analogous but limited fields.

### Reconciliation & Watch Flows

```mermaid
sequenceDiagram
    participant User
    participant API as Hub API Server
    participant GFPctrl as GFP Controller
    participant MCProvider as multicluster-runtime Provider
    participant Remote as Remote Cluster API

    User->>API: kubectl apply GlobalFirewallPolicy
    API->>GFPctrl: enqueue (via informer)
    GFPctrl->>API: list ManagedClusters matching selector **AND Ready=True condition** (post-filter or indexer)
    loop per qualifying (Ready) MC
        GFPctrl->>MCProvider: cl, err := GetCluster(mc.Name); if err: update status "ClusterNotReady"; continue (partial sync)
        GFPctrl->>GFPctrl: desired, err := translator.Translate(gfp, mc); if err: record + continue
        GFPctrl->>Remote: List existing owned remote policies (label selector)
        GFPctrl->>Remote: for each desired: CreateOrUpdate (or SSA)  [PR4: basic k8s single-ns; full ns list in PR6]
        alt vanilla target (basic in PR4)
            GFPctrl->>Remote: (assume/minimal) emit NP
        end
        GFPctrl->>API: update perClusterStatus (even on partials)
    end
    Note over GFPctrl: aggregate failed map; requeue with per-cluster backoff if any failed; always patch status
    Note over Remote: Manual edit of remote CNP
    Remote-->>GFPctrl: (via mc watcher on engaged cluster) update event for owned object
    GFPctrl->>GFPctrl: map remote labels → GFP name; enqueue GFP
    GFPctrl->>Remote: overwrite back to desired (drift correction)
```

### Conflict Resolution & Ordering

- **Intra-GFP**: Rules within one GFP are emitted in source order inside one target object. CNI semantics apply.
- **Inter-GFP**: Separate target objects per GFP. No central merging. Target CNI combines (union of allows; Cilium denies take precedence if both Allow and Deny rules match the same flow).
- **Mitigation**: Document that users should partition via clusterSelectors or use a single comprehensive GFP per "intent tier". Future: add `priority` field (0 = highest) on GFP; translators can annotate or (for Cilium) rely on creation timestamp + name sorting if CNI supports; for ANP-capable targets, map to AdminNetworkPolicy (advanced).
- On delete of GFP: finalizer holds until all remote owned objects are deleted (best-effort; timeout after N attempts). See dedicated "Deletion and Finalizer" subsection below for full logic.

### Deletion and Finalizer

The controller uses a finalizer (`firewall.networking.example.com/finalizer`) on `GlobalFirewallPolicy` to ensure remote cleanup. Cross-cluster ownerReferences are impossible, so we rely on ownership labels (`firewall.networking.example.com/managed-by=globalfirewallpolicy-controller`, `firewall.networking.example.com/policy-uid=...`, `firewall.networking.example.com/policy-name=...`) + historical status for traceability. Cleanup is per-MC (current + historical from status.perClusterStatus), best-effort, and never blocks forever.

**Numbered logic (implemented in GFP controller Reconcile delete path, PR4):**

1. On GFP create/update (if finalizer absent): Add finalizer via `controllerutil.AddFinalizer` + patch metadata (idempotent).

2. On GFP delete (deletionTimestamp set, finalizer present):
   - Collect target MCs: union of (a) MCs currently matching `spec.clusterSelector` (list + Ready filter per Issue 3), and (b) MCs referenced in `status.perClusterStatus` (historical, even if MC deleted or selector shrunk).
   - For each such MC (skip if !Ready or GetCluster fails permanently):
     - Obtain remote client.
     - List owned remote policies using label selector: `firewall.networking.example.com/policy-name=<gfp-name>` AND `firewall.networking.example.com/managed-by=...` (use field indexer on remote cache for efficiency if registered).
     - For each listed object: Delete (use DeleteOptions with PropagationPolicy=Background or Foreground as appropriate; for CCNP use cluster-scoped delete).
     - On per-object delete error (e.g. NotFound = success; conflict = retry; permanent auth = log + mark): record in a transient map or annotation on GFP (e.g. `cleanup-attempts-<mc>=N` or status extension). Update perClusterStatus for that MC with a "CleanupInProgress" condition.
   - Track overall: successes vs. pending. Requeue with exponential backoff (workqueue rate limiter keyed by GFP+ "cleanup" + cluster; max 10 attempts per MC or 5m total wall time).
   - On permanent MC unavailability (e.g. MC deleted, or GetCluster fails with auth after retries): log warning, remove the perClusterStatus entry for it (after attempting label-based list/delete if client was previously engaged), proceed. Do not orphan objects silently; attempt delete via last-known client if cached.
   - Only when *all* MCs report cleanup success (or max attempts exceeded + force-remove): remove finalizer via `controllerutil.RemoveFinalizer` + patch. If force-remove, leave a final "OrphanedRemotes" condition on GFP status for audit.
   - During cleanup, continue to serve status updates; do not block other GFPs.

3. MC deletion interaction: MC controller (or deletion watcher) enqueues all GFPs that have that MC in their historical perClusterStatus (via label or separate index). Those GFPs treat it as "dropped" and run the per-MC cleanup step above (remove status entry post-clean).

4. GFP clusterSelector shrink (drops an MC): On next reconcile, the dropped MC (if in historical status) triggers cleanup of its remote objects (using the ownership labels) + removal of its perClusterStatus entry. This is explicit, not relying on drift.

5. Tests: Unit tests for partial cleanup (1 of 3 MCs fails permanently), finalizer add/remove, requeue during cleanup, MC delete mid-cleanup. Integration: create GFP, delete it, assert remotes gone within timeout.

This makes "best-effort; timeout after N attempts" (N=10 per-MC or configurable via flag) precise and safe. Finalizer removal is gated on success or explicit force (with audit trail).

**Simple delete/finalizer flow Mermaid** (4th diagram for Issue 11; complements sequence):

```mermaid
sequenceDiagram
    participant User
    participant API
    participant GFPctrl
    participant MCs
    participant Remotes

    User->>API: delete GlobalFirewallPolicy
    API->>GFPctrl: enqueue (deletionTimestamp set)
    GFPctrl->>GFPctrl: add finalizer if absent (on create path)
    GFPctrl->>MCs: collect current matching + historical from status
    loop per MC (Ready or historical)
        GFPctrl->>Remotes: List owned by labels (policy-name + managed-by)
        GFPctrl->>Remotes: Delete each (background); record success/fail in temp status
    end
    alt all cleaned or max attempts
        GFPctrl->>API: remove finalizer
    else pending
        GFPctrl->>API: requeue with backoff; update CleanupInProgress conditions
    end
```

### Lifecycle Management (CNI transitions, MC delete, selector changes, status bloat; addresses Issue 9 + OQs)

In "Reconciliation & Watch Flows" + Deletion logic (and implemented in PR4+), the following default behaviors apply (proposed answers to OQs; not left as open questions):

- **CNI type transition (OQ6)**: On MC `spec.policyType` change (e.g. kubernetes -> cilium): the GFP reconcile for that MC will (a) list/delete old GVK objects using ownership labels (old kind like NetworkPolicy or old CNP), (b) emit new GVK objects per current policyType. Status updated with transition note. No manual intervention needed; old objects cleaned for traceability. (Documented; tested in e2e.)

- **MC deletion**: MC controller (or finalizer if added) enqueues GFPs referencing it in historical status. GFP reconcile: for the deleted MC, attempt ownership-label list+delete of any lingering remote objects (via last-known client if possible, else skip), then remove the perClusterStatus entry. Objects are not left behind if avoidable; status entry pruned. Provider disengages as before.

- **GFP clusterSelector shrink (drops MC)**: Dropped MC (from historical status) treated like "delete for that MC": explicit cleanup of its remotes via labels + remove its status entry. (Prevents drift-owned orphans.)

- **Status pruning (OQ5)**: On every GFP reconcile, prune perClusterStatus entries for MCs that (a) no longer exist (List MCs), or (b) have not been seen/updated in last N reconciles (N=3 or configurable; use lastSyncTime). Keeps object size bounded (e.g. <100 entries even for 500 GFPs). Historical info is in events/logs if needed.

- **OQ1 (secret vs MC embed)**: Default: keep secrets as provider contract (secrets hygiene + rotation); MC is metadata/selector/Ready surface only. MC controller can optionally sync label but does not embed. (Future: support embed behind flag.)

- **OQ2 (Cilium ns label)**: Use `k8s:io.kubernetes.pod.namespace` (standard in Cilium docs 1.14+); pin supported Cilium >=1.15 in docs; make configurable in CiliumPolicyHints if needed.

- **OQ3 (ANP backend)**: Future (post-v1 in PR8); translator can have "anp" policyType path emitting AdminNetworkPolicy on capable clusters (priority from GFP if added).

- **OQ4 (src ports)**: Omitted in v1 (ephemeral nature + weak support in both NP and CNP for src-port matching in most rules). Can add `srcPorts` to Port struct + translation note later.

- **OQ7 (validating webhook)**: v1.1+; prevents obvious bad rules (0.0.0.0/0 + privileged ports) at admission.

These are now **proposed defaults in the design body** (not just listed in OQs). OQ list can be trimmed or marked "resolved in design".

### Drift Detection & Reconciliation

Controller is strictly authoritative. Any change to a remote object bearing the ownership labels that does not match the current translation is reverted on the next reconcile. Reconciles are triggered by:
- GFP/MC changes on hub.
- Remote policy object changes (watched via per-cluster informers).
- Periodic resync (standard cache).

Users can "adopt" by deleting the central GFP (remotes are cleaned) or by matching the generated objects manually (not recommended).

### Scale Considerations

- **Clusters**: 100. Each MC engage starts a cache + client (memory ~ tens of MB per cluster for policy informers only; we register only the two policy GVKs + Namespaces for vanilla ns discovery).
- **GFPs**: 500. Each GFP reconcile fans out to N matching clusters (worst-case 100). Use `workqueue` with per-item (GFP+cluster) rate limiter.
- **Client rate limiting**: Per-remote rest.Config with `QPS`/`Burst` tuned (e.g. 20/40); shared transport where possible.
- **Batching**: Translators produce small lists (1 CCNP or N NPs where N = #target ns, typically <100). Use SSA patches.
- **Leader election**: Standard; only one replica reconciles.
- **Memory/CPU targets**: < 1Gi / 500m for hub under load. Metrics to validate.
- **Storage**: Negligible (status arrays; 100 clusters × 500 GFPs × 1kB ≈ 50 MiB worst case, but sparse).

### Concurrency and Resource Model (addresses Issue 10)

GFP reconcile performs **serial per-MC sync** (to bound memory/ thundering herd on 100 clusters; one GetCluster + translate + list/apply at a time per GFP reconcile). Use `errgroup` + semaphore (tunable via `controller.maxClusterConcurrency=10` flag/Helm value, default 5) for limited *intra-GFP* parallelism if desired (e.g. independent clusters); errors still collected. 

Per engaged cluster: ~2-3 informers (target policies + Namespaces for vanilla) x base cache (~few MB each for policy objects). Total for 100 clusters: ~200-500 MiB cache + hub caches for 500 GFPs/MCs (~100 MiB) + controller overhead; target <1Gi.

**Backpressure**: per-cluster rest QPS/Burst + workqueue rate limiter + circuit breaker (5 fails -> 5m backoff per GFP+cluster key). Drift watches add informers but only for engaged clusters (lazy).

**Report-only / dry-run**: First-class in rollout (Helm `dryRun: true` or `ENABLE_DRY_RUN=true`); controller does all logic + status updates ("would apply X objects") but skips Create/Update/Delete. Metrics: `remote_policies_would_apply_total`. E2e test covers it. Makes "future flag" concrete and testable early (PR7).

**Etcd/API load**: Status patches are generation-gated + merge/SSA (not full replace). Per-cluster clients are rate-limited. Watch pressure bounded by registering only needed GVKs per cluster. Document: "For 100 clusters expect ~10-20 QPS aggregate from hub under steady state; monitor apiserver."

Concurrency test: e2e with 5+ fake remotes + load GFP updates; assert no thundering, partial success, bounded memory.

### Data Model Changes

- New CRDs only (no changes to core K8s types).
- On hub: etcd stores GFPs + MCs + status.
- No migrations needed (greenfield).
- Remote objects are *derived*; no central storage of remote state beyond status.

## API / Interface Changes

No changes to existing K8s APIs. New CRDs as defined above.

The controller exposes no new HTTP/gRPC API; all interaction via standard K8s CRUD + status subresource + events.

Example `kubectl` UX remains unchanged.

## Alternatives Considered

### 1. Pure GitOps + Kustomize/Helm per cluster (no custom controller)
- **Description**: Store GFP-like YAML in Git; use Kustomize overlays or Helm values per cluster to emit the correct NP vs CNP variant; Argo/Flux applies.
- **Pros**: No new controller; leverages existing GitOps.
- **Cons**: Translation logic lives in templates (hard to maintain, no validation, no status feedback on central object, no automatic drift correction from central, no clusterSelector dynamism without custom generators, poor partial-failure handling). Cannot easily implement "list target ns on remote and create per-ns NP".
- **Trade-off**: Simpler initial deploy, but operational burden shifts to template authors and lacks the "single pane of glass" status the design requires. Rejected for lack of dynamic translation + feedback loop.

### 2. Direct copy of a new portable CRD to all remotes + per-cluster "translator operator"
- **Description**: Install the GFP CRD (and a small translator Deployment) on *every* remote cluster. Central controller (or propagation tool like Karmada) copies GFP objects to remotes; local translator turns GFP into native policy on each spoke.
- **Pros**: Spokes are self-contained; works offline; leverages existing multi-cluster propagation.
- **Cons**: Requires CRD + RBAC + Deployment (even if tiny) on every remote (violates "CRDs only on hub" and "no spoke components"). Increases attack surface and upgrade surface on remotes. For vanilla clusters without operator framework, extra burden. Central still needs to know which clusters exist.
- **Trade-off**: Better isolation, but directly contradicts the "pure hub-to-spoke with only kubeconfig access" and "installation simplicity" requirements. Rejected; the hub controller + direct writes is lower footprint on remotes.

### 3. Use AdminNetworkPolicy everywhere + custom CNI plugin (or wait for universal support)
- **Description**: Adopt policy.networking.k8s.io AdminNetworkPolicy as the central (and remote) form. Write a controller that emits ANP on all remotes (vanilla or Cilium).
- **Pros**: Standard (SIG) API, explicit priority + Allow/Deny/Pass, cluster-scoped.
- **Cons**: Still alpha (v1alpha1 as of 2026), not implemented by all CNIs (Cilium has partial/experimental support via its own CRs or gateway; many vanilla setups use only core NetworkPolicy). Would still require a translator layer for clusters that don't understand ANP. Does not solve the "source IP" 5-tuple portability for pure K8s NP users.
- **Trade-off**: Good long-term direction, but not portable *today* across the described fleet. Our GFP can evolve to emit ANP on capable clusters as an alternative backend in the translator (future work). For now, we own a minimal portable superset focused on the user's 5-tuple + selectors.

Other rejected: embedding full Cilium policy language in GFP (non-portable), using only CIDRs (too static, loses label benefits).

## Security & Privacy Considerations

**Threat model**:
- Attacker with hub write access to GFP/MC can affect policy on all matching remotes (broad blast radius). Mitigate with RBAC, admission webhooks (future: validating webhook for dangerous "allow world" rules), audit logging.
- Compromise of a single remote kubeconfig Secret allows attacker to apply policies only on that cluster (limited by the narrow remote RBAC).
- Drift overwrite: intentional (design); prevents "shadow admin" policies on remotes.

**AuthN/Z**:
- Hub: standard K8s RBAC for platform users (create GFP/MC in `firewall-system` or cluster-scoped).
- Remote access: each cluster's kubeconfig uses a dedicated ServiceAccount bound to a ClusterRole (or Role per target ns) with **only**:
  - `get,list,watch,create,update,patch,delete` on `networking.k8s.io/networkpolicies`
  - same for `cilium.io/ciliumnetworkpolicies` and `ciliumclusterwidenetworkpolicies`
  - `get,list,watch` on `namespaces` (for vanilla ns discovery) and perhaps `nodes` for health.
- No pod/exec/logs/secrets access.
- Secrets containing kubeconfigs: store in dedicated ns, encrypt at rest (KMS), restrict via RBAC + external-secrets or sealed-secrets. Rotate tokens periodically.
- Controller SA on hub: broad on hub CRDs + secrets (read), but no cross-cluster unless via the provider.

**Data handling**: No PII in policies (labels + CIDRs + ports only). Status may contain error messages from remotes (sanitize before storing?).

**Principle of least privilege**: Explicitly called out in docs + Helm values comments. Provide example `remote-rbac.yaml` per cluster type (included verbatim below; also generated by `hack/generate-remote-rbac.sh` which takes target ns list + policyType and emits Role/ClusterRole+Bindings for a given cluster SA).

**Example remote RBAC (cilium target, cluster-scoped for CCNP + namespaced for CNP; per-ns Role variant in comments):**

```yaml
# remote-rbac-cilium.yaml (apply with the remote SA bound to this)
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: gfp-remote-cilium
rules:
- apiGroups: ["cilium.io"]
  resources: ["ciliumnetworkpolicies", "ciliumclusterwidenetworkpolicies"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get", "list", "watch"]  # for discovery if needed
---
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRoleBinding
metadata:
  name: gfp-remote-cilium
roleRef:
  apiGroup: rbac.authorization.k8s.io
  kind: ClusterRole
  name: gfp-remote-cilium
subjects:
- kind: ServiceAccount
  name: gfp-remote
  namespace: firewall-system  # or the ns where SA lives on remote
# For per-ns (vanilla or restricted cilium): use Role + RoleBinding per target ns instead of ClusterRole for the namespaced CNP.
```

**Example for kubernetes (vanilla) target (namespaced NPs only; ClusterRole optional if using per-ns Roles):**

```yaml
apiVersion: rbac.authorization.k8s.io/v1
kind: ClusterRole
metadata:
  name: gfp-remote-k8s
rules:
- apiGroups: ["networking.k8s.io"]
  resources: ["networkpolicies"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["namespaces"]
  verbs: ["get", "list", "watch"]
---
# Binding same as above.
# Per-ns variant: Role in each target ns with same rules on networkpolicies, bound to SA.
```

**Hub controller SA RBAC skeleton** (for `config/rbac/role.yaml` or generated; PR2/7; at minimum):

```yaml
rules:
- apiGroups: ["firewall.networking.example.com"]
  resources: ["globalfirewallpolicies", "globalfirewallpolicies/status", "managedclusters", "managedclusters/status"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]
- apiGroups: [""]
  resources: ["secrets"]
  verbs: ["get", "list", "watch"]  # in firewall-system, for provider + MC ctrl (label selector in code)
- apiGroups: ["coordination.k8s.io"]
  resources: ["leases"]
  verbs: ["get", "list", "watch", "create", "update", "patch", "delete"]  # leader election
```

**Hub Deployment auth**: Standard in-cluster (no extra --authentication-token-webhook needed for controller; uses ServiceAccount token). Secrets use projected tokens where possible for remotes (note: kubeconfig provider supports static tokens; for rotation use external secret operator + short-lived). Token projection/rotation: recommend using `TokenRequest` API or external-secrets with rotation for the remote SAs' kubeconfigs.

(The generate script in PR7 outputs exactly these YAMLs given `TARGET_NS="ns1,ns2" POLICY_TYPE=cilium CLUSTER_SA=gfp-remote` etc.)

**Risks** (with severity):
- **High**: Overly broad remote RBAC → privilege escalation on remote. *Mitigation*: documented minimal manifests; CI check in chart; optional per-ns Roles instead of ClusterRole.
- **Medium**: Secret leakage → one cluster compromise. *Mitigation*: separate secrets, short-lived tokens where possible, audit.
- **Medium**: Controller bug emits bad policy (e.g. deny-all) → outage. *Mitigation*: dry-run flag, canary clusters via selectors, validating admission on GFP (future), status visibility.
- **Low**: Status contains sensitive remote error details. *Mitigation*: strip known sensitive strings.

## Observability

**Metrics** (controller-runtime + custom, exposed on :8080/metrics):
- `globalfirewallpolicy_reconcile_total` / `errors`
- `per_cluster_sync_duration_seconds` (histogram, labels: cluster, policy_type)
- `remote_policies_applied_total` (labels: cluster, kind, action)
- `managedcluster_ready` gauge
- `active_clusters` gauge
- Workqueue depth, client QPS per cluster (via rest config wrappers)

**Logs**: Structured (logr/zap). Key fields: `controller`, `globalfirewallpolicy`, `cluster`, `remote_policy_kind/name`, `generation`, `error`. Example: `"applied remote policy" cluster="eu-prod-01" kind="CiliumClusterwideNetworkPolicy" ...`

**Events**: 
- On hub GFP: Normal "PolicyPropagated" / Warning "ClusterApplyFailed".
- On ManagedCluster: connectivity events.
- (Optional) Attempt to create Events on remote objects (limited value cross-cluster).

**Alerting examples** (Prometheus + Alertmanager):
- `increase(per_cluster_sync_duration_seconds_sum[5m]) / ... > 30` → slow sync on cluster.
- `remote_policies_applied_total{action="error"} > 0`
- Any GFP with `perClusterStatus[*].conditions[Applied=False]` for > 10m.
- Number of active clusters drops.

**Tracing**: Optional OpenTelemetry spans around per-cluster fan-out (future, behind flag).

**Dashboards**: Grafana panel showing "Policy coverage by cluster" (from status), error heat map.

## Rollout Plan

1. **Pre-flight**: Document remote RBAC requirements. Provide `hack/generate-remote-rbac.sh` that, given a list of target ns, emits the ClusterRole/Role+Binding YAML for a given cluster name (for both policyType values).
2. **Installation (v0.1)**: Helm install on hub only (`helm install firewall-controller ./charts/firewall-controller --namespace firewall-system --create-namespace`). CRDs applied. Controller starts with leader election. No remotes yet.
3. **Onboarding clusters**:
   - Create Secret + ManagedCluster for a canary cluster.
   - MC controller marks Ready.
   - Create first GFP with tight `clusterSelector` matching only canary.
   - Observe status, events, remote objects appear.
4. **Staged fleet rollout**:
   - Phase 1: 5 canary clusters (mix of cilium + vanilla).
   - Phase 2: 20 "prod-like" via broader selectors.
   - Phase 3: Full fleet (use progressive cluster label changes or multiple GFPs).
   - Use feature gate / env var `ENABLE_VANILLA_TRANSLATION=false` initially if risk.
5. **Feature flags / config** (in Helm values + ConfigMap/env):
   - `controller.maxConcurrentReconciles`
   - `perClusterQPS`, `perClusterBurst`
   - `driftCorrectionEnabled: true`
   - `dryRun: false` (future: server-side dry-run before apply)
6. **Rollback**:
   - Helm rollback to previous chart version (controller image).
   - Or scale controller to 0 replicas (policies stay in last-applied state on remotes; no automatic revert).
   - Emergency: delete offending GFP (triggers cleanup finalizer).
   - Per-cluster: edit ManagedCluster to set a "paused" label/annotation; controller skips.
7. **Upgrades**: CRD changes require careful conversion webhooks later; for alpha use "delete + recreate" on dev fleets. Status is best-effort.
8. **Decommission**: Remove labels from Secrets or delete MCs → provider disengages; delete GFPs → finalizers clean remotes.

**Risk mitigation during rollout**: Start with read-only "report only" mode (future flag) that populates status but does not write remotes.

## Open Questions

**Note (post-revision)**: The following list is retained for historical context from the initial design. All 7 items now have explicit **proposed resolutions / default behaviors** documented in the "Lifecycle Management" subsection (which states "the following default behaviors apply (proposed answers to OQs; not left as open questions)" and covers CNI transitions, MC deletion, clusterSelector changes, status pruning + direct OQ1–7 answers). See also cross-references in Reconciliation pseudocode, Deletion and Finalizer, and Implementation Details. No unresolved decisions remain; these are proposed as part of the design spec (future refinements via PRs or user input as noted).

1. Should `ManagedCluster` be the source of truth for kubeconfig (i.e. embed base64 in MC spec, with controller writing the labeled Secret), or keep secrets as the contract with the provider? (Trade-off: one CR vs secret hygiene.) **(Proposed resolution: see Lifecycle Management > OQ1 (secret vs MC embed))** 
2. Exact Cilium label prefix for namespace in endpointSelector (`k8s:io.kubernetes.pod.namespace` vs `io.kubernetes.pod.namespace`)? Need to pin to supported Cilium versions (1.14+ ?). **(Proposed resolution: see Lifecycle Management > OQ2 (Cilium ns label))**
3. Support for `AdminNetworkPolicy` as an *additional* emission target on clusters that have the CRD? (Would give priority ordering.) **(Proposed resolution: see Lifecycle Management > OQ3 (ANP backend); future in PR8)**
4. Should we support source ports in the model (and how to translate where possible)? **(Proposed resolution: see Lifecycle Management > OQ4 (src ports); omitted in v1)**
5. Retention policy for historical perClusterStatus entries (prune old clusters)? **(Proposed resolution: see Lifecycle Management > OQ5 (status pruning) + status pruning bullet)**
6. How to handle clusters that transition from "kubernetes" to "cilium" (or vice-versa)? Reconcile will switch the emitted objects; old ones should be cleaned. **(Proposed resolution: see Lifecycle Management > CNI type transitions (OQ6) + MC deletion + clusterSelector shrink bullets)**
7. Validation webhook for dangerous rules (e.g. allow from 0.0.0.0/0 on privileged ports)? Scope for v1 or v1.1? **(Proposed resolution: see Lifecycle Management > OQ7 (validating webhook); v1.1+)**

(The Lifecycle Management subsection also addresses related lifecycle behaviors for MC delete, selector shrink, and pruning that affect several of the above.)

## Key Decisions (with Rationale)

- **CRD names & group**: `GlobalFirewallPolicy` + `ManagedCluster` under `firewall.networking.example.com/v1alpha1`. Rationale: "Global" signals cluster-spanning intent (like Calico GlobalNetworkPolicy); avoids clashing with core `NetworkPolicy` or Cilium names; "Firewall" matches user language of 5-tuple firewall rules. Separate MC kind keeps concerns clean.
- **Cluster-scoped for both CRs**: Platform/SRE use case; no per-team namespacing needed initially. RBAC can still be namespaced via aggregated roles if multi-team later.
- **L3/L4 portable core + optional Cilium hints**: Keeps the abstraction useful for vanilla targets while allowing power users full fidelity on Cilium without forking the model. Explicitly documents "Deny unsupported on kubernetes".
- **multicluster-runtime + kubeconfig provider**: Matches existing pattern in the ecosystem (KubeCon talks, sigs repo). Secrets are the lowest-common-denominator for arbitrary clusters (no dependency on Cluster API, Karmada, etc.). MC CR adds the needed selector + metadata layer.
- **Authoritative overwrite (no merge)**: Simplest semantics for "single source of truth". Matches GitOps expectations. Drift detection via watches is cheap once clusters are engaged.
- **One remote policy object per GFP (not one giant merged policy)**: Traceability (label back to source GFP + generation), easy cleanup on delete, independent reconciliation. CNI handles combination.
- **No spoke components / CRDs on remotes**: Directly satisfies "installation & packaging" and "least privilege" requirements. Pure API writes from hub.
- **Subject + per-rule peers model** (inspired by AdminNetworkPolicy + Calico): Natural for both "protect these pods" and "from these sources". Enables the ns-discovery trick for vanilla NP.
- **Status as array of per-cluster structs (not sub-resources or separate CR)**: Simple, queryable via `kubectl get gfp -o yaml`, sufficient for hundreds of clusters. Future: consider sharded status if it grows too large.
- **Reject pure template or per-spoke translator approaches**: They fail the concrete requirements around dynamic per-cluster translation, central status, and minimal remote footprint.

These decisions were made after reviewing Cilium docs, network-policy-api, Calico GlobalNetworkPolicy, multicluster-runtime source, controller-runtime multi-cluster issues, and related projects.

## References

- Kubernetes NetworkPolicy: https://kubernetes.io/docs/concepts/services-and-networking/network-policies/
- Cilium Network Policy (CNP / CCNP): https://docs.cilium.io/en/latest/network/kubernetes/policy/ (and /security/policy/...)
- AdminNetworkPolicy API: https://network-policy-api.sigs.k8s.io/reference/spec/ and https://github.com/kubernetes-sigs/network-policy-api
- Calico GlobalNetworkPolicy: https://docs.tigera.io/calico/latest/reference/resources/globalnetworkpolicy
- multicluster-runtime (kubeconfig provider): https://github.com/kubernetes-sigs/multicluster-runtime (and providers/kubeconfig)
- Cilium ClusterMesh: https://cilium.io/use-cases/cluster-mesh/
- Submariner: https://submariner.io/ (connectivity, not policy translation)
- Karmada + Submariner examples for multi-cluster propagation patterns.
- Prior multi-cluster controller patterns: KubeCon EU 2025 "Dynamic Multi-Cluster Controllers with controller-runtime"; sigs.k8s.io/controller-runtime issues on multi-cluster.
- AWS VPC CNI Network Policy controller (example of per-cluster policy reconcilers).

## PR Plan

Ordered list of independently mergeable PRs (each adds value, can be reviewed/shipped separately; later PRs build on earlier infrastructure). **Each PR must produce code that compiles, has passing unit tests (even with later features stubbed), and delivers standalone value** (e.g. "PR4 can propagate to cilium + basic k8s end-to-end"). PR1 adds the dep but usage + tests start in PR2 (skeleton e2e in PR2 uses fake provider). No separate PR0; scaffolding + basic wiring in 1+2. Assume standard Go module, `make generate`, controller-gen, etc.

**PR7 note**: Bundles a lot; consider splitting RBAC/helm skeleton into PR2 or PR4 if review prefers (skeleton manifests + example remote-rbac.yaml can land early without full docs).

1. **PR: Scaffold project + CRDs + basic types**  
   - Files: `go.mod`, `Makefile`, `api/firewall/v1alpha1/globalfirewallpolicy_types.go`, `managedcluster_types.go`, `config/crd/...` (or use controller-gen markers), `charts/firewall-controller/crds/*.yaml`.  
   - Deps: controller-runtime, controller-tools, multicluster-runtime (add as dep; no usage yet).  
   - Desc: Define the two cluster-scoped types (full with markers, consts, missing structs like RemotePolicyRef/CiliumPolicyHints per Issue 2), generate deepcopy + CRDs. No controllers yet. Include example YAMLs in `config/samples/`. CI: `make manifests generate` passes. (Compiles as library.)

2. **PR: Add multicluster-runtime integration + ManagedCluster controller (skeleton)**  
   - Files: `cmd/manager/main.go` (setup mcmanager + kubeconfig provider + local manager; leader election), `controllers/managedcluster_controller.go` (basic watch on MC + Secrets, mark Ready if secret exists + can list namespaces via engaged client; full Reconcile steps per Issue 7), `pkg/multicluster/client.go`, basic RBAC for hub + skeleton remote-rbac.yaml example.  
   - Deps: multicluster-runtime + its kubeconfig provider (now used).  
   - Desc: **Value delivered**: Bootstraps the multi-cluster manager (engages clusters from labeled secrets). MC controller does connectivity smoke test + Ready status. Secrets pre-labeled or labeled by MC. E2E skeleton with kind + fake remote (or envtest) exercising MC creation/Ready. Leader election enabled. Provides foundation for GFP (even without GFP controller yet). Tests pass independently.

3. **PR: Implement pkg/translator (core logic, unit tests)**  
   - Files: `pkg/translator/*.go` (translator interface + full NetworkPolicy + Cilium translators + testdata/ with before/after YAML pairs; util.go with name hash), `pkg/translator/util.go`.  
   - Deps: none new (use k8s.io/api, cilium types vendored or imported as needed; for Cilium CRDs we can use unstructured or add cilium API as optional dep).  
   - Desc: **Value delivered**: Pure functions + interface (per Issue 7). Exhaustive table tests for subject translation, peer types, port mapping, action handling (Deny skipped for k8s), name generation (hash spec), CCNP vs CNP decision, basic vs full k8s split. No cluster I/O. Documents limitations. Usable by later controller PRs.

4. **PR: GlobalFirewallPolicy controller – hub reconciliation + status**  
   - Files: `controllers/globalfirewallpolicy_controller.go` (core Reconcile: list MCs (with Ready filter), fan-out via GetCluster, call translator, apply logic using client on remote, update GFP status, finalizer add + basic delete path), wiring in main.go, events, metrics.  
   - Deps: previous (1-3).  
   - Desc: **Value delivered (standalone)**: End-to-end GFP -> propagation for *cilium* targets (full CCNP/CNP) + *basic kubernetes* (cilium full + k8s single-ns/hardcoded or minimal from MC; assumes subject selects one ns for PR4). Full fan-out with error collection/partial syncs (never aborts other clusters), perClusterStatus updates, finalizer scaffolding (add/remove; full multi-MC cleanup logic from "Deletion and Finalizer" subsection). Apply is idempotent from day 1 (CreateOrUpdate/SSA). No remote watches yet. Unit + fake-client tests; compiles/runs independently (stubs full cross-ns k8s if needed). Delivers "controller can manage policies on mixed fleets at basic level".

5. **PR: Drift detection + remote watches via multicluster sources**  
   - Files: Extend GFP controller to setup per-cluster informers for the target policy GVKs (using mc builder or manual), handler that extracts policy-name label and enqueues GFP, enhance delete path if needed. (Apply idempotency already in PR4; this PR does *not* touch basic apply.)  
   - Deps: previous PRs (4+).  
   - Desc: **Value delivered**: Adds remote watches + drift correction (owned object change -> overwrite). Integration test mutates remote and asserts correction. Pure addition on top of working PR4 apply.

6. **PR: Vanilla K8s full namespace discovery + targetNamespaceSelector + enhancements**  
   - Files: Enhancements in translator + controller/reconcile for full target ns listing (List Namespaces on remote when policyType=kubernetes), filtering via MC `targetNamespaceSelector`, multi-ns apply handling, update docs/pseudocode for "full" vanilla.  
   - Deps: 4+5.  
   - Desc: **Value delivered**: Makes vanilla targets *fully* functional for cross-ns subjects and MC-driven ns filters (PR4 had basic single-ns k8s support). Tests with multiple ns + selector on fake cluster. Updates reconcile/translation notes to remove "basic" caveats.

7. **PR: Observability, RBAC, Helm packaging, docs**  
   - Files: `pkg/metrics/`, Prometheus ServiceMonitor in chart, full RBAC generation (hub + example remote-rbac.yaml; move skeleton to earlier if split), `charts/firewall-controller/` complete (deployment, serviceaccount, leader election role, values.yaml with qps knobs, report-only/dry-run flag), `README.md` (quickstart, architecture diagram, security section), example remote onboarding scripts + hack/generate-remote-rbac.sh (output format described in Issue 8).  
   - Deps: all prior.  
   - Desc: **Value delivered**: Production-ready packaging + first-class report-only/dry-run (metrics for would-apply). Includes `hack/` scripts. CI: helm template + kubeconform + e2e. Completes rollout story. (Can be split: e.g. RBAC/helm skeleton earlier.)

8. **PR (optional, post-v1): Validating webhook + priority field + L7 extension stub + AdminNetworkPolicy backend**  
   - Files: `api/...` additions, `webhooks/`, translator extensions, docs update.  
   - Deps: 7.  
   - Desc: Hardens the API and adds the next-tier features discussed in Open Questions.

Each PR should include:
- Unit + integration tests (envtest + fake remote clusters via multicluster testing helpers); each PR's code must build and test green without later PRs (stubs as needed, e.g. for PR4 k8s ns list).
- Updated samples + e2e script (kind cluster + cilium kind cluster + vanilla kind cluster); progressive (PR2 has MC e2e skeleton; PR4 adds GFP->cilium propagation).
- Makefile targets exercised in CI.
- PR description linking back to this design doc sections + explicit "value delivered" (e.g. "PR4 delivers end-to-end GFP->cilium + basic k8s propagation + status + finalizer scaffolding").

---

*End of design document. This is a complete, concrete starting point for implementation on the greenfield workspace.*
