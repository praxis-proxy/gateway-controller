package controller

import (
	"context"

	"k8s.io/apimachinery/pkg/runtime"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/log"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

type AIGatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=aigateways,verbs=get;list;watch;create;update;patch;delete
// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=aigateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=aigateways/finalizers,verbs=update

func (r *AIGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	logger := log.FromContext(ctx)

	var aigateway aigatewayv1alpha1.AIGateway
	if err := r.Get(ctx, req.NamespacedName, &aigateway); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	logger.Info("reconciling AIGateway", "name", aigateway.Name, "namespace", aigateway.Namespace)

	return ctrl.Result{}, nil
}

func (r *AIGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aigatewayv1alpha1.AIGateway{}).
		Complete(r)
}
