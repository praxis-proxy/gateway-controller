package controller

import (
	"context"
	"fmt"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"
	"sigs.k8s.io/controller-runtime/pkg/client/interceptor"
	"sigs.k8s.io/yaml"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

// newRenderReader returns a client.Reader over the objects the render
// path resolves policy filter references against.
func newRenderReader(t *testing.T, objects ...client.Object) client.Reader {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := proxyv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("build scheme: %v", err)
	}
	return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
}

func TestRenderProxyConfig_Minimal(t *testing.T) {
	t.Parallel()
	spec := &proxyv1alpha1.ProxySpec{
		Image:     "ghcr.io/praxis-proxy/praxis:latest",
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	// The default bind renders the IPv4 wildcard, which never answers the
	// kubelet's dial to an IPv6 pod IP, so the controller appends the
	// dual-stack readiness listener next to it.
	if len(parsed.Listeners) != 2 {
		t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
	}
	if parsed.Listeners[0].Address != "0.0.0.0:8080" {
		t.Errorf("expected address 0.0.0.0:8080, got %s", parsed.Listeners[0].Address)
	}
	if parsed.Listeners[1].Name != "readiness" || parsed.Listeners[1].Address != "[::]:19902" {
		t.Errorf("expected the dual-stack readiness listener, got %+v", parsed.Listeners[1])
	}
	if parsed.InsecureOptions != nil {
		t.Error("expected no insecure_options block without the spec opting in")
	}
}

func TestRenderProxyConfig_AdminAndInsecure(t *testing.T) {
	t.Parallel()
	allowPriv := true
	denyUnbounded := false
	allowPublic := true
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		Admin: &proxyv1alpha1.PraxisAdmin{Address: "127.0.0.1:19901"},
		InsecureOptions: &proxyv1alpha1.PraxisInsecureOptions{
			AllowPrivateEndpoints: &allowPriv,
			AllowUnboundedBody:    &denyUnbounded,
			AllowPublicAdmin:      &allowPublic,
		},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	if parsed.Admin == nil || parsed.Admin.Address != "127.0.0.1:19901" {
		t.Errorf("admin address mismatch: %+v", parsed.Admin)
	}
	if parsed.InsecureOptions == nil {
		t.Fatal("expected insecure_options")
	}
	if parsed.InsecureOptions.AllowPrivateEndpoints == nil || !*parsed.InsecureOptions.AllowPrivateEndpoints {
		t.Error("expected allow_private_endpoints=true")
	}
	if parsed.InsecureOptions.AllowUnboundedBody == nil || *parsed.InsecureOptions.AllowUnboundedBody {
		t.Error("expected allow_unbounded_body=false")
	}
	if parsed.InsecureOptions.AllowPublicAdmin == nil || !*parsed.InsecureOptions.AllowPublicAdmin {
		t.Error("expected allow_public_admin=true")
	}
}

func TestRenderProxyConfig_IPv6BindAddress(t *testing.T) {
	t.Parallel()
	bindAddr := "::1"
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		Listeners: []proxyv1alpha1.PraxisListener{{
			Name:         "http",
			Port:         8080,
			BindAddress:  &bindAddr,
			FilterChains: []string{},
		}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	if parsed.Listeners[0].Address != "[::1]:8080" {
		t.Errorf("expected [::1]:8080, got %s", parsed.Listeners[0].Address)
	}
}

func TestRenderProxyConfig_ListenerTLS(t *testing.T) {
	t.Parallel()
	bindAddr := "10.0.0.1"
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		Listeners: []proxyv1alpha1.PraxisListener{{
			Name:        "https",
			Port:        8443,
			BindAddress: &bindAddr,
			TLS:         &proxyv1alpha1.PraxisListenerTLS{CertificateRef: proxyv1alpha1.PraxisLocalReference{Name: "gw-tls"}},
		}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	listener := parsed.Listeners[0]
	if listener.Address != "10.0.0.1:8443" {
		t.Errorf("expected 10.0.0.1:8443, got %s", listener.Address)
	}
	if listener.TLS == nil {
		t.Fatal("expected TLS config")
	}
	// Pin the TLS wire format: the Praxis release pinned in
	// config/samples/proxy/proxy.yaml rejects a flat cert_path/key_path
	// pair on the listener's tls block ("unknown field `cert_path`,
	// expected one of `certificates`, ...") and crashes at startup, so
	// the certificate must render as the certificates list the runtime
	// loads. Verified against that release with a live, CA-verified
	// TLS request through a listener configured with this form.
	var raw map[string]any
	if err := yaml.Unmarshal([]byte(got.Config), &raw); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	rawListeners, ok := raw["listeners"].([]any)
	if !ok || len(rawListeners) < 1 {
		t.Fatalf("listeners = %#v, want at least one", raw["listeners"])
	}
	var https map[string]any
	for _, l := range rawListeners {
		listener, ok := l.(map[string]any)
		if !ok {
			continue
		}
		if listener["name"] == "https" {
			https = listener
		}
	}
	if https == nil {
		t.Fatalf("listeners = %#v, want one named https", raw["listeners"])
	}
	tlsBlock, ok := https["tls"].(map[string]any)
	if !ok {
		t.Fatalf("listener tls block = %#v, want an object", rawListeners[0])
	}
	certificates, ok := tlsBlock["certificates"].([]any)
	if !ok || len(certificates) != 1 {
		t.Fatalf("tls.certificates = %#v, want one entry", tlsBlock["certificates"])
	}
	certificate, ok := certificates[0].(map[string]any)
	if !ok {
		t.Fatalf("tls.certificates[0] = %#v, want an object", certificates[0])
	}
	if certificate["cert_path"] != "/etc/praxis/tls/https/tls.crt" {
		t.Errorf("tls.certificates[0].cert_path = %v, want /etc/praxis/tls/https/tls.crt", certificate["cert_path"])
	}
	if certificate["key_path"] != "/etc/praxis/tls/https/tls.key" {
		t.Errorf("tls.certificates[0].key_path = %v, want /etc/praxis/tls/https/tls.key", certificate["key_path"])
	}
}

func TestRenderProxyConfig_ReservedConfigKeys(t *testing.T) {
	t.Parallel()

	// inlineSpec returns a minimal valid spec whose single inline filter
	// carries the given raw JSON configuration.
	inlineSpec := func(config string) *proxyv1alpha1.ProxySpec {
		return &proxyv1alpha1.ProxySpec{
			Image: "ghcr.io/praxis-proxy/praxis:latest",
			FilterChains: []proxyv1alpha1.ProxyFilterChain{{
				Name: "main",
				Filters: []proxyv1alpha1.PraxisFilterEntry{{
					Name: "rl",
					InlineFilter: &proxyv1alpha1.PraxisInlineFilter{
						Type:   "rate_limiter",
						Config: &runtime.RawExtension{Raw: []byte(config)},
					},
				}},
			}},
			Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080, FilterChains: []string{"main"}}},
		}
	}

	// The inline Config is copied over the rendered filter entry, so
	// these keys would override or impersonate the fields the controller
	// renders from the typed spec — every controller-owned key must be
	// rejected, not just the ones the entry happens to set.
	for _, key := range []string{
		"name", "filter", "failure_mode", "conditions", "response_conditions", "config_path",
	} {
		t.Run("rejects inline config setting "+key, func(t *testing.T) {
			t.Parallel()
			config := fmt.Sprintf(`{%q:"evil"}`, key)
			_, err := renderProxyConfig(t.Context(), newRenderReader(t), inlineSpec(config), "default", "")
			if err == nil {
				t.Fatalf("inline config setting %q must be rejected", key)
			}
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error must name the reserved key %q, got: %v", key, err)
			}
			if !strings.Contains(err.Error(), `"rl"`) {
				t.Errorf("error must name the filter entry, got: %v", err)
			}
		})
	}

	t.Run("names every offending key", func(t *testing.T) {
		t.Parallel()
		_, err := renderProxyConfig(t.Context(), newRenderReader(t),
			inlineSpec(`{"name":"evil","filter":"evil"}`), "default", "")
		if err == nil {
			t.Fatal("inline config setting reserved keys must be rejected")
		}
		for _, key := range []string{`"name"`, `"filter"`} {
			if !strings.Contains(err.Error(), key) {
				t.Errorf("error must name reserved key %s, got: %v", key, err)
			}
		}
	})

	t.Run("keeps flattening ordinary filter configuration keys", func(t *testing.T) {
		t.Parallel()
		got, err := renderProxyConfig(t.Context(), newRenderReader(t),
			inlineSpec(`{"requests_per_second":100,"burst":200}`), "default", "")
		if err != nil {
			t.Fatalf("ordinary configuration keys must keep flattening: %v", err)
		}
		var parsed nativeConfig
		if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
			t.Fatalf("invalid YAML: %v", err)
		}
		filter := parsed.FilterChains[0].Filters[0]
		if filter["name"] != "rl" || filter["filter"] != "rate_limiter" {
			t.Errorf("controller-rendered keys must stay authoritative, got: %v", filter)
		}
		if v, ok := filter["burst"]; !ok || v != float64(200) {
			t.Errorf("ordinary key must still be flattened, burst: %v", v)
		}
		if v, ok := filter["requests_per_second"]; !ok || v != float64(100) {
			t.Errorf("ordinary key must still be flattened, requests_per_second: %v", v)
		}
	})
}

