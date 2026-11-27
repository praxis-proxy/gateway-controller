/*
Copyright 2026.

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
	"k8s.io/apimachinery/pkg/runtime"
)

// ProxySpec defines the runtime image, listeners, filter chains, and service for a Praxis proxy.
type ProxySpec struct {
	// image is the Praxis runtime image, subject to the operator's approved runtime policy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Image string `json:"image"`

	// replicas is the desired replica count.
	// +optional
	// +kubebuilder:default=1
	// +kubebuilder:validation:Minimum=1
	Replicas *int32 `json:"replicas,omitempty"`

	// service configures the Service exposing listener ports.
	// +optional
	Service *ProxyService `json:"service,omitempty"`

	// secretEnv projects authorized same-namespace Secret keys as environment variables.
	// +optional
	SecretEnv []PraxisSecretEnv `json:"secretEnv,omitempty"`

	// filterChains defines reusable filter chains, referenced by name from listeners.
	// +optional
	// +listType=map
	// +listMapKey=name
	FilterChains []ProxyFilterChain `json:"filterChains,omitempty"`

	// listeners is a non-empty list of listeners, keyed by unique name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=map
	// +listMapKey=name
	Listeners []PraxisListener `json:"listeners"`

	// admin configures the Praxis admin interface.
	// +optional
	Admin *PraxisAdmin `json:"admin,omitempty"`

	// insecureOptions configures insecure runtime options that relax safety defaults.
	// +optional
	InsecureOptions *PraxisInsecureOptions `json:"insecureOptions,omitempty"`
}

// ProxyFilterChain defines an ordered list of filter and policy uses within a proxy.
type ProxyFilterChain struct {
	// name is the unique chain name within this proxy.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// conditions are chain-level request gates inherited by every filter in this chain.
	// Each filter's effective conditions are the chain conditions followed by its own (AND-composed).
	// +optional
	Conditions []PraxisRequestCondition `json:"conditions,omitempty"`

	// filters is a non-empty ordered list of filter/policy entries.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinItems=1
	// +listType=atomic
	Filters []PraxisFilterEntry `json:"filters"`
}

// ProxyService configures the Service for the proxy.
type ProxyService struct {
	// type is the Kubernetes Service type. Ports derive from listeners.
	// +optional
	// +kubebuilder:default="ClusterIP"
	Type *ServiceType `json:"type,omitempty"`
}

// PraxisSecretEnv projects a Secret key as an environment variable.
type PraxisSecretEnv struct {
	// name is the environment variable name used by a native filter.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// secretRef identifies the same-namespace Secret and key.
	// +kubebuilder:validation:Required
	SecretRef PraxisSecretKeyRef `json:"secretRef"`
}

// PraxisSecretKeyRef references a key within a same-namespace Secret.
type PraxisSecretKeyRef struct {
	// name is the Secret name.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Name string `json:"name"`

	// key is the key within the Secret.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Key string `json:"key"`
}

// PraxisListener defines a single listener on the proxy.
type PraxisListener struct {
	// name is the unique listener and generated container and Service
	// port name, so it must satisfy Kubernetes port-name validation (an
	// IANA service name, RFC 6335): lowercase letters, digits, and
	// hyphens, with at least one letter and no leading, trailing, or
	// consecutive hyphens. Names like "123" or "http--api" would make
	// the generated Deployment fail container-port validation.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	// +kubebuilder:validation:MaxLength=15
	// +kubebuilder:validation:Pattern=`^([a-z0-9]+-)*[a-z0-9]*[a-z][a-z0-9]*(-[a-z0-9]+)*$`
	Name string `json:"name"`

	// port is the container and Service port.
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:Minimum=1
	// +kubebuilder:validation:Maximum=65535
	Port int32 `json:"port"`

	// bindAddress is the IP the listener binds to in the native config.
	// Defaults to "0.0.0.0". Must be an IPv4 or IPv6 address literal; a
	// hostname cannot be resolved at render time and the Service family
	// selection could not account for it.
	// +optional
	// +kubebuilder:default="0.0.0.0"
	// +kubebuilder:validation:Pattern="^((25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])([.](25[0-5]|2[0-4][0-9]|1[0-9]{2}|[1-9]?[0-9])){3}|([0-9A-Fa-f]{1,4}:){7}[0-9A-Fa-f]{1,4}|([0-9A-Fa-f]{1,4}:){1,7}:|([0-9A-Fa-f]{1,4}:){1,6}:[0-9A-Fa-f]{1,4}|([0-9A-Fa-f]{1,4}:){1,5}(:[0-9A-Fa-f]{1,4}){1,2}|([0-9A-Fa-f]{1,4}:){1,4}(:[0-9A-Fa-f]{1,4}){1,3}|([0-9A-Fa-f]{1,4}:){1,3}(:[0-9A-Fa-f]{1,4}){1,4}|([0-9A-Fa-f]{1,4}:){1,2}(:[0-9A-Fa-f]{1,4}){1,5}|[0-9A-Fa-f]{1,4}:((:[0-9A-Fa-f]{1,4}){1,6})|:((:[0-9A-Fa-f]{1,4}){1,7}|:))$"
	BindAddress *string `json:"bindAddress,omitempty"`

	// tls configures downstream TLS material.
	// +optional
	TLS *PraxisListenerTLS `json:"tls,omitempty"`

	// filterChains references chains from spec.filterChains by name.
	// +optional
	FilterChains []string `json:"filterChains,omitempty"`
}

// PraxisListenerTLS configures downstream TLS for a listener.
type PraxisListenerTLS struct {
	// certificateRef is a same-namespace kubernetes.io/tls Secret.
	// +kubebuilder:validation:Required
	CertificateRef PraxisLocalReference `json:"certificateRef"`
}

// PraxisAdmin configures the Praxis admin interface.
type PraxisAdmin struct {
	// address is the admin bind address and port (e.g. "127.0.0.1:19901").
	// +kubebuilder:validation:Required
	// +kubebuilder:validation:MinLength=1
	Address string `json:"address"`
}

// PraxisInsecureOptions relaxes safety defaults in the Praxis runtime.
type PraxisInsecureOptions struct {
	// allowPrivateEndpoints permits routing to RFC 1918 addresses.
	// +optional
	AllowPrivateEndpoints *bool `json:"allowPrivateEndpoints,omitempty"`

	// allowUnboundedBody disables body size limits.
	// +optional
	AllowUnboundedBody *bool `json:"allowUnboundedBody,omitempty"`

	// allowPublicAdmin permits the admin interface to bind non-loopback
	// addresses, rendered as the Praxis allow_public_admin option. The
	// admin interface performs no authentication, so the controller only
	// publishes non-loopback admin binds when this is set.
	// +optional
	AllowPublicAdmin *bool `json:"allowPublicAdmin,omitempty"`
}

// ProxyStatus reports published and serving revision state.
type ProxyStatus struct {
	// observedGeneration is the latest proxy generation inspected.
	// +optional
	ObservedGeneration int64 `json:"observedGeneration,omitempty"`

	// desiredRevision identifies the rendered config and runtime image/environment inputs.
	// +optional
	DesiredRevision string `json:"desiredRevision,omitempty"`

	// publishedRevision identifies the config and workload successfully written for this proxy.
	// It does not acknowledge that running processes have reloaded the config.
	// +optional
	PublishedRevision string `json:"publishedRevision,omitempty"`

	// servingReplicas is the number of ready replicas reported by the Deployment.
	// It does not confirm that those replicas have loaded desiredRevision.
	// +optional
	ServingReplicas int32 `json:"servingReplicas,omitempty"`

	// listeners reports per-listener results and assembled chain order.
	// +optional
	Listeners []PraxisListenerStatus `json:"listeners,omitempty"`

	// availableFilters lists filter types reported by all serving replicas.
	// +optional
	AvailableFilters []string `json:"availableFilters,omitempty"`

	// conditions report Accepted, ResolvedRefs, Programmed, and Ready.
	// Ready reports desired workload readiness, not acknowledgement of a config reload.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// PraxisListenerStatus reports a single listener's status and chain order.
type PraxisListenerStatus struct {
	// name is the listener name from spec.
	// +kubebuilder:validation:Required
	Name string `json:"name"`

	// chainOrder lists attached chain identifiers (namespace/name) in effective pipeline order.
	// +optional
	ChainOrder []string `json:"chainOrder,omitempty"`

	// conditions report Programmed and Ready for this listener.
	// +listType=map
	// +listMapKey=type
	// +optional
	Conditions []metav1.Condition `json:"conditions,omitempty"`
}

// +kubebuilder:object:root=true
// +kubebuilder:subresource:status
// +kubebuilder:printcolumn:name="Image",type=string,JSONPath=`.spec.image`
// +kubebuilder:printcolumn:name="Replicas",type=integer,JSONPath=`.spec.replicas`
// +kubebuilder:printcolumn:name="Ready",type=string,JSONPath=`.status.conditions[?(@.type=="Ready")].status`
// +kubebuilder:printcolumn:name="Age",type=date,JSONPath=`.metadata.creationTimestamp`
// +kubebuilder:validation:XValidation:rule="size(self.metadata.name) <= 63",message="proxy name must not exceed 63 characters"
// +kubebuilder:validation:XValidation:rule="self.metadata.name.matches('^[a-z0-9]([-a-z0-9]*[a-z0-9])?$')",message="proxy name must be a lowercase DNS label (alphanumerics and hyphens, starting and ending with an alphanumeric) because the controller names the generated Service after the proxy"

// Proxy owns the Praxis Deployment, Service, config, assets, and listeners.
type Proxy struct {
	metav1.TypeMeta `json:",inline"`

	// +optional
	metav1.ObjectMeta `json:"metadata,omitzero"`

	// +required
	Spec ProxySpec `json:"spec"`

	// +optional
	Status ProxyStatus `json:"status,omitzero"`
}

// +kubebuilder:object:root=true

// ProxyList contains a list of Proxy.
type ProxyList struct {
	metav1.TypeMeta `json:",inline"`
	metav1.ListMeta `json:"metadata,omitzero"`
	Items           []Proxy `json:"items"`
}

func init() {
	SchemeBuilder.Register(func(s *runtime.Scheme) error {
		s.AddKnownTypes(SchemeGroupVersion, &Proxy{}, &ProxyList{})
		return nil
	})
}
