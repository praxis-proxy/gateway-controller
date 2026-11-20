package controller

import (
	"context"
	"fmt"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
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
)

type ProxyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=praxis.io,resources=proxies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=praxis.io,resources=proxies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=praxis.io,resources=proxies/finalizers,verbs=update
// +kubebuilder:rbac:groups="",resources=configmaps,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=apps,resources=deployments,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups="",resources=services,verbs=get;list;watch;create;update;patch;delete

func (r *ProxyReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var proxy proxyv1alpha1.Proxy
	if err := r.Get(ctx, req.NamespacedName, &proxy); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	patch := client.MergeFrom(proxy.DeepCopy())

	logger.V(1).Info("reconciling proxy")

	proxy.Status.ObservedGeneration = proxy.Generation

	if validationErrs := validateProxySpec(&proxy.Spec); len(validationErrs) > 0 {
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted,
			metav1.ConditionFalse, "ValidationError", joinValidationErrors(validationErrs), proxy.Generation)
		if err := r.Status().Patch(ctx, &proxy, patch); err != nil {
			return ctrl.Result{}, err
		}
		return ctrl.Result{}, nil
	}
	setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted,
		metav1.ConditionTrue, "Valid", "Proxy specification is valid", proxy.Generation)

	configData, err := renderProxyConfig(&proxy.Spec, proxy.Namespace)
	if err != nil {
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted,
			metav1.ConditionFalse, "ConfigRenderError", err.Error(), proxy.Generation)
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs,
			metav1.ConditionFalse, "ConfigRenderError", err.Error(), proxy.Generation)
		if patchErr := r.Status().Patch(ctx, &proxy, patch); patchErr != nil {
			return ctrl.Result{}, patchErr
		}
		return ctrl.Result{}, nil
	}
	setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs,
		metav1.ConditionTrue, "ResolvedRefs", "All references resolved", proxy.Generation)

	if err := r.reconcileConfigMap(ctx, &proxy, configData); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile configmap: %w", err)
	}

	revision, err := computeRevision(configData, proxy.Spec)
	if err != nil {
		return ctrl.Result{}, err
	}
	proxy.Status.DesiredRevision = revision

	if err := r.reconcileDeployment(ctx, &proxy, revision); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile deployment: %w", err)
	}

	if err := r.reconcileService(ctx, &proxy); err != nil {
		return ctrl.Result{}, fmt.Errorf("reconcile service: %w", err)
	}

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

func configMapName(proxy *proxyv1alpha1.Proxy) string {
	return proxy.Name + "-config"
}

func (r *ProxyReconciler) reconcileConfigMap(ctx context.Context, proxy *proxyv1alpha1.Proxy, configData string) error {
	desired := &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:      configMapName(proxy),
			Namespace: proxy.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
		if err := controllerutil.SetControllerReference(proxy, desired, r.Scheme); err != nil {
			return err
		}
		desired.Data = map[string]string{configFileName: configData}
		return nil
	})
	return err
}

const configRevisionAnnotation = "praxis.io/config-revision"

func (r *ProxyReconciler) reconcileDeployment(ctx context.Context, proxy *proxyv1alpha1.Proxy, revision string) error {
	desired := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:      proxy.Name,
			Namespace: proxy.Namespace,
		},
	}
	_, err := controllerutil.CreateOrUpdate(ctx, r.Client, desired, func() error {
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
		desired.Spec.Template = buildPodTemplate(proxy, revision)
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

func buildPodTemplate(proxy *proxyv1alpha1.Proxy, revision string) corev1.PodTemplateSpec {
	volumes, mounts := buildVolumes(proxy)
	gracePeriod := defaultTerminationGracePeriod
	container := corev1.Container{
		Name:                     containerName,
		Image:                    proxy.Spec.Image,
		Command:                  []string{containerName},
		Args:                     []string{"--config", configMountPath + "/" + configFileName},
		Ports:                    buildContainerPorts(proxy),
		VolumeMounts:             mounts,
		Env:                      buildEnvVars(proxy),
		ImagePullPolicy:          corev1.PullIfNotPresent,
		TerminationMessagePath:   corev1.TerminationMessagePathDefault,
		TerminationMessagePolicy: corev1.TerminationMessageReadFile,
	}
	return corev1.PodTemplateSpec{
		ObjectMeta: metav1.ObjectMeta{
			Labels: proxyLabels(proxy),
			Annotations: map[string]string{
				configRevisionAnnotation: revision,
			},
		},
		Spec: corev1.PodSpec{
			Containers:                    []corev1.Container{container},
			Volumes:                       volumes,
			RestartPolicy:                 corev1.RestartPolicyAlways,
			DNSPolicy:                     corev1.DNSClusterFirst,
			TerminationGracePeriodSeconds: &gracePeriod,
			SchedulerName:                 corev1.DefaultSchedulerName,
		},
	}
}

func buildVolumes(proxy *proxyv1alpha1.Proxy) ([]corev1.Volume, []corev1.VolumeMount) {
	volumes := []corev1.Volume{{
		Name: configVolumeName,
		VolumeSource: corev1.VolumeSource{
			ConfigMap: &corev1.ConfigMapVolumeSource{
				LocalObjectReference: corev1.LocalObjectReference{Name: configMapName(proxy)},
			},
		},
	}}
	mounts := []corev1.VolumeMount{{
		Name:      configVolumeName,
		MountPath: configMountPath + "/" + configFileName,
		SubPath:   configSubPath,
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
					SecretName: listener.TLS.CertificateRef.Name,
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

func buildContainerPorts(proxy *proxyv1alpha1.Proxy) []corev1.ContainerPort {
	ports := make([]corev1.ContainerPort, 0, len(proxy.Spec.Listeners))
	for _, listener := range proxy.Spec.Listeners {
		ports = append(ports, corev1.ContainerPort{
			Name:          listener.Name,
			ContainerPort: listener.Port,
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
		if err := controllerutil.SetControllerReference(proxy, desired, r.Scheme); err != nil {
			return err
		}
		desired.Spec.Selector = proxyLabels(proxy)
		desired.Spec.Type = serviceType(proxy)
		desired.Spec.Ports = buildServicePorts(proxy)
		return nil
	})
	return err
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
	if deploy.Spec.Replicas != nil {
		desired = *deploy.Spec.Replicas
	}
	rolloutComplete := deploy.Status.ObservedGeneration >= deploy.Generation &&
		deploy.Status.UpdatedReplicas == desired &&
		deploy.Status.ReadyReplicas == desired &&
		deploy.Status.UnavailableReplicas == 0

	switch {
	case rolloutComplete:
		proxy.Status.PublishedRevision = proxy.Status.DesiredRevision
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