func TestRenderProxyConfig_InlineFilter(t *testing.T) {
	t.Parallel()
	fmOpen := proxyv1alpha1.FailureModeOpen
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "main",
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name: "rl",
				InlineFilter: &proxyv1alpha1.PraxisInlineFilter{
					Type:   "rate_limiter",
					Config: &runtime.RawExtension{Raw: []byte(`{"requests_per_second":100,"burst":200}`)},
				},
				FailureMode: &fmOpen,
			}},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080, FilterChains: []string{"main"}}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	filter := parsed.FilterChains[0].Filters[0]
	if filter["filter"] != "rate_limiter" {
		t.Errorf("filter type: %v", filter["filter"])
	}
	if filter["failure_mode"] != "open" {
		t.Errorf("failure_mode: %v", filter["failure_mode"])
	}
	if v, ok := filter["requests_per_second"]; !ok || v != float64(100) {
		t.Errorf("requests_per_second: %v", v)
	}
	if v, ok := filter["burst"]; !ok || v != float64(200) {
		t.Errorf("burst: %v", v)
	}
}

func TestRenderProxyConfig_PolicyRef(t *testing.T) {
	t.Parallel()

	policyDocument := "apiVersion: policy.praxis.io/v1\nkind: PolicyEngine\ndocument: minimal"

	t.Run("explicit namespace", func(t *testing.T) {
		t.Parallel()
		fmClosed := proxyv1alpha1.FailureModeClosed
		spec := &proxyv1alpha1.ProxySpec{
			Image: "ghcr.io/praxis-proxy/praxis:latest",
			FilterChains: []proxyv1alpha1.ProxyFilterChain{{
				Name: "policy-chain",
				Filters: []proxyv1alpha1.PraxisFilterEntry{{
					Name: "guardrails",
					Ref: &proxyv1alpha1.PraxisFilterReference{
						Group: proxyv1alpha1.PraxisGroup, Kind: proxyv1alpha1.PolicyKind,
						Namespace: "other-ns", Name: "my-policy",
					},
					FailureMode: &fmClosed,
				}},
			}},
			Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
		}
		policy := &proxyv1alpha1.Policy{
			ObjectMeta: metav1.ObjectMeta{Namespace: "other-ns", Name: "my-policy"},
			Spec:       proxyv1alpha1.PolicySpec{Document: policyDocument},
		}
		got, err := renderProxyConfig(t.Context(), newRenderReader(t, policy), spec, "default", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed nativeConfig
		if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
			t.Fatalf("invalid YAML: %v", err)
		}
		filter := parsed.FilterChains[0].Filters[0]
		if filter["filter"] != "policy" {
			t.Errorf("filter: %v", filter["filter"])
		}
		if filter["config_path"] != "/etc/praxis/policies/other-ns/my-policy.yaml" {
			t.Errorf("config_path: %v", filter["config_path"])
		}
		if got.PolicyDocuments["/etc/praxis/policies/other-ns/my-policy.yaml"] != policyDocument {
			t.Errorf("policy document collection: %v", got.PolicyDocuments)
		}
	})

	t.Run("defaults to proxy namespace", func(t *testing.T) {
		t.Parallel()
		spec := &proxyv1alpha1.ProxySpec{
			Image: "ghcr.io/praxis-proxy/praxis:latest",
			FilterChains: []proxyv1alpha1.ProxyFilterChain{{
				Name: "chain",
				Filters: []proxyv1alpha1.PraxisFilterEntry{{
					Name: "p",
					Ref: &proxyv1alpha1.PraxisFilterReference{
						Group: proxyv1alpha1.PraxisGroup, Kind: proxyv1alpha1.PolicyKind,
						Name: "local-policy",
					},
				}},
			}},
			Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
		}
		// No filter config: only the structural keys render.
		policy := &proxyv1alpha1.Policy{
			ObjectMeta: metav1.ObjectMeta{Namespace: "my-ns", Name: "local-policy"},
			Spec:       proxyv1alpha1.PolicySpec{Document: policyDocument},
		}
		got, err := renderProxyConfig(t.Context(), newRenderReader(t, policy), spec, "my-ns", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed nativeConfig
		if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
			t.Fatalf("invalid YAML: %v", err)
		}
		filter := parsed.FilterChains[0].Filters[0]
		if filter["config_path"] != "/etc/praxis/policies/my-ns/local-policy.yaml" {
			t.Errorf("config_path: %v", filter["config_path"])
		}
		for _, key := range []string{"body_access", "llm", "context_meta"} {
			if _, ok := filter[key]; ok {
				t.Errorf("unset policy filter option %q must not render", key)
			}
		}
		if got.PolicyDocuments["/etc/praxis/policies/my-ns/local-policy.yaml"] != policyDocument {
			t.Errorf("policy document collection: %v", got.PolicyDocuments)
		}
	})
}

