package integration

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

// newTestNamespace creates a uniquely named namespace and returns its name.
//
// Tenant behaviour is namespace-scoped, so specs cannot share the default
// namespace the way the Proxy and Policy specs do. The namespace is
// deliberately never deleted: envtest runs no namespace controller, so a
// deleted namespace stays Terminating forever and never collects its contents.
// Specs clean up the objects they create instead.
func newTestNamespace(ctx context.Context) string {
	ns := &corev1.Namespace{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "tenant-test-"},
	}
	Expect(k8sClient.Create(ctx, ns)).To(Succeed())
	return ns.Name
}

// validTenantSpec is the documented example from api-v1alpha1.md. The rejection
// cases below each break one part of it, so a failure names the constraint that
// is missing rather than any of the others.
func validTenantSpec() aigatewayv1alpha1.TenantSpec {
	return aigatewayv1alpha1.TenantSpec{
		AuthorizedIdentities: []aigatewayv1alpha1.AuthorizedIdentity{
			{Type: aigatewayv1alpha1.IdentityTypeOIDC},
			{Type: aigatewayv1alpha1.IdentityTypeAPIKey},
		},
		AuthorizedCredentials: []aigatewayv1alpha1.AuthorizedCredential{
			{SecretRef: aigatewayv1alpha1.LocalObjectReference{Name: "model-provider-key"}},
		},
	}
}

var _ = Describe("Tenant CRD", func() {
	It("accepts a Tenant with no spec", func(ctx context.Context) {
		tenant := &aigatewayv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "minimal-",
				Namespace:    newTestNamespace(ctx),
			},
		}

		Expect(k8sClient.Create(ctx, tenant)).To(Succeed(),
			"a Tenant granting nothing is a valid ownership anchor")

		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, tenant))
		})
	})

	It("accepts the documented example and reads it back", func(ctx context.Context) {
		tenant := &aigatewayv1alpha1.Tenant{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "team-alpha",
				Namespace: newTestNamespace(ctx),
			},
			Spec: validTenantSpec(),
		}

		Expect(k8sClient.Create(ctx, tenant)).To(Succeed())

		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, tenant))
		})

		got := &aigatewayv1alpha1.Tenant{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(tenant), got)).To(Succeed())
		Expect(got.Spec.AuthorizedIdentities).To(HaveLen(2))
		Expect(got.Spec.AuthorizedCredentials).To(HaveLen(1))
		Expect(got.Spec.AuthorizedCredentials[0].SecretRef.Name).To(Equal("model-provider-key"))
	})

	DescribeTable("rejects an invalid Tenant",
		func(ctx context.Context, name string, breakSpec func(*aigatewayv1alpha1.TenantSpec)) {
			tenant := &aigatewayv1alpha1.Tenant{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: newTestNamespace(ctx),
				},
				Spec: validTenantSpec(),
			}
			breakSpec(&tenant.Spec)

			err := k8sClient.Create(ctx, tenant)
			Expect(err).To(HaveOccurred(), "the apiserver should have rejected this Tenant")
			// Asserted specifically, so the case cannot pass on an unrelated
			// failure such as a name collision.
			Expect(apierrors.IsInvalid(err)).To(BeTrue(),
				"expected a schema validation error, got: %v", err)
		},
		Entry("an unknown identity type", "unknown-identity-type",
			func(spec *aigatewayv1alpha1.TenantSpec) {
				spec.AuthorizedIdentities = []aigatewayv1alpha1.AuthorizedIdentity{
					{Type: aigatewayv1alpha1.IdentityType("SAML")},
				}
			}),
		Entry("duplicate identity types", "duplicate-identity-types",
			func(spec *aigatewayv1alpha1.TenantSpec) {
				spec.AuthorizedIdentities = []aigatewayv1alpha1.AuthorizedIdentity{
					{Type: aigatewayv1alpha1.IdentityTypeOIDC},
					{Type: aigatewayv1alpha1.IdentityTypeOIDC},
				}
			}),
		Entry("duplicate credential secret names", "duplicate-credentials",
			func(spec *aigatewayv1alpha1.TenantSpec) {
				spec.AuthorizedCredentials = []aigatewayv1alpha1.AuthorizedCredential{
					{SecretRef: aigatewayv1alpha1.LocalObjectReference{Name: "shared"}},
					{SecretRef: aigatewayv1alpha1.LocalObjectReference{Name: "shared"}},
				}
			}),
		Entry("an empty credential secret name", "empty-credential-name",
			func(spec *aigatewayv1alpha1.TenantSpec) {
				spec.AuthorizedCredentials = []aigatewayv1alpha1.AuthorizedCredential{
					{SecretRef: aigatewayv1alpha1.LocalObjectReference{Name: ""}},
				}
			}),
	)
})
