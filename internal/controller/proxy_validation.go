package controller

import (
	"fmt"
	"net"
	"strings"
	"unicode/utf8"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

const (
	maxConditionMessageLen           = 32768
	iterativeRequestRouterFilterType = "iterative_request_router"
)

var terminalFilterTypes = map[string]bool{
	iterativeRequestRouterFilterType: true,
}

func validateProxySpec(spec *proxyv1alpha1.ProxySpec) []string {
	var errs []string

	chainsByName := make(map[string]*proxyv1alpha1.ProxyFilterChain, len(spec.FilterChains))
	for chainIdx, chain := range spec.FilterChains {
		chainsByName[chain.Name] = &spec.FilterChains[chainIdx]

		entryNames := make(map[string]bool, len(chain.Filters))
		for filterIdx, entry := range chain.Filters {
			if entryNames[entry.Name] {
				errs = append(errs, fmt.Sprintf("spec.filterChains[%d].filters[%d]: duplicate entry name %q in chain %q", chainIdx, filterIdx, entry.Name, chain.Name))
			}
			entryNames[entry.Name] = true

			if entry.InlineFilter != nil && terminalFilterTypes[entry.InlineFilter.Type] && filterIdx != len(chain.Filters)-1 {
				errs = append(errs, fmt.Sprintf("spec.filterChains[%d].filters[%d]: terminal filter %q must be last in chain %q", chainIdx, filterIdx, entry.InlineFilter.Type, chain.Name))
			}
		}
	}

	for listenerIdx, listener := range spec.Listeners {
		if len(listener.FilterChains) == 0 {
			errs = append(errs, fmt.Sprintf("spec.listeners[%d]: listener %q must reference at least one filter chain", listenerIdx, listener.Name))
		}
		// A listener executes its referenced chains as one flattened
		// pipeline, so an entry name contributed by one chain may not
		// reappear in another chain of the same listener. The set is
		// scoped per listener: chains that no single listener references
		// together may reuse names, and a duplicate confined to one
		// chain is already reported by the per-chain check above.
		pipelineEntryNames := make(map[string]string)
		for refIdx, ref := range listener.FilterChains {
			chain, ok := chainsByName[ref]
			if !ok {
				errs = append(errs, fmt.Sprintf("spec.listeners[%d]: listener %q references undefined chain %q", listenerIdx, listener.Name, ref))
				continue
			}
			for _, entry := range chain.Filters {
				if first, ok := pipelineEntryNames[entry.Name]; ok {
					if first != chain.Name {
						errs = append(errs, fmt.Sprintf("spec.listeners[%d]: duplicate entry name %q in listener %q pipeline (chains %q and %q)", listenerIdx, entry.Name, listener.Name, first, chain.Name))
					}
					continue
				}
				pipelineEntryNames[entry.Name] = chain.Name
			}
			if refIdx < len(listener.FilterChains)-1 && len(chain.Filters) > 0 {
				last := chain.Filters[len(chain.Filters)-1]
				if last.InlineFilter != nil && terminalFilterTypes[last.InlineFilter.Type] {
					errs = append(errs, fmt.Sprintf("spec.listeners[%d]: listener %q chain %q ends with terminal filter %q but is followed by another chain", listenerIdx, listener.Name, ref, last.InlineFilter.Type))
				}
			}
		}
	}

	if adminBindRequiresOptIn(spec.Admin, spec.InsecureOptions) {
		errs = append(errs, fmt.Sprintf(
			"spec.admin.address: %q binds the admin interface, which performs no authentication, beyond loopback; set spec.insecureOptions.allowPublicAdmin or bind a loopback address",
			spec.Admin.Address))
	}

	return errs
}

// adminBindRequiresOptIn reports whether the spec binds the admin
// interface beyond loopback without opting in: the Praxis runtime
// rejects non-loopback admin binds at startup without its
// allow_public_admin option, so publishing them would crash-loop the
// runtime. Loopback follows the runtime's rule — 127.0.0.0/8, ::1, and
// IPv4-mapped loopback addresses.
func adminBindRequiresOptIn(admin *proxyv1alpha1.PraxisAdmin, options *proxyv1alpha1.PraxisInsecureOptions) bool {
	if admin == nil {
		return false
	}
	host, _, err := net.SplitHostPort(admin.Address)
	if err != nil {
		return true
	}
	ip := net.ParseIP(host)
	if ip != nil && ip.IsLoopback() {
		return false
	}
	return options == nil || options.AllowPublicAdmin == nil || !*options.AllowPublicAdmin
}

func joinValidationErrors(errs []string) string {
	const sep = "; "
	total := len(errs)
	var builder strings.Builder
	for idx, err := range errs {
		needed := len(err)
		if idx > 0 {
			needed += len(sep)
		}
		remaining := total - idx - 1
		if remaining > 0 {
			suffix := fmt.Sprintf("%s(and %d more)", sep, remaining)
			if builder.Len()+needed+len(suffix) > maxConditionMessageLen {
				avail := maxConditionMessageLen - builder.Len() - len(suffix)
				if idx > 0 {
					avail -= len(sep)
				}
				if avail <= 0 {
					remaining++
					suffix = fmt.Sprintf("%s(and %d more)", sep, remaining)
					avail = maxConditionMessageLen - builder.Len() - len(suffix)
					if idx > 0 {
						avail -= len(sep)
					}
				}
				if avail > 0 {
					if idx > 0 {
						builder.WriteString(sep)
					}
					builder.WriteString(truncateToRuneBoundary(err, avail))
				}
				builder.WriteString(suffix)
				return builder.String()
			}
		} else if builder.Len()+needed > maxConditionMessageLen {
			if builder.Len() > 0 {
				fmt.Fprintf(&builder, "%s(and 1 more)", sep)
			} else {
				builder.WriteString(truncateToRuneBoundary(err, maxConditionMessageLen))
			}
			return builder.String()
		}
		if idx > 0 {
			builder.WriteString(sep)
		}
		builder.WriteString(err)
	}
	return builder.String()
}

func truncateToRuneBoundary(s string, maxBytes int) string {
	if maxBytes >= len(s) {
		return s
	}
	for maxBytes > 0 && !utf8.RuneStart(s[maxBytes]) {
		maxBytes--
	}
	return s[:maxBytes]
}