func TestRenderProxyConfig_PolicyFilterConfig(t *testing.T) {
	t.Parallel()

	readWrite := proxyv1alpha1.BodyAccessReadWrite
	initTimeout := int32(45)
	maxBuffer := int64(2097152)
	allowPrivateIdp := true
	requireProtocolMetadata := false
	maxRequestBytes := int64(1048576)
	provider := "openai"
	requireModel := true
	scope := "tenant-acme"
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "policy-chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name: "guardrails",
				Ref: &proxyv1alpha1.PraxisFilterReference{
					Group: proxyv1alpha1.PraxisGroup, Kind: proxyv1alpha1.PolicyKind,
					Namespace: "other-ns", Name: "my-policy",
				},
			}},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	policy := &proxyv1alpha1.Policy{
		ObjectMeta: metav1.ObjectMeta{Namespace: "other-ns", Name: "my-policy"},
		Spec: proxyv1alpha1.PolicySpec{
			Document: "kind: policy-document",
			Filter: &proxyv1alpha1.PolicyFilterConfig{
				BodyAccess:              &readWrite,
				InitTimeoutSeconds:      &initTimeout,
				MaxBufferBytes:          &maxBuffer,
				AllowPrivateIdp:         &allowPrivateIdp,
				TrustedPrivateEndpoints: []string{"internal.example.com"},
				RequireProtocolMetadata: &requireProtocolMetadata,
				LLM: &proxyv1alpha1.PolicyLLMOptions{
					MaxRequestBytes: &maxRequestBytes,
					PromoteParams:   []string{"model", "route"},
					Provider:        &provider,
					RequireModel:    &requireModel,
				},
				ContextHeaders: map[string]string{"agent.conversation_id": "x-praxis-conversation"},
				ContextMeta: &proxyv1alpha1.PolicyContextMeta{
					Tags:       []string{"production"},
					Scope:      &scope,
					Properties: map[string]string{"tier": "gold"},
				},
			},
		},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t, policy), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	filter := parsed.FilterChains[0].Filters[0]
	if filter["body_access"] != "read_write" {
		t.Errorf("body_access: %v", filter["body_access"])
	}
	if filter["init_timeout_secs"] != float64(45) {
		t.Errorf("init_timeout_secs: %v", filter["init_timeout_secs"])
	}
	if filter["max_buffer_bytes"] != float64(2097152) {
		t.Errorf("max_buffer_bytes: %v", filter["max_buffer_bytes"])
	}
	if filter["allow_private_idp"] != true {
		t.Errorf("allow_private_idp: %v", filter["allow_private_idp"])
	}
	if filter["require_protocol_metadata"] != false {
		t.Errorf("require_protocol_metadata: %v", filter["require_protocol_metadata"])
	}
	if v, ok := filter["trusted_private_endpoints"]; !ok || fmt.Sprint(v) != "[internal.example.com]" {
		t.Errorf("trusted_private_endpoints: %v", v)
	}
	llm, ok := filter["llm"].(map[string]any)
	if !ok {
		t.Fatalf("llm: %v", filter["llm"])
	}
	if llm["max_request_bytes"] != float64(1048576) {
		t.Errorf("llm.max_request_bytes: %v", llm["max_request_bytes"])
	}
	if llm["provider"] != "openai" {
		t.Errorf("llm.provider: %v", llm["provider"])
	}
	if llm["require_model"] != true {
		t.Errorf("llm.require_model: %v", llm["require_model"])
	}
	if v, ok := llm["promote_params"]; !ok || fmt.Sprint(v) != "[model route]" {
		t.Errorf("llm.promote_params: %v", v)
	}
	if v, ok := filter["context_headers"].(map[string]any); !ok || v["agent.conversation_id"] != "x-praxis-conversation" {
		t.Errorf("context_headers: %v", filter["context_headers"])
	}
	meta, ok := filter["context_meta"].(map[string]any)
	if !ok {
		t.Fatalf("context_meta: %v", filter["context_meta"])
	}
	if meta["scope"] != "tenant-acme" {
		t.Errorf("context_meta.scope: %v", meta["scope"])
	}
	if v, ok := meta["tags"].([]any); !ok || len(v) != 1 || v[0] != "production" {
		t.Errorf("context_meta.tags: %v", meta["tags"])
	}
	if v, ok := meta["properties"].(map[string]any); !ok || v["tier"] != "gold" {
		t.Errorf("context_meta.properties: %v", meta["properties"])
	}
	// The filter config sits flat under the filter entry, not nested
	// under a config wrapper.
	if _, ok := filter["config"]; ok {
		t.Errorf("policy filter config must be flat, found config wrapper: %v", filter["config"])
	}
}

