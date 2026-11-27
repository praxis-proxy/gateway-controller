package controller

import (
	"context"
	"encoding/json"
	"fmt"
	"maps"
	"net"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
	"sigs.k8s.io/yaml"
)

const (
	configVolumeName   = "praxis-config"
	configMountPath    = "/etc/praxis/config"
	configFileName     = "config.yaml"
	defaultBindAddress = "0.0.0.0"
	policyFilterType   = "policy"
	policyMountPath    = "/etc/praxis/policies"
	tlsMountPrefix     = "/etc/praxis/tls"
)

const (
	readinessListenerBase = "readiness"
	defaultReadinessPort  = 19902
	// readinessFilterType is the single filter of the readiness
	// listener's health-only chain. The Praxis configuration reference
	// documents static_response for exactly this use — "useful for
	// health checks" — answering with a fixed response without
	// contacting any upstream, so the chain cannot route application
	// traffic.
	readinessFilterType = "static_response"
	// readinessResponseStatus is the fixed status the health-only chain
	// answers with.
	readinessResponseStatus = 200
	// dualStackWildcard is the address the controller-rendered readiness
	// listener binds on IPv6-primary pod networks, and when the pod
	// network's primary family is unknown: the IPv6 wildcard, the only
	// listener address that accepts connections on the pod IP's family
	// whichever it is — Linux dual-stack sockets accept IPv4 connections
	// on it through IPv4-mapped addresses. Binding it requires IPv6 in
	// the pod netns, so an IPv6-disabled netns or IPv6-only binding
	// (net.ipv6.bindv6only) cannot serve it; IPv4-primary networks bind
	// the IPv4 wildcard instead (readinessWildcard).
	dualStackWildcard = "::"
)

// clusterPrimaryIPFamily returns the pod network's primary IP family,
// read from the default/kubernetes Service the API server allocates in
// the cluster's primary family — the same family the pod network
// allocates pod IPs in, so it is the family of the pod IP the kubelet
// dials for probes. The empty value means unknown: the Service is
// absent or reports no families, leaving callers to family-agnostic
// behavior. Any error other than absence is returned so a transient
// read fails the reconcile instead of silently flipping the rendered
// configuration.
func clusterPrimaryIPFamily(ctx context.Context, reader client.Reader) (corev1.IPFamily, error) {
	kubernetes := &corev1.Service{}
	err := reader.Get(ctx, types.NamespacedName{Namespace: "default", Name: "kubernetes"}, kubernetes)
	if apierrors.IsNotFound(err) {
		return "", nil
	}
	if err != nil {
		return "", fmt.Errorf("get default/kubernetes service: %w", err)
	}
	if len(kubernetes.Spec.IPFamilies) == 0 {
		return "", nil
	}
	return kubernetes.Spec.IPFamilies[0], nil
}

// podIPBind reports whether a listener bound to bindAddress accepts
// connections on the pod IP the kubelet dials, whose family is the pod
// network's primary family. The wildcard of that family answers, and
// so does the IPv6 wildcard on an IPv4-primary network, through the
// IPv4-mapped addresses Linux dual-stack sockets accept. When the
// family is unknown only the IPv6 wildcard qualifies: it answers dials
// of either family, while the IPv4 wildcard would never answer a dial
// to an IPv6 pod IP. A nil bind renders as the IPv4 wildcard.
func podIPBind(bindAddress *string, primary corev1.IPFamily) bool {
	if bindAddress == nil {
		return primary == corev1.IPv4Protocol
	}
	ip := net.ParseIP(*bindAddress)
	if ip == nil || !ip.IsUnspecified() {
		return false
	}
	if ip.To4() != nil {
		return primary == corev1.IPv4Protocol
	}
	return true
}

// podIPListener returns the first spec listener the readiness probe can
// reach through the pod IP, or nil when none can: only wildcard binds
// answer the kubelet's dial on the pod network's primary family (see
// podIPBind).
func podIPListener(spec *proxyv1alpha1.ProxySpec, primary corev1.IPFamily) *proxyv1alpha1.PraxisListener {
	for i := range spec.Listeners {
		if podIPBind(spec.Listeners[i].BindAddress, primary) {
			return &spec.Listeners[i]
		}
	}
	return nil
}

