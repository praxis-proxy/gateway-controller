package v1alpha1

// LocalSecretReference references a Secret in the same namespace as the
// referring resource.
type LocalSecretReference struct {
	// name is the name of the Secret.
	// +required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=253
	Name string `json:"name"`
}
