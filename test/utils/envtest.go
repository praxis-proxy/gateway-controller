// Package utils provides shared test utilities for the integration and e2e
// suites.
package utils

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"

	k8sruntime "k8s.io/apimachinery/pkg/runtime"
	clientgoscheme "k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/envtest"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

// findRepoRoot walks up the directory tree from the working directory to find
// the repository root (directory containing go.mod). go test sets the working
// directory to the package under test, so this holds wherever the suite is run
// from. It starts from the working directory rather than runtime.Caller because
// the latter reports a compile-time path that -trimpath rewrites to one that
// does not exist on disk.
func findRepoRoot() (string, error) {
	start, err := os.Getwd()
	if err != nil {
		return "", fmt.Errorf("get working directory: %w", err)
	}

	dir := start
	for {
		if _, err := os.Stat(filepath.Join(dir, "go.mod")); err == nil {
			return dir, nil
		}
		parent := filepath.Dir(dir)
		if parent == dir {
			return "", fmt.Errorf("no go.mod in %s or any parent", start)
		}
		dir = parent
	}
}

// CRDDirectoryPaths locates the generated CRDs relative to the repository root.
// It fails the test rather than returning a relative path, which envtest would
// otherwise report as a missing CRD directory without naming the cause.
func CRDDirectoryPaths(tb testing.TB) []string {
	tb.Helper()

	repoRoot, err := findRepoRoot()
	if err != nil {
		tb.Fatalf("locate repository root: %v", err)
	}

	return []string{filepath.Join(repoRoot, "config", "crd", "bases")}
}

// NewScheme returns a scheme with the Kubernetes built-ins and both project
// API groups registered.
func NewScheme(tb testing.TB) *k8sruntime.Scheme {
	tb.Helper()

	scheme := k8sruntime.NewScheme()
	for _, add := range []func(*k8sruntime.Scheme) error{
		clientgoscheme.AddToScheme,
		proxyv1alpha1.AddToScheme,
		aigatewayv1alpha1.AddToScheme,
	} {
		if err := add(scheme); err != nil {
			tb.Fatalf("build scheme: %v", err)
		}
	}

	return scheme
}

// StartEnvtest brings up a control plane with the project CRDs installed and
// returns a client backed by the project scheme. The environment is stopped
// when the test finishes.
func StartEnvtest(tb testing.TB) (client.Client, *rest.Config) {
	tb.Helper()

	env := &envtest.Environment{
		CRDDirectoryPaths:     CRDDirectoryPaths(tb),
		ErrorIfCRDPathMissing: true,
	}

	// Registered before Start, because Start brings the control plane up and
	// then installs the CRDs: a failure in that second phase returns an error
	// with etcd and the apiserver still running.
	tb.Cleanup(func() {
		if err := env.Stop(); err != nil {
			tb.Errorf("stop envtest: %v", err)
		}
	})

	cfg, err := env.Start()
	if err != nil {
		tb.Fatalf("start envtest: %v", err)
	}

	c, err := client.New(cfg, client.Options{Scheme: NewScheme(tb)})
	if err != nil {
		tb.Fatalf("new client: %v", err)
	}

	return c, cfg
}
