package controller

import (
	"context"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

type ProxyReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=praxis.io,resources=proxies,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=praxis.io,resources=proxies/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=praxis.io,resources=proxies/finalizers,verbs=update

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
	} else {
		setCondition(&proxy.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted,
			metav1.ConditionTrue, "Valid", "Proxy specification is valid", proxy.Generation)
	}

	if err := r.Status().Patch(ctx, &proxy, patch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

func (r *ProxyReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&proxyv1alpha1.Proxy{}).
		Complete(r)
}