func TestRenderProxyConfig_PolicyDocumentCollection(t *testing.T) {
	t.Parallel()
	firstDocument := "kind: first"
	secondDocument := "kind: second"
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "shared",
			// The same policy referenced twice collects one document;
			// a second policy from another namespace collects its own.
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "guardrails-a", Ref: &proxyv1alpha1.PraxisFilterReference{
					Group: proxyv1alpha1.PraxisGroup, Kind: proxyv1alpha1.PolicyKind, Name: "shared-policy",
				}},
				{Name: "guardrails-b", Ref: &proxyv1alpha1.PraxisFilterReference{
					Group: proxyv1alpha1.PraxisGroup, Kind: proxyv1alpha1.PolicyKind, Name: "shared-policy",
				}},
			},
		}, {
			Name: "cross-ns",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "guardrails-c", Ref: &proxyv1alpha1.PraxisFilterReference{
					Group: proxyv1alpha1.PraxisGroup, Kind: proxyv1alpha1.PolicyKind,
					Namespace: "other-ns", Name: "remote-policy",
				}},
			},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	reader := newRenderReader(t,
		&proxyv1alpha1.Policy{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "shared-policy"},
			Spec:       proxyv1alpha1.PolicySpec{Document: firstDocument},
		},
		&proxyv1alpha1.Policy{
			ObjectMeta: metav1.ObjectMeta{Namespace: "other-ns", Name: "remote-policy"},
			Spec:       proxyv1alpha1.PolicySpec{Document: secondDocument},
		},
	)
	got, err := renderProxyConfig(t.Context(), reader, spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	want := map[string]string{
		"/etc/praxis/policies/default/shared-policy.yaml":  firstDocument,
		"/etc/praxis/policies/other-ns/remote-policy.yaml": secondDocument,
	}
	if len(got.PolicyDocuments) != len(want) {
		t.Fatalf("policy documents = %v, want exactly %v", got.PolicyDocuments, want)
	}
	for path, document := range want {
		if got.PolicyDocuments[path] != document {
			t.Errorf("policy document %q = %q, want %q", path, got.PolicyDocuments[path], document)
		}
	}
}

func TestRenderProxyConfig_PolicyLookupError(t *testing.T) {
	t.Parallel()
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name: "guardrails",
				Ref: &proxyv1alpha1.PraxisFilterReference{
					Group: proxyv1alpha1.PraxisGroup, Kind: proxyv1alpha1.PolicyKind,
					Namespace: "other-ns", Name: "missing-policy",
				},
			}},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	// The referenced Policy is absent from the reader, so the render
	// fails with the lookup error.
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err == nil {
		t.Fatalf("expected a policy lookup error, got config %q", got.Config)
	}
	if !strings.Contains(err.Error(), "resolve policy other-ns/missing-policy") {
		t.Errorf("error should name the unresolved policy: %v", err)
	}
	if !apierrors.IsNotFound(err) {
		t.Errorf("lookup error should preserve the not-found cause: %v", err)
	}
	if !strings.Contains(err.Error(), "chain \"chain\" filter \"guardrails\"") {
		t.Errorf("error should name the referencing filter entry: %v", err)
	}
}

