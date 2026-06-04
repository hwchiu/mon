package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// GroundRuleSpec defines a baseline ground rule between two zones.
// Ground rules form the immutable matrix and cannot be overridden to DENY by zone rules.
type GroundRuleSpec struct {
	FromZone string `json:"fromZone"`
	ToZone   string `json:"toZone"`
	// Ports can be a single port, range (e.g. "1024-65535"), or "all"
	Ports    string `json:"ports"`
	Protocol string `json:"protocol"` // tcp, udp, all
	Action   string `json:"action"`   // allow or deny (baseline)
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

type GroundRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec GroundRuleSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

type GroundRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []GroundRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&GroundRule{}, &GroundRuleList{})
}
