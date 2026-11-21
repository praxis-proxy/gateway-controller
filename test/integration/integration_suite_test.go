package integration

import (
	"context"
	"testing"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/client-go/rest"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	metricsserver "sigs.k8s.io/controller-runtime/pkg/metrics/server"

	"github.com/praxis-proxy/gateway-controller/internal/controller"
	"github.com/praxis-proxy/gateway-controller/test/utils"
)

var (
	k8sClient client.Client
	mgrClient client.Client
)

func TestIntegration(t *testing.T) {
	RegisterFailHandler(Fail)

	var cfg *rest.Config
	k8sClient, cfg = utils.StartEnvtest(t)

	mgr, err := ctrl.NewManager(cfg, ctrl.Options{
		Scheme:  utils.NewScheme(t),
		Metrics: metricsserver.Options{BindAddress: "0"},
	})
	if err != nil {
		t.Fatalf("create manager: %v", err)
	}

	ctx, cancel := context.WithCancel(context.Background())
	t.Cleanup(cancel)

	if err := controller.SetupIndexes(ctx, mgr.GetFieldIndexer()); err != nil {
		t.Fatalf("setup indexes: %v", err)
	}

	if err := (&controller.ProxyReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		t.Fatalf("setup proxy reconciler: %v", err)
	}

	if err := (&controller.TenantReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		t.Fatalf("setup tenant reconciler: %v", err)
	}

	if err := (&controller.AIGatewayReconciler{
		Client: mgr.GetClient(),
		Scheme: mgr.GetScheme(),
	}).SetupWithManager(mgr); err != nil {
		t.Fatalf("setup aigateway reconciler: %v", err)
	}

	go func() {
		if err := mgr.Start(ctx); err != nil && ctx.Err() == nil {
			t.Errorf("start manager: %v", err)
		}
	}()

	if !mgr.GetCache().WaitForCacheSync(ctx) {
		t.Fatal("cache sync failed")
	}

	mgrClient = mgr.GetClient()

	RunSpecs(t, "Integration Suite")
}
