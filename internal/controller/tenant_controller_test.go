package controller

import (
	"testing"
	"time"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

func tenantAt(name string, created time.Time) aigatewayv1alpha1.Tenant {
	return aigatewayv1alpha1.Tenant{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "team-alpha",
			CreationTimestamp: metav1.NewTime(created),
		},
	}
}

func TestResolveNamespaceOwner(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)
	earlier := base.Add(-time.Hour)
	later := base.Add(time.Hour)

	tests := []struct {
		name      string
		tenants   []aigatewayv1alpha1.Tenant
		wantOwner string
	}{
		{
			name:      "no tenants yields no owner",
			tenants:   nil,
			wantOwner: "",
		},
		{
			name:      "a single tenant owns the namespace",
			tenants:   []aigatewayv1alpha1.Tenant{tenantAt("only", base)},
			wantOwner: "only",
		},
		{
			name: "the earlier creation timestamp wins",
			tenants: []aigatewayv1alpha1.Tenant{
				tenantAt("newer", later),
				tenantAt("older", earlier),
			},
			wantOwner: "older",
		},
		{
			name: "an equal timestamp is broken by ascending name",
			tenants: []aigatewayv1alpha1.Tenant{
				tenantAt("zulu", base),
				tenantAt("alpha", base),
			},
			wantOwner: "alpha",
		},
		{
			name: "the earliest of three wins regardless of name",
			tenants: []aigatewayv1alpha1.Tenant{
				tenantAt("alpha", later),
				tenantAt("zulu", earlier),
				tenantAt("mike", base),
			},
			wantOwner: "zulu",
		},
		{
			name: "the same set in a different order yields the same owner",
			tenants: []aigatewayv1alpha1.Tenant{
				tenantAt("mike", base),
				tenantAt("alpha", later),
				tenantAt("zulu", earlier),
			},
			wantOwner: "zulu",
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			owner := resolveNamespaceOwner(tt.tenants)

			if tt.wantOwner == "" {
				if owner != nil {
					t.Fatalf("expected no owner, got %q", owner.Name)
				}
				return
			}
			if owner == nil {
				t.Fatalf("expected owner %q, got none", tt.wantOwner)
			}
			if owner.Name != tt.wantOwner {
				t.Errorf("owner = %q, want %q", owner.Name, tt.wantOwner)
			}
		})
	}
}

func TestTenantsToRequests(t *testing.T) {
	t.Parallel()

	base := time.Date(2026, 11, 16, 12, 0, 0, 0, time.UTC)

	tests := []struct {
		name     string
		tenants  []aigatewayv1alpha1.Tenant
		wantKeys []types.NamespacedName
	}{
		{
			name:     "no tenants produce no requests",
			tenants:  nil,
			wantKeys: nil,
		},
		{
			name:    "one tenant produces one request",
			tenants: []aigatewayv1alpha1.Tenant{tenantAt("only", base)},
			wantKeys: []types.NamespacedName{
				{Name: "only", Namespace: "team-alpha"},
			},
		},
		{
			name: "every tenant produces a request in list order",
			tenants: []aigatewayv1alpha1.Tenant{
				tenantAt("alpha", base),
				tenantAt("zulu", base),
			},
			wantKeys: []types.NamespacedName{
				{Name: "alpha", Namespace: "team-alpha"},
				{Name: "zulu", Namespace: "team-alpha"},
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()

			requests := tenantsToRequests(tt.tenants)

			if len(requests) != len(tt.wantKeys) {
				t.Fatalf("expected %d requests, got %d", len(tt.wantKeys), len(requests))
			}
			for i, want := range tt.wantKeys {
				if requests[i].NamespacedName != want {
					t.Errorf("request[%d] = %v, want %v", i, requests[i].NamespacedName, want)
				}
			}
		})
	}
}