// readinessListenerName returns the name of the controller-rendered
// readiness listener, avoiding the spec listener names so the rendered
// config keeps unique listener names.
func readinessListenerName(spec *proxyv1alpha1.ProxySpec) string {
	taken := make(map[string]bool, len(spec.Listeners))
	for _, listener := range spec.Listeners {
		taken[listener.Name] = true
	}
	return uniqueName(readinessListenerBase, taken)
}

// readinessChainName returns the name of the readiness listener's
// health-only filter chain, avoiding the spec chain names so the chain
// reference resolves to the controller-rendered chain alone.
func readinessChainName(spec *proxyv1alpha1.ProxySpec) string {
	taken := make(map[string]bool, len(spec.FilterChains))
	for _, chain := range spec.FilterChains {
		taken[chain.Name] = true
	}
	return uniqueName(readinessListenerBase, taken)
}

// uniqueName returns base, or base-<n> for the smallest suffix not
// already taken.
func uniqueName(base string, taken map[string]bool) string {
	name := base
	for suffix := 0; taken[name]; suffix++ {
		name = fmt.Sprintf("%s-%d", base, suffix)
	}
	return name
}

// readinessPort returns the port of the controller-rendered readiness
// listener: the default readiness port, or the next free port when a spec
// listener or the admin interface already binds it.
func readinessPort(spec *proxyv1alpha1.ProxySpec) int32 {
	used := make(map[int32]bool, len(spec.Listeners))
	for _, listener := range spec.Listeners {
		used[listener.Port] = true
	}
	if spec.Admin != nil {
		if _, port, err := net.SplitHostPort(spec.Admin.Address); err == nil {
			if parsed, err := strconv.ParseInt(port, 10, 32); err == nil {
				used[int32(parsed)] = true
			}
		}
	}
	port := int32(defaultReadinessPort)
	for used[port] {
		port++
	}
	return port
}

// renderListenerTLS returns the native TLS config for a listener's
// certificate, which the controller's Deployment mounts under the
// listener's TLS directory. The Praxis release pinned in
// config/samples/proxy/proxy.yaml loads certificates as a list — a
// flat cert_path/key_path pair on the tls block is rejected at startup
// ("unknown field `cert_path`, expected one of `certificates`,
// cipher_suites, client_ca, client_cert_mode, trusted_spiffe_ids,
// hot_reload, min_version") — so the mounted pair renders as the
// single entry of the certificates list. Verified against that
// release with a live, CA-verified TLS request through a listener
// configured with this form.
func renderListenerTLS(listener *proxyv1alpha1.PraxisListener) *nativeTLS {
	if listener.TLS == nil {
		return nil
	}
	return &nativeTLS{
		Certificates: []nativeTLSCertificate{{
			CertPath: fmt.Sprintf("%s/%s/tls.crt", tlsMountPrefix, listener.Name),
			KeyPath:  fmt.Sprintf("%s/%s/tls.key", tlsMountPrefix, listener.Name),
		}},
	}
}

// readinessFilterChain returns the health-only filter chain the
// readiness listener executes. The Praxis validator rejects chainless
// HTTP listeners ("at least one filter chain required for HTTP
// listeners"; filter_chains may only be omitted on protocol: tcp
// listeners, which must instead route via upstream or cluster), so the
// readiness listener carries this dedicated chain. Verified against
// the release pinned in config/samples/proxy/proxy.yaml, which accepts
// the rendered form and rejects the chainless one. Its single
// static_response filter is the form the Praxis configuration reference
// documents for health checks: it answers with a fixed response without
// contacting any upstream, so no application traffic can be routed
// through it.
func readinessFilterChain(spec *proxyv1alpha1.ProxySpec) nativeChain {
	return nativeChain{
		Name: readinessChainName(spec),
		Filters: []map[string]any{{
			"filter": readinessFilterType,
			"status": readinessResponseStatus,
		}},
	}
}

