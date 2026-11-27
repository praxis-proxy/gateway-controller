package e2e

import (
	"testing"

	coreclient "k8s.io/client-go/kubernetes/typed/core/v1"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"sigs.k8s.io/controller-runtime/pkg/client"
	ctrlconfig "sigs.k8s.io/controller-runtime/pkg/client/config"

	"github.com/praxis-proxy/gateway-controller/test/utils"
)

const managerNamespace = "gateway-controller-system"

var (
	k8sClient  client.Client
	coreClient coreclient.CoreV1Interface
)

func TestE2E(t *testing.T) {
	// Fail rather than skip when there is no cluster. Both go test and ginkgo
	// report a skipped suite as passing, so skipping makes a run that tested
	// nothing indistinguishable from one that tested everything. In CI, where
	// `make kind-up` runs first, that turns a broken cluster into a green run.
	cfg, err := ctrlconfig.GetConfig()
	if err != nil {
		t.Fatalf("e2e requires a cluster: run `make kind-up` first: %v", err)
	}

	k8sClient, err = client.New(cfg, client.Options{Scheme: utils.NewScheme(t)})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	coreClient, err = coreclient.NewForConfig(cfg)
	if err != nil {
		t.Fatalf("new core client: %v", err)
	}

	RegisterFailHandler(Fail)
	RunSpecs(t, "E2E Suite")
}
