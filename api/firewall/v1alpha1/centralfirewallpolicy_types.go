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
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// +genclient
// +genclient:nonNamespaced
// +k8s:deepcopy-gen:interfaces=k8s.io/apimachinery/pkg/runtime.Object
// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster,shortName=cfp
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Hosts",type="integer",JSONPath=".status.perHostStatus.length"
// +kubebuilder:printcolumn:name="Age",type="date",JSONPath=".metadata.creationTimestamp"

type CentralizedFirewallPolicy struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   CentralizedFirewallPolicySpec   `json:"spec,omitempty"`
	Status CentralizedFirewallPolicyStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type CentralizedFirewallPolicyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []CentralizedFirewallPolicy `json:"items"`
}

type CentralizedFirewallPolicySpec struct {
	// HostSelector selects ManagedHost objects this policy applies to.
	HostSelector metav1.LabelSelector `json:"hostSelector,omitempty"`

	// ClusterSelector is an additional filter (can be used together with or instead of HostSelector).
	ClusterSelector metav1.LabelSelector `json:"clusterSelector,omitempty"`

	// DefaultAction is the catch-all when no rule matches. "Deny" is strongly recommended.
	DefaultAction string `json:"defaultAction,omitempty"` // "Allow" | "Deny"

	Ingress []FirewallRule `json:"ingress,omitempty"`
	Egress  []FirewallRule `json:"egress,omitempty"`

	// Hints for other backends (Cilium etc.). Pure eBPF compiler skips these.
	Cilium *CiliumPolicyHints `json:"cilium,omitempty"`
}

// Note: FirewallRule, Peer, Port, CiliumPolicyHints are defined in globalfirewallpolicy_types.go
// and are intentionally shared (same portable model with small eBPF extensions like Priority/Except/EndPort).

type CentralizedFirewallPolicyStatus struct {
	ObservedGeneration int64              `json:"observedGeneration,omitempty"`
	PerHostStatus      []PerHostStatus    `json:"perHostStatus,omitempty"`
	Conditions         []metav1.Condition `json:"conditions,omitempty"`
}

type PerHostStatus struct {
	HostName             string             `json:"hostName"`
	ObservedGeneration   int64              `json:"observedGeneration,omitempty"`
	AppliedPolicyVersion string             `json:"appliedPolicyVersion,omitempty"`
	Conditions           []metav1.Condition `json:"conditions,omitempty"`
	LastSyncTime         *metav1.Time       `json:"lastSyncTime,omitempty"`
	KernelVersion        string             `json:"kernelVersion,omitempty"`
	MapUtilization       string             `json:"mapUtilization,omitempty"` // e.g. "1234/65536"
	ErrorCount           int                `json:"errorCount,omitempty"`
}