// readinessWildcard is the address the controller-rendered readiness
// listener binds: the wildcard of the pod network's primary family,
// which the kubelet's dial to the pod IP always reaches. The IPv6
// wildcard also answers IPv4 dials through IPv4-mapped addresses, so it
// is kept when the family is unknown — but binding it needs IPv6
// available in the pod netns, which an IPv4-primary network cannot
// promise, so that network binds the IPv4 wildcard instead.
func readinessWildcard(primary corev1.IPFamily) string {
	if primary == corev1.IPv4Protocol {
		return defaultBindAddress
	}
	return dualStackWildcard
}

// renderReadinessListener returns the listener the controller appends to
// the rendered config when no spec listener is reachable through the pod
// IP: a wildcard-bound listener dedicated to the readiness probe,
// executing only the health-only static_response chain. It
// mirrors the first listener's TLS material — already mounted for that
// listener — so a TLS-enabled proxy keeps no plaintext port. A TCP probe
// still succeeds against a TLS listener, which accepts the connection
// before any handshake.
func renderReadinessListener(spec *proxyv1alpha1.ProxySpec, primary corev1.IPFamily) nativeListener {
	var listenerTLS *nativeTLS
	if len(spec.Listeners) > 0 {
		listenerTLS = renderListenerTLS(&spec.Listeners[0])
	}
	return nativeListener{
		Name:         readinessListenerName(spec),
		Address:      net.JoinHostPort(readinessWildcard(primary), strconv.FormatInt(int64(readinessPort(spec)), 10)),
		FilterChains: []string{readinessChainName(spec)},
		TLS:          listenerTLS,
	}
}

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
	AllowPublicAdmin      *bool `json:"allow_public_admin,omitempty"`
}

type nativeListener struct {
	Name         string     `json:"name"`
	Address      string     `json:"address"`
	TLS          *nativeTLS `json:"tls,omitempty"`
	FilterChains []string   `json:"filter_chains,omitempty"`
}

type nativeTLS struct {
	Certificates []nativeTLSCertificate `json:"certificates"`
}

// nativeTLSCertificate is one entry of a listener's certificates list:
// the certificate chain and private key the runtime loads, mounted by
// the controller's Deployment from the referenced Secret.
type nativeTLSCertificate struct {
	CertPath string `json:"cert_path"`
	KeyPath  string `json:"key_path"`
}

type nativeChain struct {
	Name       string           `json:"name"`
	Conditions []map[string]any `json:"conditions,omitempty"`
	Filters    []map[string]any `json:"filters"`
}

// renderedConfig is the result of rendering a Proxy spec into the
// Praxis-native configuration.
type renderedConfig struct {
	// Config is the rendered Praxis configuration YAML.
	Config string

	// PolicyDocuments holds the document of every Policy the rendered
	// configuration references, keyed by the file path the rendered
	// policy filter reads it from. The controller collects the documents
	// while rendering; publishing them next to the proxy and mounting
	// them into the Praxis container are owned by separate tasks
	// (#19 and #20), which build on this collection.
	PolicyDocuments map[string]string
}

