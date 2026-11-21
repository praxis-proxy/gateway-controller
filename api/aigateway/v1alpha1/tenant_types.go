package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// IdentityType is an authentication type a Tenant may authorize.
// +kubebuilder:validation:Enum=OIDC;APIKey
type IdentityType string

const (
	// IdentityTypeOIDC authorizes OIDC authentication.
	IdentityTypeOIDC IdentityType = "OIDC"

	// IdentityTypeAPIKey authorizes API key authentication.
	IdentityTypeAPIKey IdentityType = "APIKey"
)

// AuthorizedIdentity is an authentication type the Tenant permits callers to
// use against its gateways.
type AuthorizedIdentity struct {
	// type is the authentication type this tenant authorizes.
	// +required
	Type IdentityType `json:"type"`
}

// AuthorizedCredential authorizes a Secret for upstream authentication.
type AuthorizedCredential struct {
	// secretRef names a Secret in the Tenant's namespace.
	// +required
	SecretRef LocalObjectReference `json:"secretRef"`
}

// TenantSpec defines the authorization grants for a tenant. Both lists are
// optional; a Tenant granting nothing is a valid ownership anchor whose
// grants are added as the tenant grows.
type TenantSpec struct {
	// authorizedIdentities lists the authentication types callers may use to
	// authenticate to this tenant's gateways. An AIGateway may configure any
	// subset of these types.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=16
	AuthorizedIdentities []AuthorizedIdentity `json:"authorizedIdentities,omitempty"`

	// authorizedCredentials lists the Secrets this tenant's ModelProviders may
	// use to authenticate to upstream model services. Entries must reference
	// distinct Secrets.
	// +optional
	// +kubebuilder:validation:MaxItems=64
	// +kubebuilder:validation:XValidation:rule="self.all(c, self.exists_one(x, x.secretRef.name == c.secretRef.name))",message="authorizedCredentials entries must reference distinct Secrets"
	AuthorizedCredentials []AuthorizedCredential `json:"authorizedCredentials,omitempty"`
}

// TenantStatus reports the observed state of a Tenant.
type TenantStatus struct {
	// conditions describe the current state of the Tenant.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// Tenant is the ownership and isolation anchor for a set of gateways and the
// models published through them. In v1alpha1 a Tenant's isolation boundary is
// its own namespace, and a namespace belongs to a single Tenant.
//
// If more than one Tenant exists in a namespace, the one with the earliest
// metadata.creationTimestamp owns it; ties are broken by metadata.name in
// ascending order. The rest report status.conditions[Ready]=False with reason
// Conflict.
type Tenant struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +optional
	Spec TenantSpec `json:"spec,omitzero"`

	// +optional
	Status TenantStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// TenantList contains a list of Tenant.
type TenantList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Tenant `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Tenant{}, &TenantList{})
		return nil
	})
}
