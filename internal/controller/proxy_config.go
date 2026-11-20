package controller

import (
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"strconv"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
	"sigs.k8s.io/yaml"
)

const (
	configVolumeName = "praxis-config"
	configMountPath  = "/etc/praxis"
	configFileName   = "config.yaml"
	configSubPath    = configFileName
	policyMountPath  = "/etc/praxis/policies"
	tlsMountPrefix   = "/etc/praxis/tls"
)

type nativeConfig struct {
	Admin           *nativeAdmin     `json:"admin,omitempty"`
	InsecureOptions *nativeInsecure  `json:"insecure_options,omitempty"`
	Listeners       []nativeListener `json:"listeners"`
	FilterChains    []nativeChain    `json:"filter_chains,omitempty"`
}

type nativeAdmin struct {
	Address string `json:"address"`
}

type nativeInsecure struct {
	AllowPrivateEndpoints *bool `json:"allow_private_endpoints,omitempty"`
	AllowUnboundedBody    *bool `json:"allow_unbounded_body,omitempty"`
}

type nativeListener struct {
	Name         string     `json:"name"`
	Address      string     `json:"address"`
	TLS          *nativeTLS `json:"tls,omitempty"`
	FilterChains []string   `json:"filter_chains,omitempty"`
}

type nativeTLS struct {
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

type nativeChain struct {
	Name       string           `json:"name"`
	Conditions []map[string]any `json:"conditions,omitempty"`
	Filters    []map[string]any `json:"filters"`
}

func renderProxyConfig(spec *proxyv1alpha1.ProxySpec, namespace string) (string, error) {
	cfg := nativeConfig{}

	if spec.Admin != nil {
		cfg.Admin = &nativeAdmin{Address: spec.Admin.Address}
	}

	if spec.InsecureOptions != nil {
		cfg.InsecureOptions = &nativeInsecure{
			AllowPrivateEndpoints: spec.InsecureOptions.AllowPrivateEndpoints,
			AllowUnboundedBody:    spec.InsecureOptions.AllowUnboundedBody,
		}
	}

	cfg.Listeners = make([]nativeListener, 0, len(spec.Listeners))
	for _, listener := range spec.Listeners {
		bindAddr := "0.0.0.0"
		if listener.BindAddress != nil {
			bindAddr = *listener.BindAddress
		}
		native := nativeListener{
			Name:         listener.Name,
			Address:      net.JoinHostPort(bindAddr, strconv.FormatInt(int64(listener.Port), 10)),
			FilterChains: listener.FilterChains,
		}
		if listener.TLS != nil {
			native.TLS = &nativeTLS{
				CertPath: fmt.Sprintf("%s/%s/tls.crt", tlsMountPrefix, listener.Name),
				KeyPath:  fmt.Sprintf("%s/%s/tls.key", tlsMountPrefix, listener.Name),
			}
		}
		cfg.Listeners = append(cfg.Listeners, native)
	}

	if len(spec.FilterChains) > 0 {
		cfg.FilterChains = make([]nativeChain, 0, len(spec.FilterChains))
		for _, chain := range spec.FilterChains {
			nc := nativeChain{
				Name:       chain.Name,
				Conditions: renderRequestConditions(chain.Conditions),
			}
			nc.Filters = make([]map[string]any, 0, len(chain.Filters))
			for _, entry := range chain.Filters {
				f, err := renderFilterEntry(&entry, namespace)
				if err != nil {
					return "", fmt.Errorf("chain %q filter %q: %w", chain.Name, entry.Name, err)
				}
				nc.Filters = append(nc.Filters, f)
			}
			cfg.FilterChains = append(cfg.FilterChains, nc)
		}
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return "", fmt.Errorf("marshal config: %w", err)
	}
	return string(out), nil
}

func renderFilterEntry(entry *proxyv1alpha1.PraxisFilterEntry, namespace string) (map[string]any, error) {
	filter := map[string]any{"name": entry.Name}

	switch {
	case entry.InlineFilter != nil:
		filter["filter"] = entry.InlineFilter.Type
		if entry.InlineFilter.Config != nil && entry.InlineFilter.Config.Raw != nil {
			var configMap map[string]any
			if err := json.Unmarshal(entry.InlineFilter.Config.Raw, &configMap); err != nil {
				return nil, fmt.Errorf("unmarshal inline config: %w", err)
			}
			maps.Copy(filter, configMap)
			filter["name"] = entry.Name
			filter["filter"] = entry.InlineFilter.Type
		}
	case isPolicyRef(entry.Ref):
		policyNS := refNamespace(entry.Ref, namespace)
		filter["filter"] = "policy"
		filter["config_path"] = fmt.Sprintf("%s/%s/%s.yaml", policyMountPath, policyNS, entry.Ref.Name)
	default:
		return nil, fmt.Errorf("unsupported filter: neither inline nor policy reference")
	}

	if entry.FailureMode != nil {
		filter["failure_mode"] = string(*entry.FailureMode)
	}
	if conds := renderRequestConditions(entry.Conditions); len(conds) > 0 {
		filter["conditions"] = conds
	}
	if conds := renderResponseConditions(entry.ResponseConditions); len(conds) > 0 {
		filter["response_conditions"] = conds
	}
	return filter, nil
}

func renderRequestConditions(conds []proxyv1alpha1.PraxisRequestCondition) []map[string]any {
	if len(conds) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(conds))
	for _, cond := range conds {
		entry := map[string]any{}
		if cond.When != nil {
			entry["when"] = renderRequestMatch(cond.When)
		}
		if cond.Unless != nil {
			entry["unless"] = renderRequestMatch(cond.Unless)
		}
		out = append(out, entry)
	}
	return out
}

