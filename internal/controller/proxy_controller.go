package controller

import (
	"context"
	"fmt"
	"net"
	"slices"
	"strings"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/util/intstr"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/controller/controllerutil"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

const (
	containerName                 = "praxis"
	defaultTerminationGracePeriod = int64(30)
	// volumeDefaultMode mirrors the mode the API server defaults
	// ConfigMap and Secret volumes to when it is left unset.
	volumeDefaultMode = 0o644
	// tlsCertificateKey and tlsPrivateKeyKey are the keys a
	// kubernetes.io/tls Secret carries; the Deployment mounts them at
	// the paths the rendered config points Praxis at.
	tlsCertificateKey = "tls.crt"
	tlsPrivateKeyKey  = "tls.key"
	// unresolvedRefRequeueInterval polls for referenced objects that do
	// not exist yet: referenced Secrets are not owned by the proxy, so
	// their creation does not trigger a reconcile.
	unresolvedRefRequeueInterval = 30 * time.Second
)

type ProxyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=praxis.io,resources=proxies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=praxis.io,resources=proxies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=praxis.io,resources=proxies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=secrets,verbs=get;list;watch
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=praxis.io,resources=policies,verbs=get;list;watch

func (r *ProxyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var proxy proxyv1alpha1.Proxy
	if err := r.Get(ctx, req.NamespacedName, &proxy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	patch := client.MergeFrom(proxy.DeepCopy())

	logger.V(1).Info("reconciling proxy", "name", req.Name, "namespace", req.Namespace)

	proxy.Status.ObservedGeneration = proxy.Generation

	if validationErrs := validateProxySpec(&proxy.Spec); len(validationErrs) > 0 {
		message := joinValidationErrors(validationErrs)
		for _, conditionType := range []string{
			proxyv1alpha1.ConditionTypeAccepted, proxyv1alpha1.ConditionTypeResolvedRefs,
			proxyv1alpha1.ConditionTypeProgrammed, proxyv1alpha1.ConditionTypeReady,
		} {
			setCondition(&proxy.Status.Conditions, conditionType,
				metav1.ConditionFalse, "ValidationError", message, proxy.Generation)
		}
		for i := range proxy.Status.Listeners {
			setCondition(&proxy.Status.Listeners[i].Conditions, proxyv1alpha1.ConditionTypeProgrammed,
				metav1.ConditionFalse, "ValidationError", message, proxy.Generation)
		}
		if err := r.Status().Patch(ctx, &proxy, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted,
		metav1.ConditionTrue, "Valid", "Proxy specification is valid", proxy.Generation)

	// The pod network's primary family decides which wildcard the
	// readiness path can bind and which spec listeners the probe can
	// reach: an IPv6 wildcard needs IPv6 in the pod netns, which an
	// IPv4-primary network cannot promise. An absent signal (no
	// default/kubernetes Service) keeps the family-agnostic behavior.
	primary, err := clusterPrimaryIPFamily(ctx, r.Client)
	if err != nil {
		return ctrl.Result{}, fmt.Errorf("read cluster primary IP family: %w", err)
	}

	rendered, err := renderProxyConfig(ctx, r.Client, &proxy.Spec, proxy.Namespace, primary)
	if err != nil {
		// Render errors quote spec content (filter names, referenced
		// documents), which validation does not length-bound, so the
		// message is truncated to what the API server accepts in a
		// condition before it reaches status.
		message := truncateToRuneBoundary(err.Error(), maxConditionMessageLen)
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted,
			metav1.ConditionFalse, "ConfigRenderError", message, proxy.Generation)
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs,
			metav1.ConditionFalse, "ConfigRenderError", message, proxy.Generation)
		if patchErr := r.Status().Patch(ctx, &proxy, patch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	// Reference resolution completes before anything is published.
	// Rendering resolved the policy references — their documents are
	// part of the rendered config — and the Secrets the Deployment
	// mounts and projects are resolved here. An unresolved reference
	// is reported in status rather than published as a pod that can
	// never start: the Deployment would block on a missing Secret
	// volume or env key. Referenced Secrets are not owned by the
	// proxy, so their creation cannot trigger a reconcile; the
	// unresolved path requeues to poll for them.
	if unresolved := r.resolveReferences(ctx, &proxy); len(unresolved) > 0 {
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs,
			metav1.ConditionFalse, "UnresolvedRefs",
			truncateToRuneBoundary(strings.Join(unresolved, "; "), maxConditionMessageLen),
			proxy.Generation)
		if patchErr := r.Status().Patch(ctx, &proxy, patch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{RequeueAfter: unresolvedRefRequeueInterval}, nil
	}
	setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs,
		metav1.ConditionTrue, "ResolvedRefs", "All references resolved", proxy.Generation)

	// The policy documents collected while rendering are carried through
	// the controller for the publication work that owns them (#19, #20).
	logger.V(1).Info("rendered praxis configuration",
		"policyDocuments", len(rendered.PolicyDocuments))
	configData := rendered.Config

	revision, err := computeRevision(configData, proxy.Spec.Image, proxy.Spec.SecretEnv)
	if err != nil {
		return ctrl.Result{}, err
	}
	proxy.Status.DesiredRevision = revision

	// A Service's allocated families are immutable, so a bind change
	// that would change them — dropping a family the Service retains
	// or adding one it lacks — can never be published while that
	// Service exists. Reject the change before the ConfigMap and
	// Deployment are updated, and report it in status rather than
	// failing after they are already published.
	var existingSvc corev1.Service
	if err := r.Get(ctx, client.ObjectKeyFromObject(&proxy), &existingSvc); err != nil && !apierrors.IsNotFound(err) {
		return ctrl.Result{}, fmt.Errorf("get service: %w", err)
	}
	if conflict := retainedFamilyConflict(&proxy, &existingSvc); conflict != nil {
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeProgrammed,
			metav1.ConditionFalse, "ServiceFamilyConflict", conflict.Error(), proxy.Generation)
		if patchErr := r.Status().Patch(ctx, &proxy, patch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}

	if err := r.reconcileConfigMap(ctx, &proxy, configData); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile configmap: %w", err)
	}

	startupRevision, err := computeStartupRevision(&proxy.Spec)
	if err != nil {
		return ctrl.Result{}, err
	}
	if err := r.reconcileDeployment(ctx, &proxy, startupRevision, primary); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile deployment: %w", err)
	}

	if err := r.reconcileService(ctx, &proxy); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile service: %w", err)
	}

	// PublishedRevision is assigned only after both the Deployment and
	// Service writes have succeeded — the error returns above skip this
	// assignment — so it records completed publishes, never partial
	// ones. DesiredRevision, set before the writes, records intent.
	proxy.Status.PublishedRevision = revision
	setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeProgrammed,
		metav1.ConditionTrue, "ConfigPublished", "Configuration rendered to ConfigMap", proxy.Generation)

	proxy.Status.Listeners = buildListenerStatus(&proxy)
	if err := r.updateReadyStatus(ctx, &proxy); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Status().Patch(ctx, &proxy, patch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// resolveReferences reports the spec references that do not resolve
// against the cluster: the TLS certificate Secret each listener mounts
// and the Secret keys projected as environment variables. Policy
// references are resolved while rendering the configuration, because
// their documents are part of the rendered config. An empty return
// means every reference resolves — trivially so for inline-only
// proxies, which reference nothing.
func (r *ProxyReconciler) resolveReferences(ctx context.Context, proxy *proxyv1alpha1.Proxy) []string {
	var unresolved []string
	for i := range proxy.Spec.Listeners {
		listener := &proxy.Spec.Listeners[i]
		if listener.TLS == nil {
			continue
		}
		name := listener.TLS.CertificateRef.Name
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: proxy.Namespace, Name: name}, secret)
		switch {
		case apierrors.IsNotFound(err):
			unresolved = append(unresolved, fmt.Sprintf("listener %q: TLS certificate secret %q not found", listener.Name, name))
		case err != nil:
			unresolved = append(unresolved, fmt.Sprintf("listener %q: get TLS certificate secret %q: %v", listener.Name, name, err))
		default:
			for _, key := range []string{tlsCertificateKey, tlsPrivateKeyKey} {
				if _, ok := secret.Data[key]; !ok {
					unresolved = append(unresolved, fmt.Sprintf("listener %q: TLS certificate secret %q lacks key %q", listener.Name, name, key))
				}
			}
		}
	}
	for _, env := range proxy.Spec.SecretEnv {
		secret := &corev1.Secret{}
		err := r.Get(ctx, client.ObjectKey{Namespace: proxy.Namespace, Name: env.SecretRef.Name}, secret)
		switch {
		case apierrors.IsNotFound(err):
			unresolved = append(unresolved, fmt.Sprintf("secretEnv %q: secret %q not found", env.Name, env.SecretRef.Name))
		case err != nil:
			unresolved = append(unresolved, fmt.Sprintf("secretEnv %q: get secret %q: %v", env.Name, env.SecretRef.Name, err))
		default:
			if _, ok := secret.Data[env.SecretRef.Key]; !ok {
				unresolved = append(unresolved, fmt.Sprintf("secretEnv %q: secret %q lacks key %q", env.Name, env.SecretRef.Name, env.SecretRef.Key))
			}
		}
	}
	return unresolved
}

