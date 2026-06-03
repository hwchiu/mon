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
	// KubeconfigSecretRef points to a Secret in the hub cluster containing a kubeconfig
	// for the remote cluster. The Secret must be labeled for the multicluster-runtime
	// kubeconfig provider.
	KubeconfigSecretRef SecretRef `json:"kubeconfigSecretRef"`

	// PolicyType tells the controller what kind of policy objects to emit on this cluster.
	// Valid values: "cilium", "kubernetes".
	PolicyType string `json:"policyType"`

	// TargetNamespaceSelector can be used to limit which namespaces on the remote
	// receive per-namespace NetworkPolicy objects (vanilla k8s path).
	TargetNamespaceSelector *metav1.LabelSelector `json:"targetNamespaceSelector,omitempty"`
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