func renderRequestMatch(match *proxyv1alpha1.PraxisRequestMatch) map[string]any {
	out := map[string]any{}
	if match.GRPC != nil {
		out["grpc"] = *match.GRPC
	}
	if match.Path != nil {
		out["path"] = *match.Path
	}
	if match.PathPrefix != nil {
		out["path_prefix"] = *match.PathPrefix
	}
	if len(match.Methods) > 0 {
		out["methods"] = match.Methods
	}
	if len(match.Headers) > 0 {
		out["headers"] = match.Headers
	}
	if len(match.HeadersPresent) > 0 {
		out["headers_present"] = match.HeadersPresent
	}
	if match.BoundUpstream != nil {
		out["bound_upstream"] = renderUpstreamMatch(match.BoundUpstream)
	}
	if match.SelectedUpstream != nil {
		out["selected_upstream"] = renderUpstreamMatch(match.SelectedUpstream)
	}
	return out
}

func renderUpstreamMatch(match *proxyv1alpha1.PraxisSelectedUpstreamMatch) map[string]any {
	out := map[string]any{}
	if match.ApplicationProtocol != nil {
		out["application_protocol"] = *match.ApplicationProtocol
	}
	if match.ApplicationProvider != nil {
		out["application_provider"] = *match.ApplicationProvider
	}
	return out
}

func renderResponseConditions(conds []proxyv1alpha1.PraxisResponseCondition) []map[string]any {
	if len(conds) == 0 {
		return nil
	}
	out := make([]map[string]any, 0, len(conds))
	for _, cond := range conds {
		entry := map[string]any{}
		if cond.When != nil {
			entry["when"] = renderResponseMatch(cond.When)
		}
		if cond.Unless != nil {
			entry["unless"] = renderResponseMatch(cond.Unless)
		}
		out = append(out, entry)
	}
	return out
}

func renderResponseMatch(match *proxyv1alpha1.PraxisResponseMatch) map[string]any {
	out := map[string]any{}
	if len(match.Status) > 0 {
		out["status"] = match.Status
	}
	if len(match.Headers) > 0 {
		out["headers"] = match.Headers
	}
	if len(match.HeadersPresent) > 0 {
		out["headers_present"] = match.HeadersPresent
	}
	return out
}