func configMapName(proxy *proxyv1alpha1.Proxy) string {
	return proxy.Name + "-config"
}

// ensureControlledBy guards a reconciliation callback against adopting or
// overwriting objects this Proxy does not control. Objects that have not
// been persisted yet (the create path of controllerutil.CreateOrUpdate,
// where the object was never fetched) always pass. An existing object with
// no controller owner, or one controlled by a different controller, yields
// a conflict error and is never written.
func ensureControlledBy(obj client.Object, proxy *proxyv1alpha1.Proxy, resource schema.GroupResource) error {
	created := obj.GetCreationTimestamp()
	if created.IsZero() {
		return nil
	}
	if metav1.IsControlledBy(obj, proxy) {
		return nil
	}
	ownerDesc := "none"
	if ref := metav1.GetControllerOf(obj); ref != nil {
		ownerDesc = fmt.Sprintf("%s %q (uid %s)", ref.Kind, ref.Name, ref.UID)
	}
	return apierrors.NewConflict(resource, obj.GetName(),
		fmt.Errorf("object exists but is not controlled by proxy %s/%s (controller owner: %s); refusing to adopt or overwrite",
			proxy.Namespace, proxy.Name, ownerDesc))
}

// reconcileConfigMap publishes the live configuration at a stable name. Pods
// mount the directory so Kubernetes' atomic projection updates reach Praxis's
// config watcher without replacing the pod template.
func (r *ProxyReconciler) reconcileConfigMap(ctx context.Context, proxy *proxyv1alpha1.Proxy, configData string) error {
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(proxy),
			Namespace: proxy.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		if err := ensureControlledBy(desired, proxy, schema.GroupResource{Group: "", Resource: "configmaps"}); err != nil {
			return err
		}
		if err := controllerutil.SetControllerReference(proxy, desired, r.Scheme); err != nil {
			return err
		}
		desired.Data = map[string]string{configFileName: configData}
		return nil
	})
	return err
}

