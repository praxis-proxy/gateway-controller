package e2e

import (
	"context"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

// Minimal gateway stack scenario for issue #32: https://github.com/praxis-proxy/gateway-controller/issues/32
//
// This scenario exercises a minimal but functional gateway configuration:
// - Tenant: basic configuration, no authentication
// - AIGateway: single inference endpoint with ChatCompletions
//
// The specs carry what that story needs and no more. This is a minimal happy
// path, not a survey of every field Tenant and AIGateway accept: a populated
// spec round-trip is the integration suite's job. Capabilities tracked under
// a separate Feature belong to that Feature's scenario.
//
// As the system grows, this scenario will evolve to include:
// - ModelProvider: minimal configuration pointing to a model service
// - Model: minimal configuration to publish the model through the gateway
var _ = Describe("Minimal gateway stack", func() {
	const timeout = time.Second * 30

	It("deploys a complete minimal gateway configuration", func(ctx context.Context) {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "minimal-gateway-"},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, ns))
		})

		// Create Tenant: basic configuration, no authentication.
		tenant := &aigatewayv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "example-team",
				Namespace: ns.Name,
			},
			Spec: aigatewayv1alpha1.TenantSpec{},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		// Verify Tenant is ready (no dependencies to resolve).
		Eventually(func(gom Gomega, ctx context.Context) {
			t := &aigatewayv1alpha1.Tenant{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), t)).To(Succeed())
			cond := meta.FindStatusCondition(t.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(cond.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
		}).WithContext(ctx).WithTimeout(timeout).Should(Succeed())

		// Create AIGateway: minimal configuration with ChatCompletions inference.
		gw := &aigatewayv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "example-gateway",
				Namespace: ns.Name,
			},
			Spec: aigatewayv1alpha1.AIGatewaySpec{
				TenantRef: aigatewayv1alpha1.LocalObjectReference{Name: "example-team"},
				Endpoint: aigatewayv1alpha1.EndpointSpec{
					Port: 8080,
				},
				Inference: &aigatewayv1alpha1.InferenceSpec{
					ChatCompletions: &aigatewayv1alpha1.ChatCompletionsSpec{},
				},
			},
		}
		Expect(k8sClient.Create(ctx, gw)).To(Succeed())

		// Verify AIGateway is ready (Tenant dependency resolved, spec valid).
		Eventually(func(gom Gomega, ctx context.Context) {
			got := &aigatewayv1alpha1.AIGateway{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gw), got)).To(Succeed())
			cond := meta.FindStatusCondition(got.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady)
			gom.Expect(cond).NotTo(BeNil())
			gom.Expect(cond.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(cond.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
		}).WithContext(ctx).WithTimeout(timeout).Should(Succeed())
	})
})
