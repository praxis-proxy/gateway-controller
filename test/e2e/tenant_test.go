package e2e

import (
	"context"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

// Creates a Tenant with the full documented spec in a fresh namespace and
// polls until the deployed manager reports it Ready, covering the Tenant CRD
// install and the controller's end-to-end reconcile path.
var _ = Describe("Tenant", func() {
	It("reaches Ready once created", func(ctx context.Context) {
		ns := &corev1.Namespace{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "tenant-e2e-"},
		}
		Expect(k8sClient.Create(ctx, ns)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, ns))
		})

		tenant := &aigatewayv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "team-alpha",
				Namespace: ns.Name,
			},
			Spec: aigatewayv1alpha1.TenantSpec{
				AuthorizedIdentities: []aigatewayv1alpha1.AuthorizedIdentity{
					{Type: aigatewayv1alpha1.IdentityTypeOIDC},
					{Type: aigatewayv1alpha1.IdentityTypeAPIKey},
				},
				AuthorizedCredentials: []aigatewayv1alpha1.AuthorizedCredential{
					{SecretRef: aigatewayv1alpha1.LocalSecretReference{Name: "model-provider-key"}},
				},
			},
		}
		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		Eventually(func(gom Gomega, ctx context.Context) {
			got := &aigatewayv1alpha1.Tenant{}
			gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), got)).To(Succeed())

			ready := meta.FindStatusCondition(got.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady)
			gom.Expect(ready).NotTo(BeNil())
			gom.Expect(ready.Status).To(Equal(metav1.ConditionTrue))
			gom.Expect(ready.Reason).To(Equal(aigatewayv1alpha1.ReasonReady))
		}).WithContext(ctx).
			WithTimeout(30 * time.Second).
			WithPolling(time.Second).
			Should(Succeed())
	})
})
