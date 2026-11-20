package controller

import (
	"strings"
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
	"sigs.k8s.io/yaml"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

func TestRenderProxyConfig_Minimal(t *testing.T) {
	t.Parallel()
	spec := &proxyv1alpha1.ProxySpec{
		Image:     "ghcr.io/praxis-proxy/praxis:latest",
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	if len(parsed.Listeners) != 1 {
		t.Fatalf("expected 1 listener, got %d", len(parsed.Listeners))
	}
	if parsed.Listeners[0].Address != "0.0.0.0:8080" {
		t.Errorf("expected address 0.0.0.0:8080, got %s", parsed.Listeners[0].Address)
	}
}

func TestRenderProxyConfig_AdminAndInsecure(t *testing.T) {
	t.Parallel()
	allowPriv := true
	denyUnbounded := false
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		Admin: &proxyv1alpha1.PraxisAdmin{Address: "127.0.0.1:19901"},
		InsecureOptions: &proxyv1alpha1.PraxisInsecureOptions{
			AllowPrivateEndpoints: &allowPriv,
			AllowUnboundedBody:    &denyUnbounded,
		},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
	}
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
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
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
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
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	listener := parsed.Listeners[0]
	if listener.Address != "10.0.0.1:8443" {
		t.Errorf("expected 10.0.0.1:8443, got %s", listener.Address)
	}
	if listener.TLS == nil {
		t.Fatal("expected TLS config")
	}
	if listener.TLS.CertPath != "/etc/praxis/tls/https/tls.crt" {
		t.Errorf("cert_path: %s", listener.TLS.CertPath)
	}
	if listener.TLS.KeyPath != "/etc/praxis/tls/https/tls.key" {
		t.Errorf("key_path: %s", listener.TLS.KeyPath)
	}
}

func TestRenderProxyConfig_ReservedKeysProtected(t *testing.T) {
	t.Parallel()
	spec := &proxyv1alpha1.ProxySpec{
		Image: "ghcr.io/praxis-proxy/praxis:latest",
		FilterChains: []proxyv1alpha1.ProxyFilterChain{{
			Name: "main",
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name: "rl",
				InlineFilter: &proxyv1alpha1.PraxisInlineFilter{
					Type:   "rate_limiter",
					Config: &runtime.RawExtension{Raw: []byte(`{"name":"evil","filter":"evil","burst":200}`)},
				},
			}},
		}},
		Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080, FilterChains: []string{"main"}}},
	}
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
		t.Fatalf("invalid YAML: %v", err)
	}
	filter := parsed.FilterChains[0].Filters[0]
	if filter["name"] != "rl" {
		t.Errorf("name should be protected, got: %v", filter["name"])
	}
	if filter["filter"] != "rate_limiter" {
		t.Errorf("filter type should be protected, got: %v", filter["filter"])
	}
	if v, ok := filter["burst"]; !ok || v != float64(200) {
		t.Errorf("non-reserved key should still be flattened, burst: %v", v)
	}
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
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
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
		got, err := renderProxyConfig(spec, "default")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed nativeConfig
		if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("invalid YAML: %v", err)
		}
		filter := parsed.FilterChains[0].Filters[0]
		if filter["filter"] != "policy" {
			t.Errorf("filter: %v", filter["filter"])
		}
		if filter["config_path"] != "/etc/praxis/policies/other-ns/my-policy.yaml" {
			t.Errorf("config_path: %v", filter["config_path"])
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
		got, err := renderProxyConfig(spec, "my-ns")
		if err != nil {
			t.Fatalf("unexpected error: %v", err)
		}
		var parsed nativeConfig
		if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
			t.Fatalf("invalid YAML: %v", err)
		}
		if parsed.FilterChains[0].Filters[0]["config_path"] != "/etc/praxis/policies/my-ns/local-policy.yaml" {
			t.Errorf("config_path: %v", parsed.FilterChains[0].Filters[0]["config_path"])
		}
	})
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
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
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
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
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
	got, err := renderProxyConfig(spec, "default")
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	var parsed nativeConfig
	if err := yaml.Unmarshal([]byte(got), &parsed); err != nil {
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
	_, err := renderProxyConfig(spec, "default")
	if err == nil {
		t.Fatal("expected error for unsupported filter reference")
	}
	if !strings.Contains(err.Error(), "unsupported filter") {
		t.Errorf("unexpected error: %v", err)
	}
}
