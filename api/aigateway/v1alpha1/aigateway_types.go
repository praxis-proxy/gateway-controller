package v1alpha1

import (
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
)

// EndpointSpec configures the shared HTTP(S) listener for the gateway.
type EndpointSpec struct {
	// port is the listener port. Required; must be 1–65535.
	// +required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// tls is optional TLS configuration for the listener.
	// +optional
	TLS *EndpointTLSSpec `json:"tls,omitzero"`
}

// EndpointTLSSpec configures TLS for the endpoint.
type EndpointTLSSpec struct {
	// certificateRef names a Secret containing the TLS certificate in the gateway's namespace.
	// +required
	CertificateRef LocalObjectReference `json:"certificateRef"`
}

// ChatCompletionsSpec selects the OpenAI Chat Completions API. The API is
// stateless, so an empty stanza is its whole configuration.
type ChatCompletionsSpec struct{}

// ResponsesSpec selects the OpenAI Responses API. Unlike Chat Completions the
// API can carry state between turns, so it also names the dependencies that
// persistence needs.
type ResponsesSpec struct {
	// conversationState configures where responses and conversation items are
	// persisted, so a later turn can restore context. Omit it to select the
	// API without persistence.
	// +optional
	ConversationState *ConversationStateSpec `json:"conversationState,omitzero"`
}

// ConversationStateSpec configures the store backing the Responses API.
type ConversationStateSpec struct {
	// secretRef names a Secret in the gateway's namespace holding the
	// connection details for the store.
	// +required
	SecretRef LocalObjectReference `json:"secretRef"`
}

// InferenceSpec selects inference APIs to be served.
// +kubebuilder:validation:XValidation:rule="has(self.chatCompletions) || has(self.responses)",message="at least one API (chatCompletions or responses) must be selected"
type InferenceSpec struct {
	// chatCompletions selects the OpenAI Chat Completions API.
	// +optional
	ChatCompletions *ChatCompletionsSpec `json:"chatCompletions,omitzero"`

	// responses selects the OpenAI Responses API.
	// +optional
	Responses *ResponsesSpec `json:"responses,omitzero"`
}

// MCPSpec enables MCP capability. An empty struct enables MCP.
type MCPSpec struct{}

// IdentityTypeSpec is authentication configuration for a single identity type.
// Discriminated by type.
type IdentityTypeSpec struct {
	// type is the authentication type. Required; must match a Tenant authorizedIdentities entry.
	// +required
	Type IdentityType `json:"type"`

	// issuerURL is the OIDC issuer URL, used when type is OIDC.
	// v1alpha1 does not validate this field against the type.
	// +optional
	IssuerURL string `json:"issuerURL,omitzero"`

	// audiences is a list of OIDC audiences, used when type is OIDC.
	// When unset, the runtime applies the gateway name.
	// +optional
	// +kubebuilder:validation:MaxItems=16
	Audiences []string `json:"audiences,omitzero"`

	// header is the HTTP header name carrying the API key, used when type is APIKey.
	// v1alpha1 does not validate this field against the type.
	// +optional
	Header string `json:"header,omitzero"`

	// keySource is the source of API keys, used when type is APIKey.
	// +optional
	KeySource *APIKeySource `json:"keySource,omitzero"`

	// refreshSeconds is the cache refresh interval for API keys, used when type is APIKey.
	// +optional
	// +kubebuilder:default=5
	// +kubebuilder:validation:Minimum=1
	RefreshSeconds *int32 `json:"refreshSeconds,omitzero"`

	// maxStalenessSeconds is the maximum staleness before a cached key is considered
	// invalid, used when type is APIKey.
	// +optional
	// +kubebuilder:default=30
	// +kubebuilder:validation:Minimum=1
	MaxStalenessSeconds *int32 `json:"maxStalenessSeconds,omitzero"`
}

// APIKeySource configures where API key lists are sourced.
type APIKeySource struct {
	// secretRef selects the Secret entry holding the API keys.
	// +required
	SecretRef LocalSecretKeyReference `json:"secretRef"`
}

// GovernanceSpec configures gateway-level governance.
type GovernanceSpec struct {
	// authentication is a list of authentication configurations.
	// Each entry's type must match a Tenant authorizedIdentities entry.
	// +optional
	// +listType=map
	// +listMapKey=type
	// +kubebuilder:validation:MaxItems=16
	Authentication []IdentityTypeSpec `json:"authentication,omitzero"`
}

// RuntimeSpec configures the gateway runtime.
type RuntimeSpec struct {
	// replicas is the desired number of gateway replicas. Defaults to 1.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitzero"`
}

// AIGatewaySpec defines the desired state of an AIGateway.
// +kubebuilder:validation:XValidation:rule="has(self.inference) || has(self.mcp)",message="at least one of inference or mcp must be configured"
type AIGatewaySpec struct {
	// tenantRef is a required, immutable reference to the owning Tenant.
	// Must resolve in the AIGateway's namespace.
	// +required
	// +kubebuilder:validation:XValidation:rule="self == oldSelf",message="tenantRef is immutable"
	TenantRef LocalObjectReference `json:"tenantRef"`

	// endpoint is required and configures the shared HTTP(S) listener.
	// +required
	Endpoint EndpointSpec `json:"endpoint"`

	// runtime is optional and configures the gateway runtime (replicas, etc.).
	// +optional
	Runtime *RuntimeSpec `json:"runtime,omitzero"`

	// inference is optional and selects inference APIs to serve.
	// If omitted, inference is disabled.
	// +optional
	Inference *InferenceSpec `json:"inference,omitzero"`

	// mcp is optional and enables the MCP capability.
	// +optional
	MCP *MCPSpec `json:"mcp,omitzero"`

	// governance configures gateway-level authorization and authentication.
	// +optional
	Governance *GovernanceSpec `json:"governance,omitzero"`
}

// AIGatewayStatus reports the observed state of an AIGateway.
type AIGatewayStatus struct {
	// conditions describe the gateway state.
	// The Ready condition reports that the spec is valid and the referenced
	// Tenant is ready.
	// +optional
	// +listType=map
	// +listMapKey=type
	Conditions []metav1.Condition `json:"conditions,omitzero"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="TenantRef",type=string,JSONPath=`.spec.tenantRef.name`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`

// AIGateway declares a gateway that exposes AI services for one Tenant.
// A Tenant may have multiple AIGateways. Each gateway declares its runtime,
// endpoint, inference and MCP capabilities, and governance.
type AIGateway struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec AIGatewaySpec `json:"spec"`

	// +optional
	Status AIGatewayStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// AIGatewayList contains a list of AIGateway.
type AIGatewayList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []AIGateway `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &AIGateway{}, &AIGatewayList{})
		return nil
	})
}
