package controller

import (
	"strings"
	"testing"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

func TestValidateProxySpec(t *testing.T) {
	t.Parallel()

	tests := []struct {
		name     string
		spec     proxyv1alpha1.ProxySpec
		wantErrs int
		wantMsg  string
	}{
		{
			name: "valid spec passes",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain-a",
					Filters: []proxyv1alpha1.PraxisFilterEntry{{
						Name:         "inline",
						InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
					}},
				}},
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name:         "http",
					Port:         8080,
					FilterChains: []string{"chain-a"},
				}},
			},
			wantErrs: 0,
		},
		{
			name: "duplicate entry names within a chain",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
						{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
					},
				}},
				Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
			},
			wantErrs: 1,
			wantMsg:  "duplicate entry name",
		},
		{
			name: "non-terminal filter type not last is valid",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "sr", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "static_response"}},
						{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
					},
				}},
				Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
			},
			wantErrs: 0,
		},
		{
			name: "terminal iterative_request_router not last",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "router", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
						{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
					},
				}},
				Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
			},
			wantErrs: 1,
			wantMsg:  "terminal filter \"iterative_request_router\"",
		},
		{
			name: "terminal filter last is valid",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "first", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
						{Name: "term", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
					},
				}},
				Listeners: []proxyv1alpha1.PraxisListener{{Name: "http", Port: 8080}},
			},
			wantErrs: 0,
		},
		{
			name: "listener references undefined chain",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "real",
					Filters: []proxyv1alpha1.PraxisFilterEntry{{
						Name: "f", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
					}},
				}},
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name:         "http",
					Port:         8080,
					FilterChains: []string{"real", "ghost"},
				}},
			},
			wantErrs: 1,
			wantMsg:  "undefined chain",
		},
		{
			name: "terminal chain followed by another chain in listener",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{
					{Name: "auth", Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "router", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
					}},
					{Name: "app", Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "rl", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
					}},
				},
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name:         "http",
					Port:         8080,
					FilterChains: []string{"auth", "app"},
				}},
			},
			wantErrs: 1,
			wantMsg:  "ends with terminal filter",
		},
		{
			name: "terminal chain last in listener is valid",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{
					{Name: "app", Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "rl", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
					}},
					{Name: "fallback", Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "router", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
					}},
				},
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name:         "http",
					Port:         8080,
					FilterChains: []string{"app", "fallback"},
				}},
			},
			wantErrs: 0,
		},
		{
			name: "multiple errors reported together",
			spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
						{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
					},
				}},
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name:         "http",
					Port:         8080,
					FilterChains: []string{"missing"},
				}},
			},
			wantErrs: 2,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			errs := validateProxySpec(&tt.spec)
			if len(errs) != tt.wantErrs {
				t.Fatalf("expected %d errors, got %d: %v", tt.wantErrs, len(errs), errs)
			}
			if tt.wantMsg != "" {
				joined := joinValidationErrors(errs)
				if !strings.Contains(joined, tt.wantMsg) {
					t.Errorf("expected error containing %q, got %q", tt.wantMsg, joined)
				}
			}
		})
	}
}

func TestJoinValidationErrors(t *testing.T) {
	t.Parallel()

	t.Run("short messages joined fully", func(t *testing.T) {
		t.Parallel()
		errs := []string{"error one", "error two"}
		got := joinValidationErrors(errs)
		if got != "error one; error two" {
			t.Errorf("unexpected result: %q", got)
		}
	})

	t.Run("long messages truncated with count", func(t *testing.T) {
		t.Parallel()
		longErr := strings.Repeat("x", maxConditionMessageLen)
		errs := []string{longErr, "second", "third"}
		got := joinValidationErrors(errs)
		if len(got) > maxConditionMessageLen {
			t.Errorf("message length %d exceeds limit %d", len(got), maxConditionMessageLen)
		}
		if !strings.Contains(got, "(and 2 more)") {
			t.Errorf("expected omission suffix, got %q", got[len(got)-50:])
		}
	})

	t.Run("single error exceeding limit is truncated", func(t *testing.T) {
		t.Parallel()
		longErr := strings.Repeat("y", maxConditionMessageLen+100)
		errs := []string{longErr}
		got := joinValidationErrors(errs)
		if len(got) != maxConditionMessageLen {
			t.Errorf("expected length %d, got %d", maxConditionMessageLen, len(got))
		}
	})
}