// startupRevisionAnnotation changes only for settings that Praxis cannot reload.
const startupRevisionAnnotation = "praxis.io/startup-revision"

func (r *ProxyReconciler) reconcileDeployment(ctx context.Context, proxy *proxyv1alpha1.Proxy, revision string, primary corev1.IPFamily) error {
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		if err := ensureControlledBy(desired, proxy, schema.GroupResource{Group: "apps", Resource: "deployments"}); err != nil {
			return err
		}
		if err := controllerutil.SetControllerReference(proxy, desired, r.Scheme); err != nil {
			return err
		}
		replicas := int32(1)
		if proxy.Spec.Replicas != nil {
			replicas = *proxy.Spec.Replicas
		}
		desired.Spec.Replicas = &replicas
		desired.Spec.Selector = &metav1.LabelSelector{
			MatchLabels: proxyLabels(proxy),
		}
		desired.Spec.Template = buildPodTemplate(proxy, revision, primary)
		return nil
	})
	return err
}

func proxyLabels(proxy *proxyv1alpha1.Proxy) map[string]string {
	return map[string]string{
		"app.kubernetes.io/name":       containerName,
		"app.kubernetes.io/instance":   proxy.Name,
		"app.kubernetes.io/managed-by": "gateway-controller",
	}
}

