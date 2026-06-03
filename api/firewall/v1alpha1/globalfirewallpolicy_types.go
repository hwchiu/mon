/*
Copyright 2026 The mon authors.

Licensed under the Apache License, Version 2.0 (the "License");
you may not use this file except in compliance with the License.
You may obtain a copy of the License at

    http://www.apache.org/licenses/LICENSE-2.0

Unless required by applicable law or agreed to in writing, software
distributed under the License is distributed on an "AS IS" BASIS,
WITHOUT WARRANTIES OR CONDITIONS OF ANY KIND, either express or implied.
See the License for the specific language governing permissions and
limitations under the License.
*/

package v1alpha1

import (
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/util/intstr"
)

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
	// ClusterSelector selects which ManagedClusters (and by extension hosts in those clusters)
	// this policy applies to. Empty selector means all clusters.
	ClusterSelector metav1.LabelSelector `json:"clusterSelector,omitempty"`

	// Subject defines the endpoints (pods) that this policy protects / applies to.
	Subject EndpointSubject `json:"subject"`

	Ingress []FirewallRule `json:"ingress,omitempty"`
	Egress  []FirewallRule `json:"egress,omitempty"`

	// Cilium-specific knobs (best-effort / ignored on non-Cilium targets and by pure eBPF backend).
	Cilium *CiliumPolicyHints `json:"cilium,omitempty"`
}

// EndpointSubject selects the "to" / protected workloads (pods in K8s).
type EndpointSubject struct {
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
	PodSelector       *metav1.LabelSelector `json:"podSelector,omitempty"`
}

// FirewallRule is the portable 5-tuple style rule used by both designs.
// Action is "Allow" or "Deny".
type FirewallRule struct {
	Name   string `json:"name,omitempty"`
	Action string `json:"action"` // "Allow" | "Deny"

	// For ingress rules: sources.
	From []Peer `json:"from,omitempty"`
	// For egress rules: destinations.
	To []Peer `json:"to,omitempty"`

	Ports []Port `json:"ports,omitempty"`
}

type Peer struct {
	IPBlocks          []string              `json:"ipBlocks,omitempty"`
	NamespaceSelector *metav1.LabelSelector `json:"namespaceSelector,omitempty"`
	PodSelector       *metav1.LabelSelector `json:"podSelector,omitempty"`
}

type Port struct {
	Protocol corev1.Protocol    `json:"protocol,omitempty"`
	Port     intstr.IntOrString `json:"port,omitempty"`
	// EndPort for ranges (supported in eBPF design more readily).
	EndPort *int32 `json:"endPort,omitempty"`
}

// CiliumPolicyHints are optional and ignored by the pure eBPF backend.
type CiliumPolicyHints struct {
	EnableDefaultDeny *struct {
		Ingress *bool `json:"ingress,omitempty"`
		Egress  *bool `json:"egress,omitempty"`
	} `json:"enableDefaultDeny,omitempty"`
	// Future L7 etc. fields can be added; both backends document their handling.
}

// RemotePolicyRef points to the concrete policy object created on a remote cluster.
type RemotePolicyRef struct {
	Kind      string `json:"kind"`
	Name      string `json:"name"`
	Namespace string `json:"namespace,omitempty"` // empty for cluster-scoped (e.g. CCNP)
}

type PerClusterStatus struct {
	ClusterName        string             `json:"clusterName"`
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
	RemotePolicies     []RemotePolicyRef  `json:"remotePolicies,omitempty"`
	LastSyncTime       *metav1.Time       `json:"lastSyncTime,omitempty"`
	ErrorCount         int                `json:"errorCount,omitempty"`
}

type GlobalFirewallPolicyStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	PerClusterStatus   []PerClusterStatus `json:"perClusterStatus,omitempty"`
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// Finalizer and label consts are in groupversion_info.go for sharing.
