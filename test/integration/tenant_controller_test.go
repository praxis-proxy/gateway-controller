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

// envtestReconcileTimeout bounds every Eventually poll below. Attaching
// WithContext disables Gomega's own default Eventually timeout unless
// explicitly overridden, so without this each poll would be bounded only by
// Ginkgo's suite-wide deadline (an hour by default) instead of failing fast
// on a genuine regression.
const envtestReconcileTimeout = 5 * time.Second

// The TenantReconciler is registered against the suite's manager in
// integration_suite_test.go, so it reconciles through its own watch, exactly
// as it would in production. Specs act only through k8sClient and assert the
// result with Eventually, rather than invoking Reconcile directly.
var _ = Describe("Tenant Controller", func() {
	// createTenant creates a Tenant with the full documented spec. The populated
	// spec exercises the same validation path a real Tenant would go through;
	// readiness depends only on namespace ownership, so this also covers
	// conflict and promotion with a realistic object.
	createTenant := func(ctx context.Context, namespace, name string) *aigatewayv1alpha1.Tenant {
		tenant := &aigatewayv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: namespace},
			Spec:       validTenantSpec(),
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, tenant))
		})
		return tenant
	}

	readyCondition := func(ctx context.Context, tenant *aigatewayv1alpha1.Tenant) *metav1.Condition {
		got := &aigatewayv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), got)).To(Succeed())
		return meta.FindStatusCondition(got.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady)
	}

	It("marks the only Tenant in a namespace Ready", func(ctx context.Context) {
		tenant := createTenant(ctx, newTestNamespace(ctx), "team-alpha")

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := readyCondition(ctx, tenant)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
			gom.Expect(ready.ObservedGeneration).To(Equal(tenant.Generation))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())

		// Readiness depends only on namespace ownership, never on spec content;
		// confirm the full documented spec survived reconciliation unchanged.
		got := &aigatewayv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), got)).To(Succeed())
		Expect(got.Spec).To(Equal(validTenantSpec()))
	})

	It("marks a second Tenant Conflict and leaves the owner Ready", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)
		// creationTimestamp has one-second resolution, so two Tenants created in
		// succession routinely tie and the name decides. The names are chosen so
		// the expected owner wins either way.
		owner := createTenant(ctx, namespace, "aaa-owner")
		loser := createTenant(ctx, namespace, "zzz-later")

		Eventually(func(gom Gomega, ctx context.Context) {
			ownerReady := readyCondition(ctx, owner)
			gom.Expect(ownerReady).NotTo(BeNil())
			gom.Expect(ownerReady.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(ownerReady.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))

			loserReady := readyCondition(ctx, loser)
			gom.Expect(loserReady).NotTo(BeNil())
			gom.Expect(loserReady.Status).To(Equal(metav1.ConditionFalse))
			gom.Expect(loserReady.Reason).To(Equal(aigatewayv1alpha1.ReasonConflict))
			gom.Expect(loserReady.Message).To(ContainSubstring("aaa-owner"),
				"the message should identify the winning Tenant")
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})

	It("promotes the survivor once the owner is deleted", func(ctx context.Context) {
		namespace := newTestNamespace(ctx)
		owner := createTenant(ctx, namespace, "aaa-owner")
		survivor := createTenant(ctx, namespace, "zzz-later")

		Eventually(func(gom Gomega, ctx context.Context) {
			ready := readyCondition(ctx, survivor)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonConflict))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())

		Expect(k8sClient.Delete(ctx, owner)).To(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			promoted := readyCondition(ctx, survivor)
			gom.Expect(promoted).NotTo(BeNil())
			gom.Expect(promoted.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(promoted.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
		}).WithContext(ctx).WithTimeout(envtestReconcileTimeout).Should(Succeed())
	})
})