// buildPodTemplate returns the pod template for the Praxis Deployment.
// reconcileDeployment replaces the stored template wholesale, so every
// field the API server defaults on a stored pod template — pull policy
// and termination message on the container; restart policy, DNS
// policy, scheduler, termination grace period, security context, and
// volume defaultMode on the pod — is rendered here with the value the
// server defaults it to. An unrendered default would differ from the
// stored object on every reconcile and churn Deployment updates;
// TestReconcileProxy_UnchangedReconcileSkipsDeploymentUpdate simulates
// the server's defaulting and fails if an unchanged reconcile writes.
func buildPodTemplate(proxy *proxyv1alpha1.Proxy, revision string, primary corev1.IPFamily) corev1.PodTemplateSpec {
	volumes, mounts := buildVolumes(proxy)
	gracePeriod := defaultTerminationGracePeriod
	container := corev1.Container{
		Name:                     containerName,
		Image:                    proxy.Spec.Image,
		Command:                  []string{containerName},
		Args:                     []string{"--config", configMountPath + "/" + configFileName},
		Ports:                    buildContainerPorts(proxy, primary),
		VolumeMounts:             mounts,
		Env:                      buildEnvVars(proxy),
		ReadinessProbe:           buildReadinessProbe(proxy, primary),
		ImagePullPolicy:          corev1.PullIfNotPresent,
		TerminationMessagePath:   corev1.TerminationMessagePathDefault,
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: proxyLabels(proxy),
			Annotations: map[string]string{
				startupRevisionAnnotation: revision,
			},
		},
		Spec: corev1.PodSpec{
			Containers:                    []corev1.Container{container},
			Volumes:                       volumes,
			RestartPolicy:                 corev1.RestartPolicyAlways,
			DNSPolicy:                     corev1.DNSClusterFirst,
			TerminationGracePeriodSeconds: &gracePeriod,
			SchedulerName:                 corev1.DefaultSchedulerName,
			// The API server defaults an unset pod securityContext to
			// the empty value on the stored template; emit it explicitly
			// so an unchanged reconcile does not clear it and send a
			// Deployment update the API server only absorbs as a no-op.
			SecurityContext: &corev1.PodSecurityContext{},
		},
	}
}

func buildVolumes(proxy *proxyv1alpha1.Proxy) ([]corev1.Volume, []corev1.VolumeMount) {
	// The API server defaults an unset ConfigMap or Secret volume
	// defaultMode to 0644 on the stored template; emit it explicitly so
	// an unchanged reconcile does not clear it and send a Deployment
	// update the API server only absorbs as a no-op.
	defaultMode := new(int32(volumeDefaultMode))
	volumes := []corev1.Volume{{
		Name: configVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(proxy)},
				DefaultMode:          defaultMode,
			},
		},
	}}
	mounts := []corev1.VolumeMount{{
		Name:      configVolumeName,
		MountPath: configMountPath,
		ReadOnly:  true,
	}}

	for _, listener := range proxy.Spec.Listeners {
		if listener.TLS == nil {
			continue
		}
		volName := "tls-" + listener.Name
		volumes = append(volumes, corev1.Volume{
			Name: volName,
			VolumeSource: corev1.VolumeSource{
				Secret: &corev1.SecretVolumeSource{
					SecretName:  listener.TLS.CertificateRef.Name,
					DefaultMode: defaultMode,
				},
			},
		})
		mounts = append(mounts, corev1.VolumeMount{
			Name:      volName,
			MountPath: fmt.Sprintf("%s/%s", tlsMountPrefix, listener.Name),
			ReadOnly:  true,
		})
	}
	return volumes, mounts
}