func TestRenderProxyConfig_Conditions(t *testing.T) {
	t.Parallel()
	apiPrefix := "/api"
	healthPath := "/healthz"
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "conditioned",
			Conditions: []proxyv1alpha1.PraxisRequestCondition{{
				When: &proxyv1alpha1.PraxisRequestMatch{
					PathPrefix: &apiPrefix,
					Methods:    []string{"POST"},
					Headers:    map[string]string{"x-tenant": "acme"},
				},
			}},
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name:         "f",
				InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
				Conditions: []proxyv1alpha1.PraxisRequestCondition{{
					Unless: &proxyv1alpha1.PraxisRequestMatch{
						Path:           &healthPath,
						HeadersPresent: []string{"x-bypass"},
					},
				}},
				ResponseConditions: []proxyv1alpha1.PraxisResponseCondition{{
					When: &proxyv1alpha1.PraxisResponseMatch{
						Status:  []int32{200, 201},
						Headers: map[string]string{"content-type": "application/json"},
					},
				}},
			}},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	chain := parsed.FilterChains[0]
	if len(chain.Conditions) != 1 {
		t.Fatalf("expected 1 chain condition, got %d", len(chain.Conditions))
	}
	when := chain.Conditions[0]["when"].(map[string]any)
	if when["path_prefix"] != "/api" {
		t.Errorf("path_prefix: %v", when["path_prefix"])
	}

	filter := chain.Filters[0]
	filterConds := filter["conditions"].([]any)
	if len(filterConds) != 1 {
		t.Fatalf("expected 1 filter condition, got %d", len(filterConds))
	}
	respConds := filter["response_conditions"].([]any)
	if len(respConds) != 1 {
		t.Fatalf("expected 1 response condition, got %d", len(respConds))
	}
}

func TestRenderProxyConfig_UpstreamMatch(t *testing.T) {
	t.Parallel()
	appProto := "openai_chat_completions"
	appProvider := "openai"
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Conditions: []proxyv1alpha1.PraxisRequestCondition{{
				When: &proxyv1alpha1.PraxisRequestMatch{
					BoundUpstream: &proxyv1alpha1.PraxisSelectedUpstreamMatch{
						ApplicationProtocol: &appProto,
						ApplicationProvider: &appProvider,
					},
				},
			}},
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name:         "f",
				InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
			}},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	when := parsed.FilterChains[0].Conditions[0]["when"].(map[string]any)
	upstream := when["bound_upstream"].(map[string]any)
	if upstream["application_protocol"] != "openai_chat_completions" {
		t.Errorf("application_protocol: %v", upstream["application_protocol"])
	}
	if upstream["application_provider"] != "openai" {
		t.Errorf("application_provider: %v", upstream["application_provider"])
	}
}

func TestRenderProxyConfig_FilterChainRefs(t *testing.T) {
	t.Parallel()
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{
			{Name: "a", Filters: []proxyv1alpha1.PraxisFilterEntry{{Name: "f", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}}}},
			{Name: "b", Filters: []proxyv1alpha1.PraxisFilterEntry{{Name: "g", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "access_log"}}}},
		},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080, FilterChains: []string{"a", "b"}}},
	}
	got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	listener := parsed.Listeners[0]
	if len(listener.FilterChains) != 2 || listener.FilterChains[0] != "a" || listener.FilterChains[1] != "b" {
		t.Errorf("filter_chains: %v", listener.FilterChains)
	}
}

func TestRenderProxyConfig_UnsupportedFilterRef(t *testing.T) {
	t.Parallel()
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name: "unknown",
				Ref: &proxyv1alpha1.PraxisFilterReference{
					Group: "other.io",
					Kind:  "CustomFilter",
					Name:  "my-filter",
				},
			}},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	_, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
	if err == nil {
		t.Fatal("expected error for unsupported filter reference")
	}
	if !strings.Contains(err.Error(), "unsupported filter") {
		t.Errorf("unexpected error: %v", err)
	}
}

