package e2e

import (
	"context"
	"fmt"
	"net"
	"os"
	"strconv"
	"time"

	. "github.com/onsi/ginkgo/v2"
	. "github.com/onsi/gomega"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"sigs.k8s.io/controller-runtime/pkg/client"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

// This spec runs only on clusters whose primary Service family is IPv6
// (make kind-up-ipv6). It drives real ClusterIP traffic through the
// proxy's Service, which the API-server Service proxy used by the other
// specs cannot exercise: that proxy dials endpoints directly and
// bypasses kube-proxy's ClusterIP family routing.
var _ = Describe("Proxy on an IPv6-primary cluster", func() {
	It("serves traffic through a Service compatible with IPv4 listener binds", SpecTimeout(10*time.Minute),
		func(ctx context.Context) {
			primary, err := clusterPrimaryIPFamily(ctx)
			Expect(err).NotTo(HaveOccurred())
			if primary != corev1.IPv6Protocol {
				Skip(fmt.Sprintf("cluster primary Service family is %s, not IPv6; run make kind-up-ipv6", primary))
			}
			image := os.Getenv("PRAXIS_E2E_IMAGE")
			Expect(image).NotTo(BeEmpty(), "set PRAXIS_E2E_IMAGE to a verified Praxis runtime image available to the cluster")
			clientImage := os.Getenv("E2E_CLIENT_IMAGE")
			Expect(clientImage).NotTo(BeEmpty(), "set E2E_CLIENT_IMAGE to an HTTP client image available to the cluster")

			namespace := &corev1.Namespace{ObjectMeta: metav1.ObjectMeta{GenerateName: "proxy-ipv6-"}}
			Expect(k8sClient.Create(ctx, namespace)).To(Succeed())
			DeferCleanup(func(ctx context.Context) error {
				if CurrentSpecReport().Failed() {
					dumpProxyDiagnostics(ctx, namespace.Name)
				}
				return client.IgnoreNotFound(k8sClient.Delete(ctx, namespace))
			})

			// The listener uses the spec default bind (the IPv4
			// wildcard), so the controller must publish an IPv4-family
			// Service even though the cluster's primary family is
			// IPv6; a defaulted Service would be IPv6 and its traffic
			// unreachable for the bind.
			proxy := &proxyv1alpha1.Proxy{
				ObjectMeta: metav1.ObjectMeta{Name: "runtime", Namespace: namespace.Name},
				Spec: proxyv1alpha1.ProxySpec{
					Image: image, Replicas: new(int32(1)),
					Listeners: []proxyv1alpha1.PraxisListener{{
						Name: "http", Port: proxyInitialPort, FilterChains: []string{"main"},
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

			By("waiting for a running Proxy and an IPv4-family Service")
			var clusterIP string
			Eventually(func(gom Gomega, ctx context.Context) {
				expectRuntimeReady(gom, ctx, key)
				service := &corev1.Service{}
				gom.Expect(k8sClient.Get(ctx, key, service)).To(Succeed())
				gom.Expect(service.Spec.IPFamilies).To(Equal([]corev1.IPFamily{corev1.IPv4Protocol}),
					"IPv4-bound listeners must not be exposed on the cluster's IPv6 family")
				clusterIP = service.Spec.ClusterIP
				gom.Expect(clusterIP).NotTo(BeEmpty())
			}).WithContext(ctx).WithTimeout(proxyStartupTimeout).WithPolling(proxyPollInterval).Should(Succeed())
			Expect(net.ParseIP(clusterIP).To4()).NotTo(BeNil(), "ClusterIP should be IPv4, got %s", clusterIP)

			By("reaching the proxy through the Service ClusterIP")
			body, err := curlServiceBody(ctx, namespace.Name, clientImage,
				net.JoinHostPort(clusterIP, strconv.FormatInt(int64(proxyInitialPort), 10)))
			Expect(err).NotTo(HaveOccurred())
			Expect(body).To(Equal("v1"))
		})
})

// clusterPrimaryIPFamily reports the primary Service family of the
// cluster. The kubernetes Service's ClusterIP is the first address of
// the service CIDR, so its family is the family Services default to.
func clusterPrimaryIPFamily(ctx context.Context) (corev1.IPFamily, error) {
	service := &corev1.Service{}
	if err := k8sClient.Get(ctx, types.NamespacedName{Namespace: "default", Name: "kubernetes"}, service); err != nil {
		return "", fmt.Errorf("get kubernetes service: %w", err)
	}
	ip := net.ParseIP(service.Spec.ClusterIP)
	if ip == nil {
		return "", fmt.Errorf("kubernetes service ClusterIP %q is not an IP", service.Spec.ClusterIP)
	}
	if ip.To4() != nil {
		return corev1.IPv4Protocol, nil
	}
	return corev1.IPv6Protocol, nil
}

// curlServiceBody runs a client Pod that requests url and returns the
// response body from its output.
func curlServiceBody(ctx context.Context, namespace, image, hostPort string) (string, error) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{GenerateName: "curl-", Namespace: namespace},
		Spec: corev1.PodSpec{
			RestartPolicy: corev1.RestartPolicyNever,
			Containers: []corev1.Container{{
				Name:    "curl",
				Image:   image,
				Command: []string{"curl", "-fsS", "--max-time", "10", "--connect-timeout", "5", "http://" + hostPort + "/"},
			}},
		},
	}
	if err := k8sClient.Create(ctx, pod); err != nil {
		return "", fmt.Errorf("create client pod: %w", err)
	}
	DeferCleanup(func(ctx context.Context) error {
		return client.IgnoreNotFound(k8sClient.Delete(ctx, pod))
	})
	Eventually(func(gom Gomega, ctx context.Context) {
		got := &corev1.Pod{}
		gom.Expect(k8sClient.Get(ctx, client.ObjectKeyFromObject(pod), got)).To(Succeed())
		gom.Expect(got.Status.Phase).To(Equal(corev1.PodSucceeded), "client pod: %s", podSummary(got))
	}).WithContext(ctx).WithTimeout(proxyStartupTimeout).WithPolling(proxyPollInterval).Should(Succeed())
	logs, err := coreClient.Pods(namespace).GetLogs(pod.Name, &corev1.PodLogOptions{Container: "curl"}).DoRaw(ctx)
	if err != nil {
		return "", fmt.Errorf("read client pod logs: %w", err)
	}
	return string(logs), nil
}

// podSummary renders a pod's phase and container states for failure
// messages.
func podSummary(pod *corev1.Pod) string {
	summary := fmt.Sprintf("phase %s", pod.Status.Phase)
	for _, status := range pod.Status.ContainerStatuses {
		state := "waiting"
		if status.State.Running != nil {
			state = "running"
		}
		if status.State.Terminated != nil {
			state = fmt.Sprintf("terminated(%d): %s", status.State.Terminated.ExitCode, status.State.Terminated.Reason)
		}
		if status.State.Waiting != nil && status.State.Waiting.Reason != "" {
			state = fmt.Sprintf("waiting(%s)", status.State.Waiting.Reason)
		}
		summary += fmt.Sprintf(", container %s %s", status.Name, state)
	}
	return summary
}
