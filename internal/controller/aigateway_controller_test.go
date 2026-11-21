package controller

import (
	"testing"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

func TestIsTenantReady(t *testing.T) {
	tests := []struct {
		name     string
		tenant   *aigatewayv1alpha1.Tenant
		expected bool
	}{
		{
			name:     "tenant with no conditions",
			tenant:   &aigatewayv1alpha1.Tenant{},
			expected: false,
		},
		{
			name: "tenant with Ready=True",
			tenant: &aigatewayv1alpha1.Tenant{
				Status: aigatewayv1alpha1.TenantStatus{
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionTrue},
					},
				},
			},
			expected: true,
		},
		{
			name: "tenant with Ready=False",
			tenant: &aigatewayv1alpha1.Tenant{
				Status: aigatewayv1alpha1.TenantStatus{
					Conditions: []metav1.Condition{
						{Type: "Ready", Status: metav1.ConditionFalse},
					},
				},
			},
			expected: false,
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			result := isTenantReady(tt.tenant)
			if result != tt.expected {
				t.Errorf("expected %v, got %v", tt.expected, result)
			}
		})
	}
}
