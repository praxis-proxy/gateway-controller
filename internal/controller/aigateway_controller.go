package controller

import (
	"context"
	"fmt"

	apierrors "k8s.io/apimachinery/pkg/api/errors"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/handler"
	logf "sigs.k8s.io/controller-runtime/pkg/log"
	"sigs.k8s.io/controller-runtime/pkg/reconcile"

	aigatewayv1alpha1 "github.com/praxis-proxy/gateway-controller/api/aigateway/v1alpha1"
)

// AIGatewayReconciler reconciles AIGateway objects.
type AIGatewayReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=aigateways,verbs=get;list;watch
// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=aigateways/status,verbs=get;update;patch
// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=tenants,verbs=get;list;watch

func (r *AIGatewayReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	log.V(1).Info("reconciling aigateway", "name", req.Name, "namespace", req.Namespace)

	// Fetch the AIGateway.
	gw := &aigatewayv1alpha1.AIGateway{}
	if err := r.Get(ctx, req.NamespacedName, gw); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}

	patch := client.MergeFrom(gw.DeepCopy())

	// Validate and set status.
	if err := r.validateAndUpdateStatus(ctx, gw); err != nil {
		return ctrl.Result{}, err
	}

	if err := r.Status().Patch(ctx, gw, patch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// validateAndUpdateStatus validates the AIGateway spec and updates its Ready
// condition. It returns an error only for API failures the caller should retry;
// a spec or dependency problem is reported through the condition instead.
//
// Structural constraints are enforced by the apiserver through the CRD schema
// and CEL rules, so only cross-resource checks live here.
//
// Ready is deliberately partial in v1alpha1. The readiness table in
// docs/design/api-v1alpha1.md also requires the generated configuration to be
// applied and the backing runtime to report ready for it; neither is
// implemented, so Ready currently means the spec validated and the Tenant
// resolved. Resolving endpoint.tls.certificateRef, and gating on the Proxy's
// own Ready condition, both belong here when they land.
func (r *AIGatewayReconciler) validateAndUpdateStatus(ctx context.Context, gw *aigatewayv1alpha1.AIGateway) error {
	var reason, msg string
	ready := true

	// Resolve the Tenant and check its readiness. Only a genuine absence is a
	// dependency verdict; any other error is transient and must be retried, or
	// a blip would leave a wrong status that nothing corrects.
	tenant := &aigatewayv1alpha1.Tenant{}
	tenantKey := types.NamespacedName{Name: gw.Spec.TenantRef.Name, Namespace: gw.Namespace}
	switch err := r.Get(ctx, tenantKey, tenant); {
	case apierrors.IsNotFound(err):
		ready = false
		reason = aigatewayv1alpha1.ReasonDependencyNotReady
		msg = fmt.Sprintf("Tenant %q not found in namespace %q", gw.Spec.TenantRef.Name, gw.Namespace)
	case err != nil:
		return fmt.Errorf("get tenant %s: %w", tenantKey, err)
	case !isTenantReady(tenant):
		ready = false
		reason = aigatewayv1alpha1.ReasonDependencyNotReady
		msg = fmt.Sprintf("Tenant %q is not ready", gw.Spec.TenantRef.Name)
	}

	// Validate authentication types are a subset of the Tenant's authorized types.
	if ready && gw.Spec.Governance != nil {
		authorizedTypes := make(map[aigatewayv1alpha1.IdentityType]bool, len(tenant.Spec.AuthorizedIdentities))
		for _, id := range tenant.Spec.AuthorizedIdentities {
			authorizedTypes[id.Type] = true
		}

		for _, auth := range gw.Spec.Governance.Authentication {
			if !authorizedTypes[auth.Type] {
				ready = false
				reason = aigatewayv1alpha1.ReasonInvalid
				msg = fmt.Sprintf("authentication type %q is not authorized by Tenant %q", auth.Type, tenant.Name)
				break
			}
		}
	}

	if ready {
		reason = aigatewayv1alpha1.ReasonReady
		msg = "Gateway configuration is valid and ready."
	}

	status := metav1.ConditionFalse
	if ready {
		status = metav1.ConditionTrue
	}
	setCondition(&gw.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady, status, reason, msg, gw.Generation)

	return nil
}

// isTenantReady reports whether the Tenant is ready for its current spec.
//
// A Ready condition left over from an earlier generation says nothing about
// the spec in hand, so it does not count: docs/design/api-v1alpha1.md requires
// an observedGeneration matching metadata.generation before readiness is
// established. Until the Tenant controller catches up, the gateway waits.
func isTenantReady(tenant *aigatewayv1alpha1.Tenant) bool {
	ready := meta.FindStatusCondition(tenant.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady)
	return ready != nil &&
		ready.Status == metav1.ConditionTrue &&
		ready.ObservedGeneration == tenant.Generation
}

// mapTenantToGateways enqueues every AIGateway in the changed Tenant's
// namespace that references it.
//
// This watch carries no generation predicate on purpose. A gateway's readiness
// tracks its Tenant's Ready condition, and status writes go through the status
// subresource, which does not bump metadata.generation. Filtering on generation
// would discard exactly the events this watch exists to deliver, leaving a
// gateway created before its Tenant became ready stuck until the next resync.
func (r *AIGatewayReconciler) mapTenantToGateways(ctx context.Context, obj client.Object) []reconcile.Request {
	tenant, ok := obj.(*aigatewayv1alpha1.Tenant)
	if !ok {
		logf.FromContext(ctx).Error(nil, "unexpected object type in tenant watch",
			"type", fmt.Sprintf("%T", obj))
		return nil
	}

	gateways := &aigatewayv1alpha1.AIGatewayList{}
	if err := r.List(ctx, gateways, client.InNamespace(tenant.Namespace)); err != nil {
		logf.FromContext(ctx).Error(err, "list aigateways for tenant mapping",
			"namespace", tenant.Namespace, "tenant", tenant.Name)
		return nil
	}

	requests := make([]reconcile.Request, 0, len(gateways.Items))
	for _, gw := range gateways.Items {
		if gw.Spec.TenantRef.Name == tenant.Name {
			requests = append(requests, reconcile.Request{
				NamespacedName: types.NamespacedName{
					Name:      gw.Name,
					Namespace: gw.Namespace,
				},
			})
		}
	}
	return requests
}

// SetupWithManager sets up the controller with the Manager.
func (r *AIGatewayReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aigatewayv1alpha1.AIGateway{}).
		Watches(&aigatewayv1alpha1.Tenant{},
			handler.EnqueueRequestsFromMapFunc(r.mapTenantToGateways)).
		Named("aigateway").
		Complete(r)
}
