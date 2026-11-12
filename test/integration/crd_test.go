package integration

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

// validProxySpec is the smallest spec the CRD admits. The rejection cases
// below each break one field of it, so that a failure names the constraint
// that is missing rather than any of the others.
func validProxySpec() proxyv1alpha1.ProxySpec {
	return proxyv1alpha1.ProxySpec{
		Image: "example.com/praxis:latest",
		Listeners: []proxyv1alpha1.PraxisListener{
			{Name: "http", Port: 8080},
		},
	}
}

var _ = Describe("Proxy CRD", func() {
	It("accepts a minimal valid Proxy and reads it back", func(ctx context.Context) {
		proxy := &proxyv1alpha1.Proxy{
			ObjectMeta: metav1.ObjectMeta{
				GenerateName: "valid-",
				Namespace:    "default",
			},
			Spec: validProxySpec(),
		}

		Expect(k8sClient.Create(ctx, proxy)).To(Succeed(), "a minimal valid Proxy should be admitted")

		// Register cleanup immediately to ensure deletion on all test exit paths.
		// NotFound is ignored: cleanup asserts the object is gone, not that this
		// call is the one that removed it.
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
		})

		got := &proxyv1alpha1.Proxy{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(proxy), got)).To(Succeed())
		Expect(got.Spec.Image).To(Equal("example.com/praxis:latest"))
	})

	It("rejects a Proxy with a missing image field", func(ctx context.Context) {
		proxy := &unstructured.Unstructured{}
		proxy.SetGroupVersionKind(proxyv1alpha1.GroupVersion.WithKind("Proxy"))
		proxy.SetNamespace("default")
		proxy.SetGenerateName("no-image-")

		spec := map[string]any{
			"listeners": []map[string]any{
				{
					"name": "http",
					"port": 8080,
				},
			},
		}
		proxy.Object["spec"] = spec

		err := k8sClient.Create(ctx, proxy)
		Expect(err).To(HaveOccurred(), "the apiserver should reject a Proxy with a missing image field")
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"expected a schema validation error for missing image, got: %v", err)
	})

	It("rejects a Proxy with a listener missing a port", func(ctx context.Context) {
		proxy := &unstructured.Unstructured{}
		proxy.SetGroupVersionKind(proxyv1alpha1.GroupVersion.WithKind("Proxy"))
		proxy.SetNamespace("default")
		proxy.SetGenerateName("no-port-")

		spec := map[string]any{
			"image": "example.com/praxis:latest",
			"listeners": []map[string]any{
				{
					"name": "http",
					// port is intentionally omitted
				},
			},
		}
		proxy.Object["spec"] = spec

		err := k8sClient.Create(ctx, proxy)
		Expect(err).To(HaveOccurred(), "the apiserver should reject a Proxy with a missing listener port")
		Expect(apierrors.IsInvalid(err)).To(BeTrue(),
			"expected a schema validation error for missing port, got: %v", err)
	})

	// These assert that the validation markers on the Go types reached the
	// generated schema, which is the part that can silently regress.
	DescribeTable("rejects an invalid Proxy",
		func(ctx context.Context, name string, breakSpec func(*proxyv1alpha1.ProxySpec)) {
			proxy := &proxyv1alpha1.Proxy{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: "default",
				},
				Spec: validProxySpec(),
			}
			breakSpec(&proxy.Spec)

			err := k8sClient.Create(ctx, proxy)
			Expect(err).To(HaveOccurred(), "the apiserver should have rejected this Proxy")
			// Asserted specifically, so the case cannot pass on an unrelated
			// failure such as a name collision.
			Expect(apierrors.IsInvalid(err)).To(BeTrue(),
				"expected a schema validation error, got: %v", err)
		},
		Entry("an empty image", "empty-image",
			func(spec *proxyv1alpha1.ProxySpec) { spec.Image = "" }),
		// listeners has no omitempty, so these two reach the apiserver as
		// null and as [], and fail different parts of the schema.
		Entry("null listeners", "null-listeners",
			func(spec *proxyv1alpha1.ProxySpec) { spec.Listeners = nil }),
		Entry("an empty listener list", "empty-listeners",
			func(spec *proxyv1alpha1.ProxySpec) {
				spec.Listeners = []proxyv1alpha1.PraxisListener{}
			}),
		Entry("a listener with an empty name", "empty-listener-name",
			func(spec *proxyv1alpha1.ProxySpec) { spec.Listeners[0].Name = "" }),
		Entry("duplicate listener names", "duplicate-listener-names",
			func(spec *proxyv1alpha1.ProxySpec) {
				spec.Listeners = append(spec.Listeners, proxyv1alpha1.PraxisListener{
					Name: "http",
					Port: 8081,
				})
			}),
		Entry("a listener port below the minimum", "port-too-low",
			func(spec *proxyv1alpha1.ProxySpec) { spec.Listeners[0].Port = 0 }),
		Entry("a listener port above the maximum", "port-too-high",
			func(spec *proxyv1alpha1.ProxySpec) { spec.Listeners[0].Port = 65536 }),
		Entry("a filter entry with neither ref nor inlineFilter", "no-ref-no-inline",
			func(spec *proxyv1alpha1.ProxySpec) {
				spec.FilterChains = []proxyv1alpha1.ProxyFilterChain{{
					Name: "chain",
					Filters: []proxyv1alpha1.PraxisFilterEntry{
						{Name: "empty"},
					},
				}}
			}),
	)

	// Verify that valid port boundaries are accepted
	DescribeTable("accepts a valid Proxy with boundary port values",
		func(ctx context.Context, port int32) {
			proxy := &proxyv1alpha1.Proxy{
				ObjectMeta: metav1.ObjectMeta{
					GenerateName: "valid-port-",
					Namespace:    "default",
				},
				Spec: proxyv1alpha1.ProxySpec{
					Image: "example.com/praxis:latest",
					Listeners: []proxyv1alpha1.PraxisListener{
						{Name: "http", Port: port},
					},
				},
			}

			Expect(k8sClient.Create(ctx, proxy)).To(Succeed(), "a Proxy with valid port %d should be admitted", port)

			// Register cleanup
			DeferCleanup(func(ctx context.Context) error {
				return client.IgnoreNotFound(k8sClient.Delete(ctx, proxy))
			})
		},
		Entry("minimum valid port", int32(1)),
		Entry("maximum valid port", int32(65535)),
	)
})
