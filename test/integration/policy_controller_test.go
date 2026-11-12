package integration

import (
	"context"
	"strconv"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	"github.com/praxis-proxy/gateway-controller/internal/controller"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

func waitForCache(ctx context.Context, obj client.Object) {
	key := client.ObjectKeyFromObject(obj)
	minRV, err := strconv.Atoi(obj.GetResourceVersion())
	Expect(err).NotTo(HaveOccurred())
	Eventually(func() int {
		current := obj.DeepCopyObject().(client.Object)
		if getErr := mgrClient.Get(ctx, key, current); getErr != nil {
			return 0
		}
		rv, parseErr := strconv.Atoi(current.GetResourceVersion())
		if parseErr != nil {
			return 0
		}
		return rv
	}).Should(BeNumerically(">=", minRV))
}

var _ = Describe("Policy Controller", func() {
	var (
		policy     *proxyv1alpha1.Policy
		reconciler *controller.PolicyReconciler
	)

	BeforeEach(func(ctx context.Context) {
		policy = &proxyv1alpha1.Policy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-policy-",
				Namespace:    "default",
			},
			Spec: proxyv1alpha1.PolicySpec{
				Document: "engine_settings:\n  log_level: info\n",
			},
		}
		Expect(k8sClient.Create(ctx, policy)).To(Succeed())
		waitForCache(ctx, policy)

		reconciler = &controller.PolicyReconciler{
			Client: mgrClient,
			Scheme: mgrClient.Scheme(),
		}
	})

	AfterEach(func(ctx context.Context) {
		Expect(client.IgnoreNotFound(k8sClient.Delete(ctx, policy))).To(Succeed())
	})

	It("should set Accepted condition and compute revision", func(ctx context.Context) {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())

		Expect(policy.Status.ObservedGeneration).To(Equal(policy.Generation))
		Expect(policy.Status.EffectiveRevision).NotTo(BeEmpty())

		accepted := meta.FindStatusCondition(policy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(accepted).NotTo(BeNil())
		Expect(accepted.Status).To(Equal(metav1.ConditionTrue))
		Expect(accepted.Reason).To(Equal("Valid"))
	})

	It("should track usedBy when a proxy references the policy", func(ctx context.Context) {
		proxy := &proxyv1alpha1.Proxy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "test-proxy-",
				Namespace:    "default",
			},
			Spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name: "http",
					Port: 8080,
				}},
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{{
						Name: "policy",
						Ref: &proxyv1alpha1.PraxisFilterReference{
							Group: proxyv1alpha1.PraxisGroup,
							Kind:  proxyv1alpha1.PolicyKind,
							Name:  policy.Name,
						},
					}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
		})

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Status.UsedBy).To(ContainElement(policy.Namespace + "/" + proxy.Name))
	})

	It("should clear usedBy when no proxy references the policy", func(ctx context.Context) {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Status.UsedBy).To(BeEmpty())
	})

	It("should not include a proxy with an inline filter in usedBy", func(ctx context.Context) {
		proxy := &proxyv1alpha1.Proxy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "inline-proxy-",
				Namespace:    "default",
			},
			Spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name: "http",
					Port: 8080,
				}},
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{{
						Name: "inline",
						InlineFilter: &proxyv1alpha1.PraxisInlineFilter{
							Type: "rate_limit",
						},
					}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
		})

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Status.UsedBy).To(BeEmpty())
	})

	It("should not include a proxy referencing a different policy in usedBy", func(ctx context.Context) {
		proxy := &proxyv1alpha1.Proxy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "other-ref-proxy-",
				Namespace:    "default",
			},
			Spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name: "http",
					Port: 8080,
				}},
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{{
						Name: "policy",
						Ref: &proxyv1alpha1.PraxisFilterReference{
							Group: proxyv1alpha1.PraxisGroup,
							Kind:  proxyv1alpha1.PolicyKind,
							Name:  "some-other-policy",
						},
					}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
		})

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Status.UsedBy).To(BeEmpty())
	})

	It("should produce a stable revision that changes when spec changes", func(ctx context.Context) {
		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		firstRevision := policy.Status.EffectiveRevision
		Expect(firstRevision).NotTo(BeEmpty())
		originalGeneration := policy.Generation

		_, err = reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Status.EffectiveRevision).To(Equal(firstRevision))

		policy.Spec.Document = "engine_settings:\n  log_level: debug\n"
		Expect(k8sClient.Update(ctx, policy)).To(Succeed())
		waitForCache(ctx, policy)

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Generation).To(BeNumerically(">", originalGeneration))

		_, err = reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Status.EffectiveRevision).NotTo(Equal(firstRevision))
		Expect(policy.Status.ObservedGeneration).To(Equal(policy.Generation))
	})

	It("should aggregate usedBy from multiple proxies", func(ctx context.Context) {
		createRefProxy := func(namePrefix string) *proxyv1alpha1.Proxy {
			proxy := &proxyv1alpha1.Proxy{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: namePrefix,
					Namespace:    "default",
				},
				Spec: proxyv1alpha1.ProxySpec{
					Image: "ghcr.io/praxis-proxy/praxis:latest",
					Listeners: []proxyv1alpha1.PraxisListener{{
						Name: "http",
						Port: 8080,
					}},
					FilterChains: []proxyv1alpha1.ProxyFilterChain{{
						Name: "chain",
						Filters: []proxyv1alpha1.PraxisFilterEntry{{
							Name: "policy",
							Ref: &proxyv1alpha1.PraxisFilterReference{
								Group: proxyv1alpha1.PraxisGroup,
								Kind:  proxyv1alpha1.PolicyKind,
								Name:  policy.Name,
							},
						}},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
			waitForCache(ctx, proxy)
			DeferCleanup(func(ctx context.Context) error {
				return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
			})
			return proxy
		}

		proxyA := createRefProxy("multi-a-")
		proxyB := createRefProxy("multi-b-")

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())
		Expect(policy.Status.UsedBy).To(HaveLen(2))
		Expect(policy.Status.UsedBy).To(ContainElement("default/" + proxyA.Name))
		Expect(policy.Status.UsedBy).To(ContainElement("default/" + proxyB.Name))

		// Verify sorted order
		Expect(policy.Status.UsedBy[0] < policy.Status.UsedBy[1]).To(BeTrue(),
			"usedBy should be sorted, got %v", policy.Status.UsedBy)
	})

	It("should set Accepted to False for an invalid YAML document", func(ctx context.Context) {
		proxy := &proxyv1alpha1.Proxy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "invalid-doc-proxy-",
				Namespace:    "default",
			},
			Spec: proxyv1alpha1.ProxySpec{
				Image: "ghcr.io/praxis-proxy/praxis:latest",
				Listeners: []proxyv1alpha1.PraxisListener{{
					Name: "http",
					Port: 8080,
				}},
				FilterChains: []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{{
						Name: "policy",
						Ref: &proxyv1alpha1.PraxisFilterReference{
							Group: proxyv1alpha1.PraxisGroup,
							Kind:  proxyv1alpha1.PolicyKind,
							Name:  policy.Name,
						},
					}},
				}},
			},
		}
		Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
		waitForCache(ctx, proxy)
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
		})

		policy.Spec.Document = "{{invalid yaml"
		Expect(k8sClient.Update(ctx, policy)).To(Succeed())
		waitForCache(ctx, policy)

		_, err := reconciler.Reconcile(ctx, reconcile.Request{
			NamespacedName: client.ObjectKeyFromObject(policy),
		})
		Expect(err).NotTo(HaveOccurred())

		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(policy), policy)).To(Succeed())

		accepted := meta.FindStatusCondition(policy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
		Expect(accepted).NotTo(BeNil())
		Expect(accepted.Status).To(Equal(metav1.ConditionFalse))
		Expect(accepted.Reason).To(Equal("InvalidDocument"))

		Expect(policy.Status.UsedBy).To(ContainElement("default/" + proxy.Name))
	})
})