// buildReadinessProbe gates pod readiness on Praxis actually serving.
// The runtime has no dedicated health endpoint, so the probe is a TCP
// connect against a listener the kubelet can reach through the pod IP
// (it dials the pod IP from the node): the first listener bound to a
// wildcard of the pod network's primary family. Listeners bound to
// specific addresses such as loopback, or to a wildcard of a family the
// pod IP does not have, cannot be reached, so the controller renders a
// dedicated wildcard-bound readiness listener into the config and the
// probe targets that instead. The port is referenced by name in both
// cases, so the wiring survives port renumbering; a replacement pod
// therefore stays unready during startup until Praxis has bound the
// listener and can accept traffic.
func buildReadinessProbe(proxy *proxyv1alpha1.Proxy, primary corev1.IPFamily) *corev1.Probe {
	if len(proxy.Spec.Listeners) == 0 {
		return nil
	}
	portName := readinessListenerName(&proxy.Spec)
	if reachable := podIPListener(&proxy.Spec, primary); reachable != nil {
		portName = reachable.Name
	}
	const (
		initialDelaySeconds = 5
		periodSeconds       = 5
		timeoutSeconds      = 2
		successThreshold    = 1
		failureThreshold    = 3
	)
	return &corev1.Probe{
		ProbeHandler: corev1.ProbeHandler{
			TCPSocket: &corev1.TCPSocketAction{
				Port: intstr.FromString(portName),
			},
		},
		InitialDelaySeconds: initialDelaySeconds,
		PeriodSeconds:       periodSeconds,
		TimeoutSeconds:      timeoutSeconds,
		SuccessThreshold:    successThreshold,
		FailureThreshold:    failureThreshold,
	}
}

func buildContainerPorts(proxy *proxyv1alpha1.Proxy, primary corev1.IPFamily) []corev1.ContainerPort {
	ports := make([]corev1.ContainerPort, 0, len(proxy.Spec.Listeners))
	for _, listener := range proxy.Spec.Listeners {
		ports = append(ports, corev1.ContainerPort{
			Name:          listener.Name,
			ContainerPort: listener.Port,
			Protocol:      corev1.ProtocolTCP,
		})
	}
	// The readiness probe targets the controller-rendered readiness
	// listener when no spec listener is reachable through the pod IP;
	// declare its port so the probe's named reference resolves.
	if podIPListener(&proxy.Spec, primary) == nil {
		ports = append(ports, corev1.ContainerPort{
			Name:          readinessListenerName(&proxy.Spec),
			ContainerPort: readinessPort(&proxy.Spec),
			Protocol:      corev1.ProtocolTCP,
		})
	}
	return ports
}

func buildEnvVars(proxy *proxyv1alpha1.Proxy) []corev1.EnvVar {
	if len(proxy.Spec.SecretEnv) == 0 {
		return nil
	}
	envVars := make([]corev1.EnvVar, 0, len(proxy.Spec.SecretEnv))
	for _, secretEnv := range proxy.Spec.SecretEnv {
		envVars = append(envVars, corev1.EnvVar{
			Name: secretEnv.Name,
			ValueFrom: &corev1.EnvVarSource{
				SecretKeyRef: &corev1.SecretKeySelector{
					LocalObjectReference: corev1.LocalObjectReference{Name: secretEnv.SecretRef.Name},
					Key:                  secretEnv.SecretRef.Key,
				},
			},
		})
	}
	return envVars
}

func (r *ProxyReconciler) reconcileService(ctx context.Context, proxy *proxyv1alpha1.Proxy) error {
	desired := &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		if err := ensureControlledBy(desired, proxy, schema.GroupResource{Group: "", Resource: "services"}); err != nil {
			return err
		}
		if err := controllerutil.SetControllerReference(proxy, desired, r.Scheme); err != nil {
			return err
		}
		desired.Spec.Selector = proxyLabels(proxy)
		desired.Spec.Type = serviceType(proxy)
		// On update, CreateOrUpdate has already fetched the stored
		// Service into desired, so its ports still carry the node
		// ports the API server allocated. Capture them before the
		// rebuild — a desired port list without them differs from the
		// stored Service on every reconcile and leans on the API
		// server's update-time stickiness to keep the allocation —
		// and carry them onto the rebuilt ports with matching names
		// while the desired type still allocates node ports. Leaving
		// the NodePort type (or never having used it) rebuilds the
		// ports unallocated, which clears node ports the API server
		// rejects on a ClusterIP Service.
		allocated := nodePortsByName(desired.Spec.Ports)
		desired.Spec.Ports = buildServicePorts(proxy)
		if serviceTypeAllocatesNodePorts(desired.Spec.Type) {
			retainNodePorts(desired.Spec.Ports, allocated)
		}
		families, familyPolicy := serviceIPFamilies(proxy)
		desired.Spec.IPFamilyPolicy = &familyPolicy
		// A nil family list asks the API server to allocate what the
		// cluster supports, so the field stays unset on create and is
		// never rewritten on update, where the families are immutable.
		if families != nil {
			desired.Spec.IPFamilies = families
		}
		return nil
	})
	return err
}

