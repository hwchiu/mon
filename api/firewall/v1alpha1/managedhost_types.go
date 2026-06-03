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
// +kubebuilder:resource:scope=Cluster,shortName=mh
// +kubebuilder:subresource:status

type ManagedHost struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ManagedHostSpec   `json:"spec,omitempty"`
	Status ManagedHostStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true
type ManagedHostList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ManagedHost `json:"items"`
}

type ManagedHostSpec struct {
	// Addresses are the known IP addresses of the host (used for rule materialization).
	Addresses []string `json:"addresses,omitempty"`

	// ReportedLabels are labels the agent reported at registration time (or from Node projection).
	ReportedLabels map[string]string `json:"reportedLabels,omitempty"`

	// ExternalID is a stable cloud/VM identifier (e.g. instance ID) for bare-metal/VM agents.
	ExternalID string `json:"externalID,omitempty"`
}

type ManagedHostStatus struct {
	Conditions []metav1.Condition `json:"conditions,omitempty"`

	LastRegistered *metav1.Time `json:"lastRegistered,omitempty"`
	AgentVersion   string       `json:"agentVersion,omitempty"`
	KernelVersion  string       `json:"kernelVersion,omitempty"`

	// bpfFeatures lists capabilities discovered by the agent (xdp-native, lpm-trie, ringbuf, etc.).
	BpfFeatures []string `json:"bpfFeatures,omitempty"`

	CurrentPolicyVersion string       `json:"currentPolicyVersion,omitempty"`
	LastPolicySync       *metav1.Time `json:"lastPolicySync,omitempty"`
}
