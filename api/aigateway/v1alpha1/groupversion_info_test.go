package v1alpha1

import (
	"testing"

	"k8s.io/apimachinery/pkg/runtime"
)

func TestAddToScheme(t *testing.T) {
	tests := []struct {
		name string
		kind string
	}{
		{name: "aigateway", kind: "AIGateway"},
		{name: "aigateway list", kind: "AIGatewayList"},
	}

	scheme := runtime.NewScheme()
	if err := AddToScheme(scheme); err != nil {
		t.Fatalf("AddToScheme: %v", err)
	}

	known := scheme.KnownTypes(SchemeGroupVersion)

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if _, ok := known[tt.kind]; !ok {
				t.Errorf("kind %q is not registered in %s", tt.kind, SchemeGroupVersion)
			}
		})
	}
}