// serviceIPFamilies returns the address families the proxy's Service
// must allocate cluster IPs in, and the policy that requests them, so a
// published Service only carries traffic the proxy's application
// listeners can accept.
//
// A listener binds IPv4 unless it names an IPv6 address: the spec default
// (and the renderer's default bind) is the IPv4 wildcard, which never
// accepts a connection to an IPv6 address, while the IPv6 wildcard ::
// accepts both families. The Service therefore follows the listeners.
// IPv4-only and IPv6-only binds keep their family explicitly — today's
// behavior on IPv4 clusters, and a compatible choice on dual-stack
// clusters whose primary family is IPv6, where a defaulted Service would
// be unreachable for the default binds.
//
// Listeners that each accept both families (the IPv6 wildcard) are
// unconstrained: the Service requests PreferDualStack and leaves the
// families unset, so the API server allocates what the cluster supports —
// both on dual-stack clusters, the one available family on single-family
// clusters, instead of rejecting the Service for requesting a family the
// cluster cannot allocate.
//
// A mix of IPv4-only and IPv6-only listeners leaves no single family that
// serves every listener, so the Service must carry both and requests them
// explicitly; single-family clusters reject it rather than publish a
// Service half the listeners cannot accept. A proxy without listeners
// keeps the explicit IPv4 default the controller has always published.
func serviceIPFamilies(proxy *proxyv1alpha1.Proxy) ([]corev1.IPFamily, corev1.IPFamilyPolicy) {
	if len(proxy.Spec.Listeners) == 0 {
		return []corev1.IPFamily{corev1.IPv4Protocol}, corev1.IPFamilyPolicySingleStack
	}
	onlyV4, onlyV6 := false, false
	for i := range proxy.Spec.Listeners {
		v4, v6 := listenerBindFamilies(proxy.Spec.Listeners[i].BindAddress)
		onlyV4, onlyV6 = onlyV4 || (v4 && !v6), onlyV6 || (v6 && !v4)
	}
	switch {
	case onlyV4 && onlyV6:
		return []corev1.IPFamily{corev1.IPv4Protocol, corev1.IPv6Protocol}, corev1.IPFamilyPolicyPreferDualStack
	case onlyV6:
		return []corev1.IPFamily{corev1.IPv6Protocol}, corev1.IPFamilyPolicySingleStack
	case onlyV4:
		return []corev1.IPFamily{corev1.IPv4Protocol}, corev1.IPFamilyPolicySingleStack
	default:
		return nil, corev1.IPFamilyPolicyPreferDualStack
	}
}

