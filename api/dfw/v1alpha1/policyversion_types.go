package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// PolicyVersionSpec defines the desired state (usually empty, as this is mostly status-driven output of compilation)
type PolicyVersionSpec struct {
	// Description for the version (optional, for humans)
	Description string `json:"description,omitempty"`
}

// PolicyVersionStatus contains the compiled map data and metadata.
type PolicyVersionStatus struct {
	Version        string `json:"version"`
	CreatedAt      metav1.Time `json:"createdAt,omitempty"`
	GroundHash     string `json:"groundHash,omitempty"`
	ZoneRulesHash  string `json:"zoneRulesHash,omitempty"`
	// MapData is the serialized data for the agent's eBPF maps (zone_cidr, verdict, etc.)
	// In production this could be stored in a ConfigMap or external, but for simplicity we put a reference or small blob here.
	MapDataRef string `json:"mapDataRef,omitempty"` // e.g. name of a secret or configmap containing the actual binary data
	// Or for small sizes, we could embed, but better reference for large policies.
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster

type PolicyVersion struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   PolicyVersionSpec   `json:"spec,omitempty"`
	Status PolicyVersionStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

type PolicyVersionList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []PolicyVersion `json:"items"`
}

func init() {
	SchemeBuilder.Register(&PolicyVersion{}, &PolicyVersionList{})
}