// TestClusterPrimaryIPFamily verifies the controller reads the pod
// network's primary family from the default/kubernetes Service — the
// same signal the kubelet and cluster tooling rely on. A missing
// Service or missing families must yield the unknown family rather
// than an error, so environments without the Service (some API
// servers) keep the family-agnostic readiness behavior.
func TestClusterPrimaryIPFamily(t *testing.T) {
	t.Parallel()

	newServiceReader := func(t *testing.T, objects ...client.Object) client.Reader {
		t.Helper()
		scheme := runtime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatalf("build scheme: %v", err)
		}
		return fake.NewClientBuilder().WithScheme(scheme).WithObjects(objects...).Build()
	}
	kubernetesService := func(families ...corev1.IPFamily) *corev1.Service {
		return &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Namespace: "default", Name: "kubernetes"},
			Spec:       corev1.ServiceSpec{IPFamilies: families},
		}
	}

	t.Run("reads the first family of default/kubernetes", func(t *testing.T) {
		t.Parallel()
		reader := newServiceReader(t, kubernetesService(corev1.IPv4Protocol, corev1.IPv6Protocol))
		got, err := clusterPrimaryIPFamily(t.Context(), reader)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != corev1.IPv4Protocol {
			t.Errorf("expected the first family IPv4, got %q", got)
		}
	})

	t.Run("reports IPv6 for IPv6-primary clusters", func(t *testing.T) {
		t.Parallel()
		reader := newServiceReader(t, kubernetesService(corev1.IPv6Protocol))
		got, err := clusterPrimaryIPFamily(t.Context(), reader)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		if got != corev1.IPv6Protocol {
			t.Errorf("expected IPv6, got %q", got)
		}
	})

	t.Run("reports the unknown family without the Service", func(t *testing.T) {
		t.Parallel()
		got, err := clusterPrimaryIPFamily(t.Context(), newServiceReader(t))
		if err != nil {
			t.Fatalf("a missing Service must not fail the reconcile: %v", err)
		}
		if got != "" {
			t.Errorf("expected the unknown family, got %q", got)
		}
	})

	t.Run("reports the unknown family without reported families", func(t *testing.T) {
		t.Parallel()
		reader := newServiceReader(t, kubernetesService())
		got, err := clusterPrimaryIPFamily(t.Context(), reader)
		if err != nil {
			t.Fatalf("missing families must not fail the reconcile: %v", err)
		}
		if got != "" {
			t.Errorf("expected the unknown family, got %q", got)
		}
	})

	t.Run("fails the reconcile on reader errors", func(t *testing.T) {
		t.Parallel()
		scheme := runtime.NewScheme()
		if err := corev1.AddToScheme(scheme); err != nil {
			t.Fatalf("build scheme: %v", err)
		}
		reader := fake.NewClientBuilder().WithScheme(scheme).
			WithInterceptorFuncs(interceptor.Funcs{
				Get: func(context.Context, client.WithWatch, client.ObjectKey, client.Object, ...client.GetOption) error {
					return fmt.Errorf("apiserver unreachable")
				},
			}).Build()
		if _, err := clusterPrimaryIPFamily(t.Context(), reader); err == nil {
			t.Fatal("expected an error for reader failures")
		}
	})
}

// TestPodIPBind verifies which listener binds the kubelet's dial to the
// pod IP can reach: the wildcard of the pod network's primary family,
// with :: staying reachable on IPv4 through v4-mapped addresses.
func TestPodIPBind(t *testing.T) {
	t.Parallel()
	bind := func(addr string) *string { return &addr }
	cases := []struct {
		name    string
		bind    *string
		primary corev1.IPFamily
		want    bool
	}{
		{
			name:    "default bind reaches an IPv4 pod IP",
			bind:    nil,
			primary: corev1.IPv4Protocol,
			want:    true,
		},
		{
			name:    "default bind cannot reach an IPv6 pod IP",
			bind:    nil,
			primary: corev1.IPv6Protocol,
			want:    false,
		},
		{
			name:    "default bind stays unreachable while the family is unknown",
			bind:    nil,
			primary: "",
			want:    false,
		},
		{
			name:    "IPv4 wildcard reaches an IPv4 pod IP",
			bind:    bind("0.0.0.0"),
			primary: corev1.IPv4Protocol,
			want:    true,
		},
		{
			name:    "IPv4 wildcard cannot reach an IPv6 pod IP",
			bind:    bind("0.0.0.0"),
			primary: corev1.IPv6Protocol,
			want:    false,
		},
		{
			name:    "dual-stack wildcard reaches an IPv4 pod IP via v4-mapped addresses",
			bind:    bind("::"),
			primary: corev1.IPv4Protocol,
			want:    true,
		},
		{
			name:    "dual-stack wildcard reaches an IPv6 pod IP",
			bind:    bind("::"),
			primary: corev1.IPv6Protocol,
			want:    true,
		},
		{
			name:    "dual-stack wildcard stays reachable while the family is unknown",
			bind:    bind("::"),
			primary: "",
			want:    true,
		},
		{
			name:    "IPv4 loopback binds are not pod-IP-reachable",
			bind:    bind("127.0.0.1"),
			primary: corev1.IPv4Protocol,
			want:    false,
		},
		{
			name:    "IPv6 loopback binds are not pod-IP-reachable",
			bind:    bind("::1"),
			primary: corev1.IPv6Protocol,
			want:    false,
		},
		{
			name:    "specific IPv4 binds are not pod-IP-reachable",
			bind:    bind("10.1.2.3"),
			primary: corev1.IPv4Protocol,
			want:    false,
		},
		{
			name:    "specific IPv6 binds are not pod-IP-reachable",
			bind:    bind("fd00::1"),
			primary: corev1.IPv6Protocol,
			want:    false,
		},
		{
			name:    "invalid bind addresses are not pod-IP-reachable",
			bind:    bind("not-an-ip"),
			primary: corev1.IPv4Protocol,
			want:    false,
		},
	}
	for _, tc := range cases {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()
			if got := podIPBind(tc.bind, tc.primary); got != tc.want {
				t.Errorf("podIPBind(%v, %q) = %v, want %v", tc.bind, tc.primary, got, tc.want)
			}
		})
	}
}

// TestReadinessWildcard verifies the dedicated readiness listener binds
// the wildcard of the pod network's primary family, falling back to the
// dual-stack wildcard while the family is unknown.
func TestReadinessWildcard(t *testing.T) {
	t.Parallel()
	cases := []struct {
		primary corev1.IPFamily
		want    string
	}{
		{primary: corev1.IPv4Protocol, want: "0.0.0.0"},
		{primary: corev1.IPv6Protocol, want: "::"},
		{primary: "", want: "::"},
	}
	for _, tc := range cases {
		if got := readinessWildcard(tc.primary); got != tc.want {
			t.Errorf("readinessWildcard(%q) = %q, want %q", tc.primary, got, tc.want)
		}
	}
}

