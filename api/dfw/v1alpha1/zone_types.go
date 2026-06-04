package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

// ZoneSpec defines the desired state of Zone
type ZoneSpec struct {
	// ID is the stable zone identifier (e.g. "zone-001")
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	ID string `json:"id"`

	// Name is a human friendly name (e.g. "DMZ")
	Name string `json:"name"`

	// CIDRs are the non-overlapping CIDR blocks belonging to this zone.
	// Every IP that should be treated as part of this zone must be covered.
	// +kubebuilder:validation:MinItems=1
	CIDRs []string `json:"cidrs"`
}

// ZoneStatus defines the observed state of Zone
type ZoneStatus struct {
	// ObservedGeneration is the last generation the controller observed.
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:resource:scope=Cluster
// +kubebuilder:printcolumn:name="ID",type=string,JSONPath=`.spec.id`
// +kubebuilder:printcolumn:name="Name",type=string,JSONPath=`.spec.name`

// Zone is the Schema for the zones API
type Zone struct {
	metav1.TypeMeta   `json:",inline"`
	metav1.ObjectMeta `json:"metadata,omitempty"`

	Spec   ZoneSpec   `json:"spec,omitempty"`
	Status ZoneStatus `json:"status,omitempty"`
}

// +kubebuilder:object:root=true

// ZoneList contains a list of Zone
type ZoneList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitempty"`
	Items           []Zone `json:"items"`
}

func init() {
	SchemeBuilder.Register(&Zone{}, &ZoneList{})
}
