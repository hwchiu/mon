package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ZoneRuleSpec is a selective override (only DENY -> ALLOW allowed).
type ZoneRuleSpec struct {
	SrcZone  string `json:"srcZone"`
	DstZone  string `json:"dstZone"`
	Ports    string `json:"ports"`
	Protocol string `json:"protocol"`
	// Direction: "egress" (defined by source zone owner) or "ingress" (defined by dest zone owner)
	Direction string `json:"direction"`
	Action    string `json:"action"` // must be "allow"
}

// +kubebuilder:object:root=true
// +kubebuilder:resource:scope=Cluster

type ZoneRule struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec ZoneRuleSpec `json:"spec,omitempty"`
}

// +kubebuilder:object:root=true

type ZoneRuleList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []ZoneRule `json:"items"`
}

func init() {
	SchemeBuilder.Register(&ZoneRule{}, &ZoneRuleList{})
}