func TestRenderProxyConfig_ReadinessListener(t *testing.T) {
	t.Parallel()

	render := func(t *testing.T, spec *proxyv1alpha1.ProxySpec) nativeConfig {
		t.Helper()
		got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", "")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed nativeConfig
		if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
			t.Fatalf("invalid YAML: %v", err)
		}
		return parsed
	}

	t.Run("renders no readiness listener for dual-stack-bound listeners", func(t *testing.T) {
		t.Parallel()
		// Only the IPv6 wildcard accepts the kubelet's dial on either
		// pod IP family, so a listener bound to it is probed directly.
		dualStack := "::"
		parsed := render(t, readinessRenderSpec(&dualStack))
		if len(parsed.Listeners) != 1 {
			t.Fatalf("expected only the spec listener, got %d", len(parsed.Listeners))
		}
	})

	t.Run("renders a dedicated readiness listener for IPv4-wildcard binds", func(t *testing.T) {
		t.Parallel()
		// The IPv4 wildcard is the API default and never answers a dial
		// to an IPv6 pod IP, so it needs the dedicated listener.
		ipv4Wildcard := "0.0.0.0"
		parsed := render(t, readinessRenderSpec(&ipv4Wildcard))
		if len(parsed.Listeners) != 2 {
			t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
		}
		readiness := parsed.Listeners[1]
		if readiness.Name != "readiness" {
			t.Errorf("readiness listener name: %q", readiness.Name)
		}
		if readiness.Address != "[::]:19902" {
			t.Errorf("readiness listener must bind the dual-stack wildcard, got: %q", readiness.Address)
		}
		if len(readiness.FilterChains) != 1 || readiness.FilterChains[0] != "readiness" {
			t.Errorf("readiness listener must reference its dedicated health-only chain, got: %v", readiness.FilterChains)
		}
		assertReadinessHealthChain(t, parsed, "readiness")
	})

	t.Run("renders a dedicated readiness listener for specific binds", func(t *testing.T) {
		t.Parallel()
		loopback := "127.0.0.1"
		parsed := render(t, readinessRenderSpec(&loopback))
		if len(parsed.Listeners) != 2 {
			t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
		}
		readiness := parsed.Listeners[1]
		if readiness.Name != "readiness" {
			t.Errorf("readiness listener name: %q", readiness.Name)
		}
		if readiness.Address != "[::]:19902" {
			t.Errorf("readiness listener must bind the dual-stack wildcard, got: %q", readiness.Address)
		}
		if len(readiness.FilterChains) != 1 || readiness.FilterChains[0] != "readiness" {
			t.Errorf("readiness listener must reference its dedicated health-only chain, got: %v", readiness.FilterChains)
		}
		assertReadinessHealthChain(t, parsed, "readiness")
		if readiness.TLS != nil {
			t.Errorf("readiness listener must not add TLS when the spec has none, got: %+v", readiness.TLS)
		}
	})

	t.Run("mirrors the first listener's TLS for TLS-enabled proxies", func(t *testing.T) {
		t.Parallel()
		loopback := "127.0.0.1"
		spec := readinessRenderSpec(&loopback)
		spec.Listeners[0].TLS = &proxyv1alpha1.PraxisListenerTLS{
			CertificateRef: proxyv1alpha1.PraxisLocalReference{Name: "gw-tls"},
		}
		parsed := render(t, spec)
		if len(parsed.Listeners) != 2 {
			t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
		}
		readiness := parsed.Listeners[1]
		if readiness.TLS == nil {
			t.Fatal("readiness listener must preserve the first listener's TLS")
		}
		if len(readiness.TLS.Certificates) != 1 {
			t.Fatalf("readiness listener certificates = %+v, want one", readiness.TLS.Certificates)
		}
		if readiness.TLS.Certificates[0].CertPath != "/etc/praxis/tls/http/tls.crt" {
			t.Errorf("readiness listener cert_path: %s", readiness.TLS.Certificates[0].CertPath)
		}
		if readiness.TLS.Certificates[0].KeyPath != "/etc/praxis/tls/http/tls.key" {
			t.Errorf("readiness listener key_path: %s", readiness.TLS.Certificates[0].KeyPath)
		}
		if len(readiness.FilterChains) != 1 || readiness.FilterChains[0] != "readiness" {
			t.Errorf("readiness listener must reference its dedicated health-only chain, got: %v", readiness.FilterChains)
		}
		assertReadinessHealthChain(t, parsed, "readiness")
	})

	t.Run("binds the dual-stack wildcard for IPv6-specific binds", func(t *testing.T) {
		t.Parallel()
		loopback := "::1"
		parsed := render(t, readinessRenderSpec(&loopback))
		if len(parsed.Listeners) != 2 {
			t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
		}
		readiness := parsed.Listeners[1]
		if readiness.Address != "[::]:19902" {
			t.Errorf("readiness listener must bind the dual-stack wildcard for IPv6-specific binds, got: %q", readiness.Address)
		}
	})

	t.Run("avoids spec listener names, chain names, and occupied ports", func(t *testing.T) {
		t.Parallel()
		loopback := "127.0.0.1"
		spec := readinessRenderSpec(&loopback)
		spec.Listeners[0].Name = "readiness"
		spec.Listeners[0].Port = 19902
		spec.Listeners[0].FilterChains = []string{"readiness"}
		spec.FilterChains[0].Name = "readiness"
		spec.Admin = &proxyv1alpha1.PraxisAdmin{Address: "127.0.0.1:19903"}
		parsed := render(t, spec)
		if len(parsed.Listeners) != 2 {
			t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
		}
		readiness := parsed.Listeners[1]
		if readiness.Name != "readiness-0" {
			t.Errorf("readiness listener must avoid the spec listener name, got: %q", readiness.Name)
		}
		if readiness.Address != "[::]:19904" {
			t.Errorf("readiness listener must avoid occupied ports, got: %q", readiness.Address)
		}
		if len(readiness.FilterChains) != 1 || readiness.FilterChains[0] != "readiness-0" {
			t.Errorf("readiness chain must avoid the spec chain name, got: %v", readiness.FilterChains)
		}
		assertReadinessHealthChain(t, parsed, "readiness-0")
	})
}

