package integration

import (
	"context"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/apis/meta/v1/unstructured"
	"sigs.k8s.io/controller-runtime/pkg/client"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

// validAIGatewaySpec is the documented example from api-v1alpha1.md. The rejection
// cases below each break one part of it, so a failure names the constraint that
// is missing rather than any of the others.
func validAIGatewaySpec() aigatewayv1alpha1.AIGatewaySpec {
	return aigatewayv1alpha1.AIGatewaySpec{
		TenantRef: aigatewayv1alpha1.LocalObjectReference{Name: "team-alpha"},
		Endpoint: aigatewayv1alpha1.EndpointSpec{
			Port: 8443,
		},
		Inference: &aigatewayv1alpha1.InferenceSpec{
			ChatCompletions: &aigatewayv1alpha1.ChatCompletionsSpec{},
		},
	}
}

var _ = Describe("AIGateway CRD", func() {
	It("accepts a minimal valid AIGateway and reads it back", func(ctx context.Context) {
		ns := newTestNamespace(ctx)
		gw := &aigatewayv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "team-alpha-gw",
				Namespace: ns,
			},
			Spec: validAIGatewaySpec(),
		}

		Expect(k8sClient.Create(ctx, gw)).To(Succeed())

		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, gw))
		})

		got := &aigatewayv1alpha1.AIGateway{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gw), got)).To(Succeed())
		Expect(got.Spec.Endpoint.Port).To(Equal(int32(8443)))
		Expect(got.Spec.Inference).NotTo(BeNil())
		Expect(got.Spec.Inference.ChatCompletions).NotTo(BeNil())
	})

	It("accepts AIGateway with both inference and MCP", func(ctx context.Context) {
		ns := newTestNamespace(ctx)
		gw := &aigatewayv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "both-capabilities",
				Namespace: ns,
			},
			Spec: aigatewayv1alpha1.AIGatewaySpec{
				TenantRef: aigatewayv1alpha1.LocalObjectReference{Name: "team-alpha"},
				Endpoint: aigatewayv1alpha1.EndpointSpec{
					Port: 8443,
				},
				Inference: &aigatewayv1alpha1.InferenceSpec{
					ChatCompletions: &aigatewayv1alpha1.ChatCompletionsSpec{},
				},
				MCP: &aigatewayv1alpha1.MCPSpec{},
			},
		}

		Expect(k8sClient.Create(ctx, gw)).To(Succeed())

		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, gw))
		})
	})

	It("rejects attempts to change tenantRef.name", func(ctx context.Context) {
		ns := newTestNamespace(ctx)
		gw := &aigatewayv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{
				Name:      "immutable-tenant-name",
				Namespace: ns,
			},
			Spec: validAIGatewaySpec(),
		}

		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, gw))
		})

		patch := client.MergeFrom(gw.DeepCopy())
		gw.Spec.TenantRef.Name = "different-tenant"
		err := k8sClient.Patch(ctx, gw, patch)
		Expect(err).To(HaveOccurred(), "apiserver should reject tenantRef change")
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got: %v", err)
		Expect(err.Error()).To(ContainSubstring("tenantRef is immutable"))
	})

	It("applies documented defaults", func(ctx context.Context) {
		ns := newTestNamespace(ctx)
		spec := validAIGatewaySpec()
		spec.Runtime = &aigatewayv1alpha1.RuntimeSpec{}
		spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
			Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
				{Type: aigatewayv1alpha1.IdentityTypeAPIKey},
			},
		}
		gw := &aigatewayv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{Name: "defaults", Namespace: ns},
			Spec:       spec,
		}

		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, gw))
		})

		got := &aigatewayv1alpha1.AIGateway{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gw), got)).To(Succeed())
		Expect(got.Spec.Runtime.Replicas).NotTo(BeNil())
		Expect(*got.Spec.Runtime.Replicas).To(Equal(int32(1)))
		auth := got.Spec.Governance.Authentication[0]
		Expect(auth.RefreshSeconds).NotTo(BeNil())
		Expect(*auth.RefreshSeconds).To(Equal(int32(5)))
		Expect(auth.MaxStalenessSeconds).NotTo(BeNil())
		Expect(*auth.MaxStalenessSeconds).To(Equal(int32(30)))
	})

	It("rejects an AIGateway submitted with no spec", func(ctx context.Context) {
		ns := newTestNamespace(ctx)
		gw := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": aigatewayv1alpha1.SchemeGroupVersion.String(),
			"kind":       "AIGateway",
			"metadata":   map[string]any{"name": "no-spec", "namespace": ns},
		}}

		err := k8sClient.Create(ctx, gw)
		Expect(err).To(HaveOccurred(), "the apiserver should have rejected this AIGateway")
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got: %v", err)
		Expect(err.Error()).To(ContainSubstring("spec: Required value"))
	})

	It("rejects TLS configured without a certificateRef key", func(ctx context.Context) {
		ns := newTestNamespace(ctx)
		gw := &unstructured.Unstructured{Object: map[string]any{
			"apiVersion": aigatewayv1alpha1.SchemeGroupVersion.String(),
			"kind":       "AIGateway",
			"metadata":   map[string]any{"name": "tls-no-cert-key", "namespace": ns},
			"spec": map[string]any{
				"tenantRef": map[string]any{"name": "team-alpha"},
				"endpoint": map[string]any{
					"port": int64(8443),
					"tls":  map[string]any{},
				},
				"inference": map[string]any{"chatCompletions": map[string]any{}},
			},
		}}

		err := k8sClient.Create(ctx, gw)
		Expect(err).To(HaveOccurred(), "the apiserver should have rejected this AIGateway")
		Expect(apierrors.IsInvalid(err)).To(BeTrue(), "expected a validation error, got: %v", err)
		Expect(err.Error()).To(ContainSubstring("spec.endpoint.tls.certificateRef: Required value"))
	})

	// All validation tests (schema and CEL) are enforced by the apiserver in envtest.
	DescribeTable("rejects an invalid AIGateway",
		func(ctx context.Context, name string, breakSpec func(*aigatewayv1alpha1.AIGatewaySpec), expectedInError string) {
			ns := newTestNamespace(ctx)
			gw := &aigatewayv1alpha1.AIGateway{
				ObjectMeta: metav1.ObjectMeta{
					Name:      name,
					Namespace: ns,
				},
				Spec: validAIGatewaySpec(),
			}
			breakSpec(&gw.Spec)

			err := k8sClient.Create(ctx, gw)
			Expect(err).To(HaveOccurred(), "the apiserver should have rejected this AIGateway")
			Expect(apierrors.IsInvalid(err)).To(BeTrue(),
				"expected a validation error, got: %v", err)
			Expect(err.Error()).To(ContainSubstring(expectedInError),
				"error message should contain expected validation reason")
		},
		// Schema validations (JSON schema constraints)
		Entry("endpoint port out of range (default 0)", "no-endpoint",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Endpoint = aigatewayv1alpha1.EndpointSpec{}
			},
			"spec.endpoint.port in body should be greater than or equal to 1"),
		Entry("endpoint port out of range (explicitly 0)", "port-zero",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Endpoint.Port = 0
			},
			"spec.endpoint.port in body should be greater than or equal to 1"),
		Entry("endpoint port out of range (>65535)", "port-too-high",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Endpoint.Port = 70000
			},
			"spec.endpoint.port in body should be less than or equal to 65535"),
		Entry("invalid authentication type", "invalid-auth-type",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
					Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
						{Type: aigatewayv1alpha1.IdentityType("SAML")},
					},
				}
			},
			"Unsupported value: \"SAML\""),
		Entry("duplicate authentication types", "duplicate-auth-types",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
					Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
						{Type: aigatewayv1alpha1.IdentityTypeOIDC},
						{Type: aigatewayv1alpha1.IdentityTypeOIDC},
					},
				}
			},
			"Duplicate value"),
		Entry("runtime replicas below minimum (negative)", "replicas-negative",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Runtime = &aigatewayv1alpha1.RuntimeSpec{Replicas: new(int32(-1))}
			},
			"spec.runtime.replicas in body should be greater than or equal to 1"),
		Entry("runtime replicas explicitly zero", "replicas-zero",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Runtime = &aigatewayv1alpha1.RuntimeSpec{Replicas: new(int32(0))}
			},
			"spec.runtime.replicas in body should be greater than or equal to 1"),
		Entry("refreshSeconds below minimum", "refresh-seconds-zero",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
					Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
						{Type: aigatewayv1alpha1.IdentityTypeAPIKey, RefreshSeconds: new(int32(0))},
					},
				}
			},
			"spec.governance.authentication[0].refreshSeconds in body should be greater than or equal to 1"),
		Entry("maxStalenessSeconds below minimum", "staleness-negative",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
					Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
						{Type: aigatewayv1alpha1.IdentityTypeAPIKey, MaxStalenessSeconds: new(int32(-1))},
					},
				}
			},
			"spec.governance.authentication[0].maxStalenessSeconds in body should be greater than or equal to 1"),
		// CEL validations (x-kubernetes-validations rules)
		Entry("neither inference nor mcp configured", "no-capability",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Inference = nil
				spec.MCP = nil
			},
			"at least one of inference or mcp must be configured"),
		Entry("inference selected but no API chosen", "inference-no-api",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Inference = &aigatewayv1alpha1.InferenceSpec{}
				spec.MCP = nil
			},
			"at least one API (chatCompletions or responses) must be selected"),
		Entry("TLS certificateRef with empty name", "tls-empty-cert-name",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Endpoint.TLS = &aigatewayv1alpha1.EndpointTLSSpec{
					CertificateRef: aigatewayv1alpha1.LocalObjectReference{Name: ""},
				}
			},
			"should be at least 1 chars long"),
		Entry("conversationState with an empty secret name", "conv-state-empty-name",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Inference = &aigatewayv1alpha1.InferenceSpec{
					Responses: &aigatewayv1alpha1.ResponsesSpec{
						ConversationState: &aigatewayv1alpha1.ConversationStateSpec{
							SecretRef: aigatewayv1alpha1.LocalObjectReference{Name: ""},
						},
					},
				}
			},
			"spec.inference.responses.conversationState.secretRef.name"),
		Entry("tenantRef name that is not a DNS subdomain", "tenant-ref-bad-name",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.TenantRef = aigatewayv1alpha1.LocalObjectReference{Name: "Team_Alpha"}
			},
			"spec.tenantRef.name in body should match"),
		Entry("keySource secretRef key with an illegal character", "key-source-bad-key",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
					Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
						{
							Type: aigatewayv1alpha1.IdentityTypeAPIKey,
							KeySource: &aigatewayv1alpha1.APIKeySource{
								SecretRef: aigatewayv1alpha1.LocalSecretKeyReference{
									LocalObjectReference: aigatewayv1alpha1.LocalObjectReference{
										Name: "consumer-keys",
									},
									Key: "keys/api",
								},
							},
						},
					},
				}
			},
			"spec.governance.authentication[0].keySource.secretRef.key in body should match"),
		Entry("keySource secretRef with an empty key", "key-source-empty-key",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
					Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
						{
							Type: aigatewayv1alpha1.IdentityTypeAPIKey,
							KeySource: &aigatewayv1alpha1.APIKeySource{
								SecretRef: aigatewayv1alpha1.LocalSecretKeyReference{
									LocalObjectReference: aigatewayv1alpha1.LocalObjectReference{
										Name: "consumer-keys",
									},
									Key: "",
								},
							},
						},
					},
				}
			},
			"spec.governance.authentication[0].keySource.secretRef.key"),
	)

	DescribeTable("accepts a valid capability combination",
		func(ctx context.Context, name string, setCapabilities func(*aigatewayv1alpha1.AIGatewaySpec)) {
			ns := newTestNamespace(ctx)
			gw := &aigatewayv1alpha1.AIGateway{
				ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
				Spec:       validAIGatewaySpec(),
			}
			setCapabilities(&gw.Spec)

			Expect(k8sClient.Create(ctx, gw)).To(Succeed())
			DeferCleanup(func(ctx context.Context) error {
				return client.IgnoreNotFound(k8sClient.Delete(ctx, gw))
			})
		},
		Entry("mcp only, inference absent", "mcp-only",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Inference = nil
				spec.MCP = &aigatewayv1alpha1.MCPSpec{}
			}),
		Entry("responses only, chatCompletions absent", "responses-only",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Inference = &aigatewayv1alpha1.InferenceSpec{
					Responses: &aigatewayv1alpha1.ResponsesSpec{},
				}
			}),
		Entry("chatCompletions only, responses absent", "chat-only",
			func(spec *aigatewayv1alpha1.AIGatewaySpec) {
				spec.Inference = &aigatewayv1alpha1.InferenceSpec{
					ChatCompletions: &aigatewayv1alpha1.ChatCompletionsSpec{},
				}
			}),
	)

	It("persists nested secret references", func(ctx context.Context) {
		ns := newTestNamespace(ctx)
		spec := validAIGatewaySpec()
		spec.Inference.Responses = &aigatewayv1alpha1.ResponsesSpec{
			ConversationState: &aigatewayv1alpha1.ConversationStateSpec{
				SecretRef: aigatewayv1alpha1.LocalObjectReference{Name: "responses-state"},
			},
		}
		spec.Governance = &aigatewayv1alpha1.GovernanceSpec{
			Authentication: []aigatewayv1alpha1.IdentityTypeSpec{
				{
					Type: aigatewayv1alpha1.IdentityTypeAPIKey,
					KeySource: &aigatewayv1alpha1.APIKeySource{
						SecretRef: aigatewayv1alpha1.LocalSecretKeyReference{
							LocalObjectReference: aigatewayv1alpha1.LocalObjectReference{
								Name: "consumer-keys",
							},
							Key: "keys.yaml",
						},
					},
				},
			},
		}
		gw := &aigatewayv1alpha1.AIGateway{
			ObjectMeta: metav1.ObjectMeta{Name: "nested-secret-refs", Namespace: ns},
			Spec:       spec,
		}

		Expect(k8sClient.Create(ctx, gw)).To(Succeed())
		DeferCleanup(func(ctx context.Context) error {
			return client.IgnoreNotFound(k8sClient.Delete(ctx, gw))
		})

		got := &aigatewayv1alpha1.AIGateway{}
		Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(gw), got)).To(Succeed())
		Expect(got.Spec.Inference.Responses.ConversationState).NotTo(BeNil())
		Expect(got.Spec.Inference.Responses.ConversationState.SecretRef.Name).To(Equal("responses-state"))
		keySource := got.Spec.Governance.Authentication[0].KeySource
		Expect(keySource).NotTo(BeNil())
		Expect(keySource.SecretRef.Name).To(Equal("consumer-keys"))
		Expect(keySource.SecretRef.Key).To(Equal("keys.yaml"))
	})
})
