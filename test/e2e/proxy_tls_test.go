package e2e

import (
	"context"
	"crypto/rand"
	"crypto/rsa"
	"crypto/x509"
	"crypto/x509/pkix"
	"encoding/pem"
	"fmt"
	"math/big"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

const proxyTLSPort = int32(18443)

var _ = Describe("TLS-enabled Proxy", func() {
	It("serves HTTPS validated against the referenced certificate and refuses plaintext", SpecTimeout(5*time.Minute),
		func(ctx context.Context) {
			image := os.Getenv("PRAXIS_E2E_IMAGE")
			Expect(image).NotTo(BeEmpty(), "set PRAXIS_E2E_IMAGE to a verified Praxis runtime image available to the cluster")
			clientImage := os.Getenv("E2E_CLIENT_IMAGE")
			Expect(clientImage).NotTo(BeEmpty(), "set E2E_CLIENT_IMAGE to an HTTP client image available to the cluster")

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "proxy-tls-"}}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			DeferCleanup(func(ctx context.Context) error {
				if CurrentSpecReport().Failed() {
					dumpProxyDiagnostics(ctx, namespace.Name)
				}
				return client.IgnoreNotFound(k8sClient.Delete(ctx, namespace))
			})

			// The client validates the served certificate against the
			// CA that signed it, so a green request proves the listener
			// terminated TLS with the referenced Secret's material
			// rather than any certificate the runtime may have found.
			serviceDNS := fmt.Sprintf("tls-gateway.%s.svc", namespace.Name)
			caPEM, certPEM, keyPEM := makeServerCertificate([]string{
				serviceDNS,
				fmt.Sprintf("tls-gateway.%s", namespace.Name),
				"tls-gateway",
			})

			certSecret := &corev1.Secret{
				ObjectMeta: metav1.ObjectMeta{Name: "tls-gateway-cert", Namespace: namespace.Name},
				Type:       corev1.SecretTypeTLS,
				Data: map[string][]byte{
					corev1.TLSCertKey:       certPEM,
					corev1.TLSPrivateKeyKey: keyPEM,
				},
			}
			Expect(k8sClient.Create(ctx, certSecret)).To(Succeed())

			proxy := &proxyv1alpha1.Proxy{
				ObjectMeta: metav1.ObjectMeta{Name: "tls-gateway", Namespace: namespace.Name},
				Spec: proxyv1alpha1.ProxySpec{
					Image: image, Replicas: new(int32(1)),
					Listeners: []proxyv1alpha1.PraxisListener{{
						Name: "https", Port: proxyTLSPort, FilterChains: []string{"main"},
						TLS: &proxyv1alpha1.PraxisListenerTLS{
							CertificateRef: proxyv1alpha1.PraxisLocalReference{Name: certSecret.Name},
						},
					}},
					FilterChains: []proxyv1alpha1.ProxyFilterChain{{
						Name: "main",
						Filters: []proxyv1alpha1.PraxisFilterEntry{{
							Name: "response", InlineFilter: staticResponse("v1"),
						}},
					}},
				},
			}
			Expect(k8sClient.Create(ctx, proxy)).To(Succeed())
			key := client.ObjectKeyFromObject(proxy)

			By("waiting for a running TLS-enabled Proxy")
			Eventually(func(gom Gomega, ctx context.Context) {
				expectRuntimeReady(gom, ctx, key)
				service := &corev1.Service{}
				gom.Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
				var exposesTLSPort bool
				for _, port := range service.Spec.Ports {
					if port.Name == "https" && port.Port == proxyTLSPort {
						exposesTLSPort = true
					}
				}
				gom.Expect(exposesTLSPort).To(BeTrue(),
					"the Service must expose the TLS listener port, got: %+v", service.Spec.Ports)
			}).WithContext(ctx).WithTimeout(proxyStartupTimeout).WithPolling(proxyPollInterval).Should(Succeed())

			By("serving HTTPS the client validates against the referenced certificate")
			httpsURL := "https://" + serviceDNS + ":" + strconv.FormatInt(int64(proxyTLSPort), 10) + "/"
			phase, logs := runCurlPod(ctx, namespace.Name, clientImage, caPEM,
				"curl", "-fsS", "--max-time", "10", "--connect-timeout", "5",
				"--cacert", curlCAPath, httpsURL)
			Expect(phase).To(Equal(corev1.PodSucceeded), "validated HTTPS request must succeed, curl output: %s", logs)
			Expect(logs).To(Equal("v1"))

			By("refusing plaintext HTTP on the TLS listener")
			httpURL := "http://" + serviceDNS + ":" + strconv.FormatInt(int64(proxyTLSPort), 10) + "/"
			phase, logs = runCurlPod(ctx, namespace.Name, clientImage, nil,
				"curl", "-fsS", "--max-time", "10", "--connect-timeout", "5", httpURL)
			Expect(phase).To(Equal(corev1.PodFailed),
				"a plaintext request must not be served on the TLS listener, curl output: %s", logs)
		})
})

