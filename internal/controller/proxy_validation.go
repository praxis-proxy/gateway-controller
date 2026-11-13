package controller

import (
	"fmt"
	"strings"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

const maxConditionMessageLen = 32768

var terminalFilterTypes = map[string]bool{
	"iterative_request_router": true,
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
		for refIdx, ref := range listener.FilterChains {
			chain, ok := chainsByName[ref]
			if !ok {
				errs = append(errs, fmt.Sprintf("spec.listeners[%d]: listener %q references undefined chain %q", listenerIdx, listener.Name, ref))
				continue
			}
			if refIdx < len(listener.FilterChains)-1 && len(chain.Filters) > 0 {
				last := chain.Filters[len(chain.Filters)-1]
				if last.InlineFilter != nil && terminalFilterTypes[last.InlineFilter.Type] {
					errs = append(errs, fmt.Sprintf("spec.listeners[%d]: listener %q chain %q ends with terminal filter %q but is followed by another chain", listenerIdx, listener.Name, ref, last.InlineFilter.Type))
				}
			}
		}
	}

	return errs
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
				builder.WriteString(suffix)
				return builder.String()
			}
		} else if builder.Len()+needed > maxConditionMessageLen {
			if builder.Len() > 0 {
				fmt.Fprintf(&builder, "%s(and 1 more)", sep)
			} else {
				builder.WriteString(err[:maxConditionMessageLen])
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