// retainedFamilyConflict reports a spec whose Service family request
// cannot be met by the existing Service. The request is the family set
// serviceIPFamilies computes and reconcileService writes, so the two
// stay consistent: a spec that keeps requesting exactly the allocated
// families never conflicts — including mixed single-family binds,
// where each family serves only the listeners that accept it and the
// dual-stack Service carries traffic for both. Service IP families are
// immutable, so a request that changes the allocated set — dropping a
// family the Service retains, or adding one it lacks — can never be
// published while that Service exists; the caller rejects the change
// before publishing configuration or workloads. A spec that leaves the
// families for the cluster to choose accepts any allocation, and a
// Service with no allocated families constrains nothing.
func retainedFamilyConflict(proxy *proxyv1alpha1.Proxy, svc *corev1.Service) error {
	if len(svc.Spec.IPFamilies) == 0 {
		return nil
	}
	desired, _ := serviceIPFamilies(proxy)
	if desired == nil || slices.Equal(desired, svc.Spec.IPFamilies) {
		return nil
	}
	dropped := familiesOnlyIn(svc.Spec.IPFamilies, desired)
	added := familiesOnlyIn(desired, svc.Spec.IPFamilies)
	var conflicts []string
	for i := range proxy.Spec.Listeners {
		listener := &proxy.Spec.Listeners[i]
		acceptsV4, acceptsV6 := listenerBindFamilies(listener.BindAddress)
		bind := "0.0.0.0" // the spec default for an unset bind
		if listener.BindAddress != nil {
			bind = *listener.BindAddress
		}
		for _, family := range dropped {
			if (family == corev1.IPv4Protocol && !acceptsV4) || (family == corev1.IPv6Protocol && !acceptsV6) {
				conflicts = append(conflicts, fmt.Sprintf("listener %q binds %s, which does not accept %s", listener.Name, bind, family))
			}
		}
		for _, family := range added {
			if (family == corev1.IPv4Protocol && acceptsV4 && !acceptsV6) || (family == corev1.IPv6Protocol && acceptsV6 && !acceptsV4) {
				conflicts = append(conflicts, fmt.Sprintf("listener %q binds %s, which requires %s", listener.Name, bind, family))
			}
		}
	}
	if len(conflicts) == 0 {
		// Every dropped or added family has a listener that no longer
		// accepts it or now requires it, except a spec whose listeners
		// were removed entirely; name the difference anyway so the
		// condition always explains the conflict.
		conflicts = append(conflicts, fmt.Sprintf("the spec requests %s while no listener binds them", familyList(desired)))
	}
	return fmt.Errorf("the Service %s/%s retains %s, but the proxy requests %s (%s); Service IP families are immutable, so revert the bindAddress or delete the Service to reallocate",
		svc.Namespace, svc.Name, familyList(svc.Spec.IPFamilies), familyList(desired), strings.Join(conflicts, "; "))
}

// familiesOnlyIn returns the families of a that b does not contain.
func familiesOnlyIn(a, b []corev1.IPFamily) []corev1.IPFamily {
	var only []corev1.IPFamily
	for _, family := range a {
		if !slices.Contains(b, family) {
			only = append(only, family)
		}
	}
	return only
}

// familyList renders families for a condition message.
func familyList(families []corev1.IPFamily) string {
	parts := make([]string, 0, len(families))
	for _, family := range families {
		parts = append(parts, string(family))
	}
	return strings.Join(parts, ", ")
}

// listenerBindFamilies reports which address families a listener's bind
// address accepts. A nil bind renders as the IPv4 wildcard. An unparseable
// bind cannot name an IPv6 address, so it is treated like the IPv4 default
// rather than pinning the Service to a family the listener may not accept.
func listenerBindFamilies(bind *string) (v4, v6 bool) {
	if bind == nil {
		return true, false
	}
	ip := net.ParseIP(*bind)
	switch {
	case ip == nil, ip.To4() != nil:
		return true, false
	case ip.IsUnspecified():
		return true, true
	default:
		return false, true
	}
}

func serviceType(proxy *proxyv1alpha1.Proxy) corev1.ServiceType {
	if proxy.Spec.Service != nil && proxy.Spec.Service.Type != nil {
		return corev1.ServiceType(*proxy.Spec.Service.Type)
	}
	return corev1.ServiceTypeClusterIP
}

func buildServicePorts(proxy *proxyv1alpha1.Proxy) []corev1.ServicePort {
	ports := make([]corev1.ServicePort, 0, len(proxy.Spec.Listeners))
	for _, listener := range proxy.Spec.Listeners {
		ports = append(ports, corev1.ServicePort{
			Name:       listener.Name,
			Port:       listener.Port,
			TargetPort: intstr.FromString(listener.Name),
			Protocol:   corev1.ProtocolTCP,
		})
	}
	return ports
}

// serviceTypeAllocatesNodePorts reports whether the API server
// allocates node ports for Services of the type.
func serviceTypeAllocatesNodePorts(serviceType corev1.ServiceType) bool {
	return serviceType == corev1.ServiceTypeNodePort || serviceType == corev1.ServiceTypeLoadBalancer
}

