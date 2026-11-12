package integration

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
	"github.com/praxis-proxy/gateway-controller/internal/controller"
)

const proxyReconcileTimeout = 5 * time.Second

func validProxy(name string) *proxyv1alpha1.Proxy {
	return &proxyv1alpha1.Proxy{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name + "-",
			Namespace:    "default",
		},
		Spec: proxyv1alpha1.ProxySpec{
			Image: "ghcr.io/praxis-proxy/praxis:latest",
			FilterChains: []proxyv1alpha1.ProxyFilterChain{{
				Name: "main",
				Filters: []proxyv1alpha1.PraxisFilterEntry{{
					Name:         "rl",
					InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
				}},
			}},
			Listeners: []proxyv1alpha1.PraxisListener{{
				Name:         "http",
				Port:         8080,
				FilterChains: []string{"main"},
			}},
		},
	}
}

func acceptedCondition(ctx context.Context, proxy *proxyv1alpha1.Proxy) *metav1.Condition {
	got := &proxyv1alpha1.Proxy{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
	proxy.Status = got.Status
	proxy.ResourceVersion = got.ResourceVersion
	proxy.Generation = got.Generation
	return meta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
}

func createProxy(ctx context.Context, proxy *proxyv1alpha1.Proxy) {
	Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
	DeferCleanup(func(ctx context.Context) error {
		return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
	})
}

var _ = Describe("Proxy Controller", func() {
	expectValidationError := func(ctx context.Context, proxy *proxyv1alpha1.Proxy, substring string) {
		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(cond.Reason).To(Equal("ValidationError"))
			gom.Expect(cond.Message).To(ContainSubstring(substring))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	}

	It("should set Accepted=True for a valid proxy spec", func(ctx context.Context) {
		proxy := validProxy("valid")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(cond.Reason).To(Equal("Valid"))
			gom.Expect(proxy.Status.ObservedGeneration).To(Equal(proxy.Generation))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should reject duplicate entry names within a chain", func(ctx context.Context) {
		proxy := validProxy("dup-entry")
		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
				{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			},
		}}
		proxy.Spec.Listeners[0].FilterChains = nil
		createProxy(ctx, proxy)

		expectValidationError(ctx, proxy, "duplicate entry name")
	})

	It("should reject terminal filter that is not last in chain", func(ctx context.Context) {
		proxy := validProxy("terminal-not-last")
		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "term", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
				{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			},
		}}
		proxy.Spec.Listeners[0].FilterChains = nil
		createProxy(ctx, proxy)

		expectValidationError(ctx, proxy, "terminal filter")
	})

	It("should reject listener referencing undefined chain", func(ctx context.Context) {
		proxy := validProxy("undefined-chain-ref")
		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "real",
			Filters: []proxyv1alpha1.PraxisFilterEntry{{
				Name: "f", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
			}},
		}}
		proxy.Spec.Listeners[0].FilterChains = []string{"real", "ghost"}
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(cond.Reason).To(Equal("ValidationError"))
			gom.Expect(cond.Message).To(ContainSubstring("undefined chain"))
			gom.Expect(cond.Message).To(ContainSubstring("ghost"))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should report multiple validation errors together", func(ctx context.Context) {
		proxy := validProxy("multi-error")
		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
				{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			},
		}}
		proxy.Spec.Listeners[0].FilterChains = []string{"missing"}
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(cond.Reason).To(Equal("ValidationError"))
			gom.Expect(cond.Message).To(ContainSubstring("duplicate entry name"))
			gom.Expect(cond.Message).To(ContainSubstring("undefined chain"))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should transition Accepted from False to True when spec is fixed", func(ctx context.Context) {
		proxy := validProxy("fix-transition")
		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "term", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
				{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			},
		}}
		proxy.Spec.Listeners[0].FilterChains = nil
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		proxy.Spec.FilterChains[0].Filters = []proxyv1alpha1.PraxisFilterEntry{
			{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			{Name: "term", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
		}
		Expect(k8sClient.Update(ctx, proxy)).To(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(proxy.Status.ObservedGeneration).To(Equal(proxy.Generation))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should transition Accepted from True to False when spec becomes invalid", func(ctx context.Context) {
		proxy := validProxy("regress-transition")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "main",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "term", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
				{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			},
		}}
		Expect(k8sClient.Update(ctx, proxy)).To(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(cond.Reason).To(Equal("ValidationError"))
			gom.Expect(proxy.Status.ObservedGeneration).To(Equal(proxy.Generation))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should handle deleted proxy gracefully", func(ctx context.Context) {
		proxy := validProxy("deleted")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			cond := acceptedCondition(ctx, proxy)
			gom.Expect(cond).NotTo(BeNil())
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		Expect(k8sClient.Delete(ctx, proxy)).To(Succeed())

		Eventually(func(ctx context.Context) bool {
			err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), proxy)
			return err != nil
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(BeTrue())

		reconciler := &controller.ProxyReconciler{
			Client: mgrClient,
			Scheme: mgrClient.Scheme(),
		}
		result, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(proxy),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
	})
})
