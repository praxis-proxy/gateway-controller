package v1alpha1

// LocalObjectReference references an object in the same namespace as the
// referring resource.
type LocalObjectReference struct {
	// name is the name of the object.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[a-z0-9]([-a-z0-9]*[a-z0-9])?(\.[a-z0-9]([-a-z0-9]*[a-z0-9])?)*$`
	Name string `json:"name"`
}

// LocalSecretKeyReference selects one entry within a Secret in the same
// namespace as the referring resource.
type LocalSecretKeyReference struct {
	LocalObjectReference `json:",inline"`

	// key is the entry within the Secret holding the value.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	// +kubebuilder:validation:Pattern=`^[-._a-zA-Z0-9]+$`
	Key string `json:"key"`
}