// TestRenderProxyConfig_ReadinessListenerFamily verifies the readiness
// path follows the pod network's primary family instead of always
// relying on a :: bind, which an IPv6-less or IPv6-only pod network
// cannot serve: on IPv4-primary clusters the default bind is probed
// directly and the dedicated readiness listener binds the IPv4
// wildcard, while IPv6-primary clusters keep the dual-stack behavior.
func TestRenderProxyConfig_ReadinessListenerFamily(t *testing.T) {
	t.Parallel()

	render := func(t *testing.T, spec *proxyv1alpha1.ProxySpec, primary corev1.IPFamily) nativeConfig {
		t.Helper()
		got, err := renderProxyConfig(t.Context(), newRenderReader(t), spec, "default", primary)
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed nativeConfig
		if err := yaml.Unmarshal([]byte(got.Config), &parsed); err != nil {
			t.Fatalf("invalid YAML: %v", err)
		}
		return parsed
	}

	t.Run("probes the default bind directly on IPv4-primary clusters", func(t *testing.T) {
		t.Parallel()
		// The default bind renders the IPv4 wildcard, which answers the
		// kubelet's dial to an IPv4 pod IP: no dedicated readiness
		// listener and no :: bind the pod network may not support.
		parsed := render(t, readinessRenderSpec(nil), corev1.IPv4Protocol)
		if len(parsed.Listeners) != 1 {
			t.Fatalf("expected only the spec listener, got %d", len(parsed.Listeners))
		}
		if parsed.Listeners[0].Address != "0.0.0.0:8080" {
			t.Errorf("spec listener must stay bound per the spec, got: %q", parsed.Listeners[0].Address)
		}
	})

	t.Run("binds the IPv4 wildcard readiness listener on IPv4-primary clusters", func(t *testing.T) {
		t.Parallel()
		// Loopback binds are unreachable through the pod IP on any
		// family, so the dedicated listener still renders — bound to
		// the primary family's wildcard, which an IPv6-less pod network
		// can always bind.
		loopback := "127.0.0.1"
		parsed := render(t, readinessRenderSpec(&loopback), corev1.IPv4Protocol)
		if len(parsed.Listeners) != 2 {
			t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
		}
		readiness := parsed.Listeners[1]
		if readiness.Address != "0.0.0.0:19902" {
			t.Errorf("readiness listener must bind the primary family's wildcard, got: %q", readiness.Address)
		}
		assertReadinessHealthChain(t, parsed, "readiness")
	})

	t.Run("binds the dual-stack readiness listener on IPv6-primary clusters", func(t *testing.T) {
		t.Parallel()
		// The default bind renders the IPv4 wildcard, which never
		// answers a dial to an IPv6 pod IP: the dedicated listener
		// renders bound to the dual-stack wildcard.
		parsed := render(t, readinessRenderSpec(nil), corev1.IPv6Protocol)
		if len(parsed.Listeners) != 2 {
			t.Fatalf("expected the spec listener plus the readiness listener, got %d", len(parsed.Listeners))
		}
		readiness := parsed.Listeners[1]
		if readiness.Address != "[::]:19902" {
			t.Errorf("readiness listener must bind the dual-stack wildcard, got: %q", readiness.Address)
		}
		assertReadinessHealthChain(t, parsed, "readiness")
	})
}

// assertReadinessHealthChain verifies the named chain is the readiness
// listener's health-only pipeline: a single static_response filter that
// answers with a fixed response without contacting any upstream — the
// form the Praxis configuration reference documents for health checks.
// The runtime rejects chainless HTTP listeners, so the chain is what
// keeps the rendered config valid.
func assertReadinessHealthChain(t *testing.T, parsed nativeConfig, name string) {
	t.Helper()
	var chain *nativeChain
	for i := range parsed.FilterChains {
		if parsed.FilterChains[i].Name == name {
			chain = &parsed.FilterChains[i]
		}
	}
	if chain == nil {
		t.Fatalf("rendered config must define the %q health-only chain", name)
	}
	if len(chain.Filters) != 1 {
		t.Fatalf("health-only chain must carry a single filter, got: %v", chain.Filters)
	}
	filter := chain.Filters[0]
	if filter["filter"] != "static_response" {
		t.Errorf("health-only chain must use the static_response filter, got: %v", filter["filter"])
	}
	status, ok := filter["status"].(float64)
	if !ok || status != 200 {
		t.Errorf("health-only chain must answer with a fixed 200 response, got: %v", filter["status"])
	}
}

// readinessRenderSpec returns a minimal valid spec whose single listener
// binds bindAddress (nil meaning the wildcard default).
func readinessRenderSpec(bindAddress *string) *proxyv1alpha1.ProxySpec {
	return &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		Listeners: []proxyv1alpha1.PraxisListener{{
			Name:         "http",
			Port:         8080,
			BindAddress:  bindAddress,
			FilterChains: []string{"main"},
		}},
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "main",
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name:         "rl",
				InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
			}},
		}},
	}
}
