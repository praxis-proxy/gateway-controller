package controller

import (
	"context"
	"fmt"

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

// resolveNamespaceOwner returns the Tenant that owns its namespace: the one
// with the earliest creationTimestamp, ties broken by name in lexicographic
// ascending order. It returns nil when tenants is empty.
//
// This is the rule docs/design/api-v1alpha1.md applies to Model published-name
// conflicts. It is computed from the objects rather than from reconcile
// order, so it is stable across controller restarts and cache rebuilds.
//
// Candidates are passed in rather than listed here: when a Tenant can claim
// namespaces it does not live in, only the gathering changes and this rule
// stays as it is.
func resolveNamespaceOwner(tenants []aigatewayv1alpha1.Tenant) *aigatewayv1alpha1.Tenant {
	var owner *aigatewayv1alpha1.Tenant
	for i := range tenants {
		candidate := &tenants[i]
		if owner == nil || hasStrongerClaim(candidate, owner) {
			owner = candidate
		}
	}
	return owner
}

// hasStrongerClaim reports whether a outranks b for namespace ownership.
//
// CreationTimestamp is compared before name, and is immutable once an object
// is created. So an established owner can never be displaced by a Tenant
// created later, whatever its name compares as — only two Tenants created
// within the same timestamp (one-second resolution) ever fall through to the
// name comparison. The only way to change an established owner is to delete
// it.
func hasStrongerClaim(a, b *aigatewayv1alpha1.Tenant) bool {
	if !a.CreationTimestamp.Equal(&b.CreationTimestamp) {
		return a.CreationTimestamp.Before(&b.CreationTimestamp)
	}
	return a.Name < b.Name
}

// TenantReconciler reconciles Tenant objects. Its only responsibility in
// v1alpha1 is enforcing that a namespace belongs to a single Tenant; the
// authorization grants in the spec are validated by the resources that consume
// them, not here.
type TenantReconciler struct {
	client.Client
	Scheme *runtime.Scheme
}

// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=tenants,verbs=get;list;watch
// +kubebuilder:rbac:groups=aigateway.praxis.io,resources=tenants/status,verbs=get;update;patch

func (r *TenantReconciler) Reconcile(ctx context.Context, req ctrl.Request) (ctrl.Result, error) {
	log := logf.FromContext(ctx)

	var tenant aigatewayv1alpha1.Tenant
	if err := r.Get(ctx, req.NamespacedName, &tenant); err != nil {
		return ctrl.Result{}, client.IgnoreNotFound(err)
	}
	patch := client.MergeFrom(tenant.DeepCopy())

	log.V(1).Info("reconciling tenant", "name", req.Name, "namespace", req.Namespace)

	var tenants aigatewayv1alpha1.TenantList
	if err := r.List(ctx, &tenants, client.InNamespace(req.Namespace)); err != nil {
		return ctrl.Result{}, fmt.Errorf("list tenants in namespace %s: %w", req.Namespace, err)
	}

	// A nil owner means the cache has not yet observed this Tenant. It exists,
	// so treat it as the owner; a later reconcile corrects this if another
	// Tenant turns out to have the stronger claim.
	owner := resolveNamespaceOwner(tenants.Items)
	if owner == nil || owner.Name == tenant.Name {
		setCondition(&tenant.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady,
			metav1.ConditionTrue, aigatewayv1alpha1.ReasonReady,
			"Tenant owns its namespace", tenant.Generation)
	} else {
		setCondition(&tenant.Status.Conditions, aigatewayv1alpha1.ConditionTypeReady,
			metav1.ConditionFalse, aigatewayv1alpha1.ReasonConflict,
			fmt.Sprintf("Namespace %s is owned by Tenant %s", tenant.Namespace, owner.Name),
			tenant.Generation)
	}

	if err := r.Status().Patch(ctx, &tenant, patch); err != nil {
		return ctrl.Result{}, err
	}

	return ctrl.Result{}, nil
}

// tenantsToRequests converts Tenants into reconcile requests for each of them.
func tenantsToRequests(tenants []aigatewayv1alpha1.Tenant) []reconcile.Request {
	requests := make([]reconcile.Request, 0, len(tenants))
	for _, tenant := range tenants {
		requests = append(requests, reconcile.Request{
			NamespacedName: types.NamespacedName{
				Name:      tenant.Name,
				Namespace: tenant.Namespace,
			},
		})
	}
	return requests
}

// mapToNamespaceTenants enqueues every Tenant in the changed object's
// namespace.
//
// Ownership is relative, so a change to one Tenant can change the verdict for
// all of them. Deletion is the case that makes this necessary: the owning
// Tenant's own key is the only one the primary watch enqueues, and that object
// no longer exists, so without this the Tenants that lost to it would never be
// promoted.
//
// This amplifies: N simultaneous Tenant changes in one namespace can enqueue
// up to N² requests. Namespace membership is meant to resolve to one Tenant,
// so N is expected to stay in the single digits; this would need revisiting
// if that stopped holding.
func (r *TenantReconciler) mapToNamespaceTenants(ctx context.Context, obj client.Object) []reconcile.Request {
	var tenants aigatewayv1alpha1.TenantList
	if err := r.List(ctx, &tenants, client.InNamespace(obj.GetNamespace())); err != nil {
		logf.FromContext(ctx).Error(err, "list tenants for namespace mapping",
			"namespace", obj.GetNamespace())
		return nil
	}
	return tenantsToRequests(tenants.Items)
}

func (r *TenantReconciler) SetupWithManager(mgr ctrl.Manager) error {
	return ctrl.NewControllerManagedBy(mgr).
		For(&aigatewayv1alpha1.Tenant{}).
		Watches(&aigatewayv1alpha1.Tenant{},
			handler.EnqueueRequestsFromMapFunc(r.mapToNamespaceTenants)).
		Named("tenant").
		Complete(r)
}
