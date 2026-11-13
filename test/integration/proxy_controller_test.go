package integration

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/praxis-proxy/gateway-controller/internal/controller"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

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

var _ = Describe("Proxy Controller", func() {
	var reconciler *controller.ProxyReconciler

	BeforeEach(func() {
		reconciler = &controller.ProxyReconciler{
			Client: mgrClient,
			Scheme: mgrClient.Scheme(),
		}
	})

	reconcileProxy := func(ctx context.Context, proxy *proxyv1alpha1.Proxy) {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(proxy),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), proxy)).To(Succeed())
	}

	createAndReconcile := func(ctx context.Context, proxy *proxyv1alpha1.Proxy) {
		Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
		})
		reconcileProxy(ctx, proxy)
	}

	It("should set Accepted=True for a valid proxy spec", func(ctx context.Context) {
		proxy := validProxy("valid")
		createAndReconcile(ctx, proxy)

		cond := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(cond.Reason).To(Equal("Valid"))
		Expect(proxy.Status.ObservedGeneration).To(Equal(proxy.Generation))
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
		createAndReconcile(ctx, proxy)

		cond := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ValidationError"))
		Expect(cond.Message).To(ContainSubstring("duplicate entry name"))
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
		createAndReconcile(ctx, proxy)

		cond := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ValidationError"))
		Expect(cond.Message).To(ContainSubstring("terminal filter"))
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
		createAndReconcile(ctx, proxy)

		cond := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ValidationError"))
		Expect(cond.Message).To(ContainSubstring("undefined chain"))
		Expect(cond.Message).To(ContainSubstring("ghost"))
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
		createAndReconcile(ctx, proxy)

		cond := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ValidationError"))
		Expect(cond.Message).To(ContainSubstring("duplicate entry name"))
		Expect(cond.Message).To(ContainSubstring("undefined chain"))
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
		createAndReconcile(ctx, proxy)

		cond := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))

		proxy.Spec.FilterChains[0].Filters = []proxyv1alpha1.PraxisFilterEntry{
			{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			{Name: "term", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
		}
		Expect(k8sClient.Update(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)

		reconcileProxy(ctx, proxy)

		cond = meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))
		Expect(proxy.Status.ObservedGeneration).To(Equal(proxy.Generation))
	})

	It("should transition Accepted from True to False when spec becomes invalid", func(ctx context.Context) {
		proxy := validProxy("regress-transition")
		createAndReconcile(ctx, proxy)

		cond := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionTrue))

		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "main",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "term", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "iterative_request_router"}},
				{Name: "after", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			},
		}}
		Expect(k8sClient.Update(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)

		reconcileProxy(ctx, proxy)

		cond = meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(cond).NotTo(BeNil())
		Expect(cond.Status).To(Equal(metav1.ConditionFalse))
		Expect(cond.Reason).To(Equal("ValidationError"))
		Expect(proxy.Status.ObservedGeneration).To(Equal(proxy.Generation))
	})

	It("should handle deleted proxy gracefully", func(ctx context.Context) {
		proxy := validProxy("deleted")
		Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)

		Expect(k8sClient.Delete(ctx, proxy)).To(Succeed())
		Eventually(func() bool {
			err := mgrClient.Get(ctx, client.ObjectKeyFromObject(proxy), proxy)
			return errors.IsNotFound(err)
		}).Should(BeTrue())

		result, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(proxy),
		})
		Expect(err).NotTo(HaveOccurred())
		Expect(result).To(Equal(ctrl.Result{}))
	})
})