func renderProxyConfig(ctx context.Context, reader client.Reader, spec *proxyv1alpha1.ProxySpec, namespace string, primary corev1.IPFamily) (*renderedConfig, error) {
	cfg := nativeConfig{}
	rendered := &renderedConfig{PolicyDocuments: map[string]string{}}

	if spec.Admin != nil {
		cfg.Admin = &nativeAdmin{Address: spec.Admin.Address}
	}

	if spec.InsecureOptions != nil {
		cfg.InsecureOptions = &nativeInsecure{
			AllowPrivateEndpoints: spec.InsecureOptions.AllowPrivateEndpoints,
			AllowUnboundedBody:    spec.InsecureOptions.AllowUnboundedBody,
			AllowPublicAdmin:      spec.InsecureOptions.AllowPublicAdmin,
		}
	}

	cfg.Listeners = make([]nativeListener, 0, len(spec.Listeners))
	for _, listener := range spec.Listeners {
		bindAddr := defaultBindAddress
		if listener.BindAddress != nil {
			bindAddr = *listener.BindAddress
		}
		native := nativeListener{
			Name:         listener.Name,
			Address:      net.JoinHostPort(bindAddr, strconv.FormatInt(int64(listener.Port), 10)),
			FilterChains: listener.FilterChains,
			TLS:          renderListenerTLS(&listener),
		}
		cfg.Listeners = append(cfg.Listeners, native)
	}

	// No spec listener accepts connections on the pod IP — every
	// listener binds a specific address or a wildcard of a family the
	// pod IP does not have — so the readiness probe needs the dedicated
	// wildcard listener to target. It executes only the health-only
	// static_response chain, so it accepts connections purely for the
	// probe and cannot route application traffic.
	renderReadiness := podIPListener(spec, primary) == nil
	if renderReadiness {
		cfg.Listeners = append(cfg.Listeners, renderReadinessListener(spec, primary))
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
				f, err := renderFilterEntry(ctx, reader, &entry, namespace, rendered.PolicyDocuments)
				if err != nil {
					return nil, fmt.Errorf("chain %q filter %q: %w", chain.Name, entry.Name, err)
				}
				nc.Filters = append(nc.Filters, f)
			}
			cfg.FilterChains = append(cfg.FilterChains, nc)
		}
	}

	// The readiness listener's chain is rendered next to the spec chains
	// so its named reference resolves.
	if renderReadiness {
		cfg.FilterChains = append(cfg.FilterChains, readinessFilterChain(spec))
	}

	out, err := yaml.Marshal(cfg)
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	rendered.Config = string(out)
	return rendered, nil
}

// reservedFilterConfigKeys are the rendered filter entry keys the
// controller owns: the entry's identity and wiring, set from the typed
// API fields (name, filter, failure_mode, conditions,
// response_conditions) or the policy reference (config_path). The
// inline Config is copied over the rendered entry, so a key set there
// would override or impersonate the controller-rendered field; inline
// configs setting one are rejected, while ordinary filter
// configuration keys keep flattening.
var reservedFilterConfigKeys = []string{
	"conditions",
	"config_path",
	"failure_mode",
	"filter",
	"name",
	"response_conditions",
}

// rejectReservedFilterConfigKeys returns an error naming every
// controller-owned key the inline filter configuration sets.
func rejectReservedFilterConfigKeys(entry *proxyv1alpha1.PraxisFilterEntry, config map[string]any) error {
	var reserved []string
	for _, key := range reservedFilterConfigKeys {
		if _, ok := config[key]; ok {
			reserved = append(reserved, strconv.Quote(key))
		}
	}
	if len(reserved) == 0 {
		return nil
	}
	return fmt.Errorf("filter %q: inline config sets reserved keys %s, which the controller renders from the filter entry's fields",
		entry.Name, strings.Join(reserved, ", "))
}

