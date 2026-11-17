package integration

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

// The AIGatewayReconciler is registered against the suite's manager in
// integration_suite_test.go, so it reconciles through its own watches, exactly
// as it would in production. Specs act only through k8sClient and assert the
// result with Eventually, rather than invoking Reconcile directly.
//
// Ready here is v1alpha1's partial form: the spec validated and the Tenant
// resolved. It will also require the generated configuration to be applied and
// the backing Proxy to report Ready, so these specs are not yet a complete
// account of the condition. See validateAndUpdateStatus for what is missing.
var _ = Describe("AIGateway Controller", func() {
	createTenant := func(
		ctx context.Context, namespace, name string, spec aigatewayv1alpha1.TenantSpec,
	) *aigatewayv1alpha1.Tenant {
		tenant := &aigatewayv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       spec,
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, tenant))
		})
		return tenant
	}

	createGateway := func(
		ctx context.Context, namespace, name string, spec aigatewayv1alpha1.AIGatewaySpec,
	) *aigatewayv1alpha1.AIGateway {
		gateway := &aigatewayv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       spec,
		}
		Expect(k8sClient.Create(ctx, gateway)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, gateway))
		})
		return gateway
	}

	// gatewaySpec is the minimal valid spec: one inference API on a plain
	// endpoint. Specs needing more set fields on the returned value, so the
	// difference from minimal stays visible at the call site.
	gatewaySpec := func(tenantName string) aigatewayv1alpha1.AIGatewaySpec {
		return aigatewayv1alpha1.AIGatewaySpec{
			TenantRef: aigatewayv1alpha1.LocalObjectReference{Name: tenantName},
			Endpoint:  aigatewayv1alpha1.EndpointSpec{Port: 8443},
			Inference: &aigatewayv1alpha1.InferenceSpec{
				ChatCompletions: &aigatewayv1alpha1.ChatCompletionsSpec{},
			},
		}
	}

	gatewayReady := func(ctx context.Context, gateway *aigatewayv1alpha1.AIGateway) *metav1.Condition {
		got := &aigatewayv1alpha1.AIGateway{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gateway), got)).To(Succeed())
		return meta.FindStatusCondition(got.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady)
	}

	tenantReady := func(ctx context.Context, tenant *aigatewayv1alpha1.Tenant) *metav1.Condition {
		got := &aigatewayv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), got)).To(Succeed())
		return meta.FindStatusCondition(got.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady)
	}

	It("becomes ready when the spec is valid and the Tenant is ready", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)
		createTenant(ctx, namespace, "team-alpha", aigatewayv1alpha1.TenantSpec{
			AuthorizedIdentities: []aigatewayv1alpha1.AuthorizedIdentity{
				{Type: aigatewayv1alpha1.IdentityTypeOIDC},
			},
		})

		gateway := createGateway(ctx, namespace, "team-alpha-gw", gatewaySpec("team-alpha"))

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
			gom.Expect(ready.ObservedGeneration).To(Equal(gateway.Generation))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})

	It("reports DependencyNotReady when the Tenant does not exist", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)

		gateway := createGateway(ctx, namespace, "orphan-gw", gatewaySpec("absent-tenant"))

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonDependencyNotReady))
			gom.Expect(ready.Message).To(ContainSubstring("absent-tenant"),
				"the message should name the unresolved Tenant")
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})

	It("reports DependencyNotReady while its Tenant is not ready", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)
		// The second Tenant in a namespace loses the ownership conflict, so it
		// never reaches Ready. Names are chosen so the owner wins whether the
		// creationTimestamps tie or not.
		createTenant(ctx, namespace, "aaa-owner", aigatewayv1alpha1.TenantSpec{})
		createTenant(ctx, namespace, "zzz-later", aigatewayv1alpha1.TenantSpec{})

		gateway := createGateway(ctx, namespace, "waiting-gw", gatewaySpec("zzz-later"))

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonDependencyNotReady))
			gom.Expect(ready.Message).To(ContainSubstring("is not ready"),
				"an existing but unready Tenant must not be reported as missing")
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})

	It("becomes ready when its Tenant is created afterwards", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)

		gateway := createGateway(ctx, namespace, "early-gw", gatewaySpec("late-tenant"))

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonDependencyNotReady))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())

		createTenant(ctx, namespace, "late-tenant", aigatewayv1alpha1.TenantSpec{})

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})

	// Promotion is a status-only write on an existing Tenant, so this fails if
	// the Tenant watch ever filters Updates by generation.
	It("becomes ready when its Tenant is promoted after losing a conflict", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)
		owner := createTenant(ctx, namespace, "aaa-owner", aigatewayv1alpha1.TenantSpec{})
		survivor := createTenant(ctx, namespace, "zzz-later", aigatewayv1alpha1.TenantSpec{})

		gateway := createGateway(ctx, namespace, "promoted-gw", gatewaySpec("zzz-later"))

		Eventually(func(gom Gomega, ctx context.Context) {
			gom.Expect(tenantReady(ctx, survivor)).NotTo(BeNil())
			gom.Expect(tenantReady(ctx, survivor).Reason).To(Equal(aigatewayv1alpha1.ReasonConflict))

			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonDependencyNotReady))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())

		// Wait for the gateway to stop being written to, so promotion is the
		// only thing that can drive the assertion below. Pinning
		// resourceVersion catches any write, not just ones that change Ready.
		settled := &aigatewayv1alpha1.AIGateway{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gateway), settled)).To(Succeed())
		Consistently(func(gom Gomega, ctx context.Context) {
			current := &aigatewayv1alpha1.AIGateway{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gateway), current)).To(Succeed())
			gom.Expect(current.ResourceVersion).To(Equal(settled.ResourceVersion))
		}).WithContext(ctx).WithTimeout(500 * time.Millisecond).
			WithPolling(100 * time.Millisecond).Should(Succeed())

		Expect(k8sClient.Delete(ctx, owner)).To(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			promoted := tenantReady(ctx, survivor)
			gom.Expect(promoted).NotTo(BeNil())
			gom.Expect(promoted.Status).To(Equal(metav1.ConditionTrue))

			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})

	It("reports Invalid when an authentication type is not authorized by the Tenant", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)
		createTenant(ctx, namespace, "oidc-only", aigatewayv1alpha1.TenantSpec{
			AuthorizedIdentities: []aigatewayv1alpha1.AuthorizedIdentity{
				{Type: aigatewayv1alpha1.IdentityTypeOIDC},
			},
		})

		spec := gatewaySpec("oidc-only")
		spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
			Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
				{Type: aigatewayv1alpha1.IdentityTypeAPIKey, Header: "X-API-Key"},
			},
		}
		gateway := createGateway(ctx, namespace, "unauthorized-auth-gw", spec)

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonInvalid))
			gom.Expect(ready.Message).To(ContainSubstring("APIKey"),
				"the message should name the unauthorized type")
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})

	It("becomes ready when the Tenant authorizes the configured authentication type", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)
		createTenant(ctx, namespace, "both-types", aigatewayv1alpha1.TenantSpec{
			AuthorizedIdentities: []aigatewayv1alpha1.AuthorizedIdentity{
				{Type: aigatewayv1alpha1.IdentityTypeOIDC},
				{Type: aigatewayv1alpha1.IdentityTypeAPIKey},
			},
		})

		spec := gatewaySpec("both-types")
		spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
			Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
				{Type: aigatewayv1alpha1.IdentityTypeOIDC, IssuerURL: "https://sso.example.com"},
			},
		}
		gateway := createGateway(ctx, namespace, "authorized-auth-gw", spec)

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := gatewayReady(ctx, gateway)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})
})