// nodePortsByName maps port name to the node port the API server
// allocated for it.
func nodePortsByName(ports []corev1.ServicePort) map[string]int32 {
	allocated := make(map[string]int32, len(ports))
	for _, port := range ports {
		if port.NodePort > 0 {
			allocated[port.Name] = port.NodePort
		}
	}
	return allocated
}

// retainNodePorts carries allocated node ports onto rebuilt ports with
// matching names, keeping the allocations stable across reconciles.
// Ports without a matching allocation — added or renamed listeners, or
// a switch to a type without node ports — stay unallocated for the API
// server to assign or clear.
func retainNodePorts(ports []corev1.ServicePort, allocated map[string]int32) {
	for i := range ports {
		if nodePort, ok := allocated[ports[i].Name]; ok {
			ports[i].NodePort = nodePort
		}
	}
}

func buildListenerStatus(proxy *proxyv1alpha1.Proxy) []proxyv1alpha1.PraxisListenerStatus {
	existing := make(map[string][]metav1.Condition, len(proxy.Status.Listeners))
	for _, ls := range proxy.Status.Listeners {
		existing[ls.Name] = ls.Conditions
	}
	statuses := make([]proxyv1alpha1.PraxisListenerStatus, 0, len(proxy.Spec.Listeners))
	for _, listener := range proxy.Spec.Listeners {
		ls := proxyv1alpha1.PraxisListenerStatus{
			Name:       listener.Name,
			ChainOrder: listener.FilterChains,
			Conditions: existing[listener.Name],
		}
		apimeta.SetStatusCondition(&ls.Conditions, metav1.Condition{
			Type:               proxyv1alpha1.ConditionTypeProgrammed,
			Status:             metav1.ConditionTrue,
			Reason:             "Programmed",
			Message:            fmt.Sprintf("Listener %q programmed", listener.Name),
			ObservedGeneration: proxy.Generation,
		})
		statuses = append(statuses, ls)
	}
	return statuses
}

func (r *ProxyReconciler) updateReadyStatus(ctx context.Context, proxy *proxyv1alpha1.Proxy) error {
	var deploy appsv1.Deployment
	if err := r.Get(ctx, client.ObjectKeyFromObject(proxy), &deploy); err != nil {
		if !apierrors.IsNotFound(err) {
			return fmt.Errorf("get deployment: %w", err)
		}
		proxy.Status.ServingReplicas = 0
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeReady,
			metav1.ConditionFalse, "NoReadyReplicas", "Deployment not found", proxy.Generation)
		return nil
	}
	proxy.Status.ServingReplicas = deploy.Status.ReadyReplicas

	desired := int32(1)
	if proxy.Spec.Replicas != nil {
		desired = *proxy.Spec.Replicas
	}
	startupRevision, err := computeStartupRevision(&proxy.Spec)
	if err != nil {
		return err
	}
	rolloutComplete := deploy.Spec.Replicas != nil && *deploy.Spec.Replicas == desired &&
		deploy.Spec.Template.Annotations[startupRevisionAnnotation] == startupRevision &&
		deploy.Status.ObservedGeneration >= deploy.Generation &&
		deploy.Status.UpdatedReplicas == desired &&
		deploy.Status.ReadyReplicas == desired &&
		deploy.Status.UnavailableReplicas == 0

	switch {
	case rolloutComplete:
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeReady,
			metav1.ConditionTrue, "DeploymentReady",
			fmt.Sprintf("%d/%d replicas ready", deploy.Status.ReadyReplicas, desired),
			proxy.Generation)
	case deploy.Status.ReadyReplicas == 0:
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeReady,
			metav1.ConditionFalse, "NoReadyReplicas",
			fmt.Sprintf("0/%d replicas ready", desired),
			proxy.Generation)
	default:
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeReady,
			metav1.ConditionFalse, "RollingOut",
			fmt.Sprintf("%d/%d updated, %d/%d ready", deploy.Status.UpdatedReplicas, desired, deploy.Status.ReadyReplicas, desired),
			proxy.Generation)
	}
	return nil
}

func (r *ProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.Proxy{}).
		Owns(&corev1.ConfigMap{}).
		Owns(&appsv1.Deployment{}).
		Owns(&corev1.Service{}).
		Complete(r)
}
