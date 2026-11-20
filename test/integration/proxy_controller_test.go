package integration

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
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
		proxy.Spec.Listeners[0].FilterChains = []string{"chain"}
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
		proxy.Spec.Listeners[0].FilterChains = []string{"chain"}
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
		proxy.Spec.Listeners[0].FilterChains = []string{"chain"}
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

	It("should create a ConfigMap with rendered config for a valid proxy", func(ctx context.Context) {
		proxy := validProxy("cm-create")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			cm := &corev1.ConfigMap{}
			gom.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      proxy.Name + "-config",
				Namespace: proxy.Namespace,
			}, cm)).To(Succeed())
			gom.Expect(cm.Data).To(HaveKey("config.yaml"))
			gom.Expect(cm.Data["config.yaml"]).To(ContainSubstring("listeners"))
			gom.Expect(cm.OwnerReferences).To(HaveLen(1))
			gom.Expect(cm.OwnerReferences[0].Name).To(Equal(proxy.Name))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should set Programmed condition and revision for a valid proxy", func(ctx context.Context) {
		proxy := validProxy("programmed")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			got := &proxyv1alpha1.Proxy{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
			cond := meta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeProgrammed)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(cond.Reason).To(Equal("ConfigPublished"))
			gom.Expect(got.Status.DesiredRevision).NotTo(BeEmpty())
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should set ResolvedRefs and populate listener status", func(ctx context.Context) {
		proxy := validProxy("resolved")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			got := &proxyv1alpha1.Proxy{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
			cond := meta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(got.Status.Listeners).To(HaveLen(1))
			gom.Expect(got.Status.Listeners[0].Name).To(Equal("http"))
			gom.Expect(got.Status.Listeners[0].ChainOrder).To(Equal([]string{"main"}))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should not create ConfigMap for invalid proxy", func(ctx context.Context) {
		proxy := validProxy("cm-no-create")
		proxy.Spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
			Name: "chain",
			Filters: []proxyv1alpha1.PraxisFilterEntry{
				{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
				{Name: "dup", InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"}},
			},
		}}
		proxy.Spec.Listeners[0].FilterChains = []string{"chain"}
		createProxy(ctx, proxy)

		expectValidationError(ctx, proxy, "duplicate entry name")

		cm := &corev1.ConfigMap{}
		err := k8sClient.Get(ctx, types.NamespacedName{
			Name:      proxy.Name + "-config",
			Namespace: proxy.Namespace,
		}, cm)
		Expect(err).To(HaveOccurred())
	})

	It("should create a Deployment with correct container spec", func(ctx context.Context) {
		proxy := validProxy("deploy")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			deploy := &appsv1.Deployment{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), deploy)).To(Succeed())
			gom.Expect(deploy.OwnerReferences).To(HaveLen(1))
			gom.Expect(deploy.OwnerReferences[0].Name).To(Equal(proxy.Name))
			gom.Expect(*deploy.Spec.Replicas).To(Equal(int32(1)))
			containers := deploy.Spec.Template.Spec.Containers
			gom.Expect(containers).To(HaveLen(1))
			gom.Expect(containers[0].Image).To(Equal(proxy.Spec.Image))
			gom.Expect(containers[0].Ports).To(HaveLen(1))
			gom.Expect(containers[0].Ports[0].ContainerPort).To(Equal(int32(8080)))
			gom.Expect(deploy.Spec.Template.Annotations).To(HaveKey("praxis.io/config-revision"))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should create a Service exposing listener ports", func(ctx context.Context) {
		proxy := validProxy("svc")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			svc := &corev1.Service{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), svc)).To(Succeed())
			gom.Expect(svc.OwnerReferences).To(HaveLen(1))
			gom.Expect(svc.OwnerReferences[0].Name).To(Equal(proxy.Name))
			gom.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
			gom.Expect(svc.Spec.Ports).To(HaveLen(1))
			gom.Expect(svc.Spec.Ports[0].Port).To(Equal(int32(8080)))
			gom.Expect(svc.Spec.Ports[0].Name).To(Equal("http"))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should set Ready=False when deployment has no ready replicas", func(ctx context.Context) {
		proxy := validProxy("ready-false")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			got := &proxyv1alpha1.Proxy{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
			cond := meta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeReady)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(cond.Reason).To(Equal("NoReadyReplicas"))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should update ConfigMap and Deployment revision when filter changes", func(ctx context.Context) {
		proxy := validProxy("update-filter")
		createProxy(ctx, proxy)

		var origRevision string
		Eventually(func(gom Gomega, ctx context.Context) {
			deploy := &appsv1.Deployment{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), deploy)).To(Succeed())
			origRevision = deploy.Spec.Template.Annotations["praxis.io/config-revision"]
			gom.Expect(origRevision).NotTo(BeEmpty())
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		Eventually(func(ctx context.Context) error {
			got := &proxyv1alpha1.Proxy{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got); err != nil {
				return err
			}
			got.Spec.FilterChains[0].Filters[0].InlineFilter.Type = "access_log"
			return k8sClient.Update(ctx, got)
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			cm := &corev1.ConfigMap{}
			gom.Expect(k8sClient.Get(ctx, types.NamespacedName{
				Name:      proxy.Name + "-config",
				Namespace: proxy.Namespace,
			}, cm)).To(Succeed())
			gom.Expect(cm.Data["config.yaml"]).To(ContainSubstring("access_log"))
			deploy := &appsv1.Deployment{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), deploy)).To(Succeed())
			newRevision := deploy.Spec.Template.Annotations["praxis.io/config-revision"]
			gom.Expect(newRevision).NotTo(BeEmpty())
			gom.Expect(newRevision).NotTo(Equal(origRevision))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should update Service type when spec.service.type changes", func(ctx context.Context) {
		proxy := validProxy("update-svc-type")
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			svc := &corev1.Service{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), svc)).To(Succeed())
			gom.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeClusterIP))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		Eventually(func(ctx context.Context) error {
			got := &proxyv1alpha1.Proxy{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got); err != nil {
				return err
			}
			nodePort := proxyv1alpha1.ServiceType(corev1.ServiceTypeNodePort)
			got.Spec.Service = &proxyv1alpha1.ProxyService{Type: &nodePort}
			return k8sClient.Update(ctx, got)
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			svc := &corev1.Service{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), svc)).To(Succeed())
			gom.Expect(svc.Spec.Type).To(Equal(corev1.ServiceTypeNodePort))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should update revision when spec.image changes", func(ctx context.Context) {
		proxy := validProxy("rev-image")
		createProxy(ctx, proxy)

		var origRevision string
		Eventually(func(gom Gomega, ctx context.Context) {
			got := &proxyv1alpha1.Proxy{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
			gom.Expect(got.Status.DesiredRevision).NotTo(BeEmpty())
			origRevision = got.Status.DesiredRevision
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		Eventually(func(ctx context.Context) error {
			got := &proxyv1alpha1.Proxy{}
			if err := k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got); err != nil {
				return err
			}
			got.Spec.Image = "ghcr.io/praxis-proxy/praxis:v2"
			return k8sClient.Update(ctx, got)
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			got := &proxyv1alpha1.Proxy{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
			gom.Expect(got.Status.DesiredRevision).NotTo(BeEmpty())
			gom.Expect(got.Status.DesiredRevision).NotTo(Equal(origRevision))
		}).WithContext(ctx).WithTimeout(proxyReconcileTimeout).Should(Succeed())
	})

	It("should set Accepted=False and ResolvedRefs=False for unrenderable filter", func(ctx context.Context) {
		proxy := validProxy("render-error")
		proxy.Spec.FilterChains[0].Filters[0] = proxyv1alpha1.PraxisFilterEntry{
			Name: "unknown",
			Ref: &proxyv1alpha1.PraxisFilterReference{
				Group: "other.io",
				Kind:  "CustomFilter",
				Name:  "my-filter",
			},
		}
		createProxy(ctx, proxy)

		Eventually(func(gom Gomega, ctx context.Context) {
			got := &proxyv1alpha1.Proxy{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
			accepted := meta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
			gom.Expect(accepted).NotTo(BeNil())
			gom.Expect(accepted.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(accepted.Reason).To(Equal("ConfigRenderError"))
			gom.Expect(accepted.Message).To(ContainSubstring("unsupported filter"))
			resolved := meta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs)
			gom.Expect(resolved).NotTo(BeNil())
			gom.Expect(resolved.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(resolved.Reason).To(Equal("ConfigRenderError"))
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
			err := mgrClient.Get(ctx, client.ObjectKeyFromObject(proxy), &proxyv1alpha1.Proxy{})
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