const curlCAPath = "/etc/ca/ca.pem"

// makeServerCertificate generates a private CA and a server certificate
// valid for dnsNames, returning the CA certificate, the server certificate,
// and the server private key, all PEM-encoded.
func makeServerCertificate(dnsNames []string) (caPEM, certPEM, keyPEM []byte) {
	GinkgoHelper()

	caKey, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	caTemplate := &x509.Certificate{
		SerialNumber:          big.NewInt(1),
		Subject:               pkix.Name{CommonName: "proxy-e2e-ca"},
		NotBefore:             time.Now().Add(-time.Hour),
		NotAfter:              time.Now().Add(24 * time.Hour),
		KeyUsage:              x509.KeyUsageCertSign,
		BasicConstraintsValid: true,
		IsCA:                  true,
	}
	caDER, err := x509.CreateCertificate(rand.Reader, caTemplate, caTemplate, &caKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())
	caCert, err := x509.ParseCertificate(caDER)
	Expect(err).NotTo(HaveOccurred())

	serverKey, err := rsa.GenerateKey(rand.Reader, 2048)
	Expect(err).NotTo(HaveOccurred())
	serverTemplate := &x509.Certificate{
		SerialNumber: big.NewInt(2),
		Subject:      pkix.Name{CommonName: dnsNames[0]},
		DNSNames:     dnsNames,
		NotBefore:    time.Now().Add(-time.Hour),
		NotAfter:     time.Now().Add(24 * time.Hour),
		KeyUsage:     x509.KeyUsageDigitalSignature | x509.KeyUsageKeyEncipherment,
		ExtKeyUsage:  []x509.ExtKeyUsage{x509.ExtKeyUsageServerAuth},
	}
	serverDER, err := x509.CreateCertificate(rand.Reader, serverTemplate, caCert, &serverKey.PublicKey, caKey)
	Expect(err).NotTo(HaveOccurred())

	caPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: caDER})
	certPEM = pem.EncodeToMemory(&pem.Block{Type: "CERTIFICATE", Bytes: serverDER})
	keyPEM = pem.EncodeToMemory(&pem.Block{Type: "RSA PRIVATE KEY", Bytes: x509.MarshalPKCS1PrivateKey(serverKey)})
	return caPEM, certPEM, keyPEM
}

// runCurlPod runs a client Pod executing command in the pinned client
// image and returns the Pod's terminal phase and container logs. A non-nil
// caPEM is mounted at curlCAPath for the command to validate TLS with.
func runCurlPod(
	ctx context.Context, namespace, image string, caPEM []byte, command ...string,
) (corev1.PodPhase, string) {
	GinkgoHelper()

	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "curl-", Namespace: namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "curl",
				Image:   image,
				Command: command,
			}},
		},
	}
	if caPEM != nil {
		caConfigMap := &corev1.ConfigMap{
			ObjectMeta: metav1.ObjectMeta{GenerateName: "curl-ca-", Namespace: namespace},
			Data:       map[string]string{"ca.pem": string(caPEM)},
		}
		Expect(k8sClient.Create(ctx, caConfigMap)).To(Succeed())
		pod.Spec.Volumes = []corev1.Volume{{
			Name: "ca",
			VolumeSource: corev1.VolumeSource{
				ConfigMap: &corev1.ConfigMapVolumeSource{
					LocalObjectReference: corev1.LocalObjectReference{Name: caConfigMap.Name},
				},
			},
		}}
		pod.Spec.Containers[0].VolumeMounts = []corev1.VolumeMount{{
			Name:      "ca",
			MountPath: "/etc/ca",
			ReadOnly:  true,
		}}
	}

	Expect(k8sClient.Create(ctx, pod)).To(Succeed())
	Eventually(func(gom Gomega, ctx context.Context) {
		got := &corev1.Pod{}
		gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), got)).To(Succeed())
		gom.Expect(got.Status.Phase).To(SatisfyAny(Equal(corev1.PodSucceeded), Equal(corev1.PodFailed)),
			"client pod must reach a terminal phase, pod: %s", podSummary(got))
	}).WithContext(ctx).WithTimeout(2 * time.Minute).WithPolling(proxyPollInterval).Should(Succeed())

	got := &corev1.Pod{}
	Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), got)).To(Succeed())
	logs, err := coreClient.Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "curl"}).DoRaw(ctx)
	Expect(err).NotTo(HaveOccurred(), "read client pod logs")
	return got.Status.Phase, string(logs)
}