func renderFilterEntry(ctx context.Context, reader client.Reader, entry *proxyv1alpha1.PraxisFilterEntry, namespace string, policyDocuments map[string]string) (map[string]any, error) {
	filter := map[string]any{"name": entry.Name}

	switch {
	case entry.InlineFilter != nil:
		filter["filter"] = entry.InlineFilter.Type
		if entry.InlineFilter.Config != nil && entry.InlineFilter.Config.Raw != nil {
			var configMap map[string]any
			if err := json.Unmarshal(entry.InlineFilter.Config.Raw, &configMap); err != nil {
				return nil, fmt.Errorf("unmarshal inline config: %w", err)
			}
			if err := rejectReservedFilterConfigKeys(entry, configMap); err != nil {
				return nil, err
			}
			maps.Copy(filter, configMap)
		}
	case isPolicyRef(entry.Ref):
		policy, err := resolvePolicy(ctx, reader, entry.Ref, namespace)
		if err != nil {
			return nil, err
		}
		path := policyConfigPath(refNamespace(entry.Ref, namespace), entry.Ref.Name)
		filter["filter"] = policyFilterType
		filter["config_path"] = path
		applyPolicyFilterConfig(filter, policy.Spec.Filter)
		policyDocuments[path] = policy.Spec.Document
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

// resolvePolicy fetches the Policy a filter entry references. The
// reference's namespace defaults to the proxy's own namespace.
func resolvePolicy(ctx context.Context, reader client.Reader, ref *proxyv1alpha1.PraxisFilterReference, namespace string) (*proxyv1alpha1.Policy, error) {
	policyNS := refNamespace(ref, namespace)
	policy := &proxyv1alpha1.Policy{}
	if err := reader.Get(ctx, types.NamespacedName{Namespace: policyNS, Name: ref.Name}, policy); err != nil {
		return nil, fmt.Errorf("resolve policy %s/%s: %w", policyNS, ref.Name, err)
	}
	return policy, nil
}

// policyConfigPath returns the file path the rendered policy filter reads
// its document from, and the key the collected document is stored under.
func policyConfigPath(namespace, name string) string {
	return fmt.Sprintf("%s/%s/%s.yaml", policyMountPath, namespace, name)
}

// applyPolicyFilterConfig copies the Policy's typed filter options into
// the rendered filter entry. Praxis filter configs are flat: the filter's
// typed fields sit directly under the filter entry alongside the
// structural keys (name, config_path), so each set field is copied under
// its native name and unset fields are omitted.
func applyPolicyFilterConfig(filter map[string]any, config *proxyv1alpha1.PolicyFilterConfig) {
	if config == nil {
		return
	}
	if config.BodyAccess != nil {
		filter["body_access"] = string(*config.BodyAccess)
	}
	if config.InitTimeoutSeconds != nil {
		filter["init_timeout_secs"] = *config.InitTimeoutSeconds
	}
	if config.MaxBufferBytes != nil {
		filter["max_buffer_bytes"] = *config.MaxBufferBytes
	}
	if config.AllowPrivateIdp != nil {
		filter["allow_private_idp"] = *config.AllowPrivateIdp
	}
	if len(config.TrustedPrivateEndpoints) > 0 {
		filter["trusted_private_endpoints"] = config.TrustedPrivateEndpoints
	}
	if config.RequireProtocolMetadata != nil {
		filter["require_protocol_metadata"] = *config.RequireProtocolMetadata
	}
	if config.LLM != nil {
		llm := map[string]any{}
		if config.LLM.MaxRequestBytes != nil {
			llm["max_request_bytes"] = *config.LLM.MaxRequestBytes
		}
		if config.LLM.PromoteParams != nil {
			llm["promote_params"] = config.LLM.PromoteParams
		}
		if config.LLM.Provider != nil {
			llm["provider"] = *config.LLM.Provider
		}
		if config.LLM.RequireModel != nil {
			llm["require_model"] = *config.LLM.RequireModel
		}
		if config.LLM.RequireRoute != nil {
			llm["require_route"] = *config.LLM.RequireRoute
		}
		filter["llm"] = llm
	}
	if len(config.ContextHeaders) > 0 {
		filter["context_headers"] = config.ContextHeaders
	}
	if config.ContextMeta != nil {
		meta := map[string]any{}
		if len(config.ContextMeta.Tags) > 0 {
			meta["tags"] = config.ContextMeta.Tags
		}
		if config.ContextMeta.Scope != nil {
			meta["scope"] = *config.ContextMeta.Scope
		}
		if len(config.ContextMeta.Properties) > 0 {
			meta["properties"] = config.ContextMeta.Properties
		}
		filter["context_meta"] = meta
	}
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

// computeStartupRevision hashes the settings that require a Praxis restart.
// Filter contents reload from the live ConfigMap; scaling and Service settings
// do not affect the running process configuration.
func computeStartupRevision(spec *proxyv1alpha1.ProxySpec) (string, error) {
	return computeRevision(spec.Listeners, spec.Admin, spec.InsecureOptions, spec.Image, spec.SecretEnv)
}
