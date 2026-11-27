package controller

import (
	"context"
	"reflect"
	"strings"
	"testing"
	"unicode/utf8"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	apimeta "k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/types"
	ctrl "sigs.k8s.io/controller-runtime"
	"sigs.k8s.io/controller-runtime/pkg/client"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

func proxyTestScheme(t *testing.T) *runtime.Scheme {
	t.Helper()
	scheme := runtime.NewScheme()
	if err := proxyv1alpha1.AddToScheme(scheme); err != nil {
		t.Fatalf("register proxy types: %v", err)
	}
	if err := corev1.AddToScheme(scheme); err != nil {
		t.Fatalf("register core types: %v", err)
	}
	if err := appsv1.AddToScheme(scheme); err != nil {
		t.Fatalf("register apps types: %v", err)
	}
	return scheme
}

func guardTestProxy() *proxyv1alpha1.Proxy {
	return &proxyv1alpha1.Proxy{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "gw",
			Namespace: "default",
			UID:       "proxy-uid",
		},
		Spec: proxyv1alpha1.ProxySpec{
			Image: "ghcr.io/praxis-proxy/praxis:latest",
			FilterChains: []proxyv1alpha1.ProxyFilterChain{{
				Name: "main",
				Filters: []proxyv1alpha1.PraxisFilterEntry{{
					Name:         "rl",
					InlineFilter: &proxyv1alpha1.PraxisInlineFilter{Type: "rate_limit"},
				}},
			}},
			Listeners: []proxyv1alpha1.PraxisListener{{
				Name:         "http",
				Port:         8080,
				FilterChains: []string{"main"},
			}},
		},
	}
}

func controllerRefFor(proxy *proxyv1alpha1.Proxy) metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: "praxis.io/v1alpha1",
		Kind:       "Proxy",
		Name:       proxy.Name,
		UID:        proxy.UID,
		Controller: new(true),
	}
}

func foreignControllerRef() metav1.OwnerReference {
	return metav1.OwnerReference{
		APIVersion: "apps/v1",
		Kind:       "Deployment",
		Name:       "someone-elses",
		UID:        "foreign-uid",
		Controller: new(true),
	}
}

func unownedConfigMap() *corev1.ConfigMap {
	return &corev1.ConfigMap{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "gw-config",
			Namespace:         "default",
			UID:               "cm-uid",
			CreationTimestamp: metav1.Now(),
		},
		Data: map[string]string{configFileName: "user-managed"},
	}
}

func unownedDeployment() *appsv1.Deployment {
	replicas := int32(7)
	return &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "gw",
			Namespace:         "default",
			UID:               "deploy-uid",
			CreationTimestamp: metav1.Now(),
		},
		Spec: appsv1.DeploymentSpec{
			Replicas: &replicas,
			Template: podTemplateWithImage("user-image:custom"),
		},
	}
}

func unownedService() *corev1.Service {
	return &corev1.Service{
		ObjectMeta: metav1.ObjectMeta{
			Name:              "gw",
			Namespace:         "default",
			UID:               "svc-uid",
			CreationTimestamp: metav1.Now(),
		},
		Spec: corev1.ServiceSpec{
			Type: corev1.ServiceTypeNodePort,
			Ports: []corev1.ServicePort{{
				Name: "user", Port: 9999, Protocol: corev1.ProtocolTCP,
			}},
		},
	}
}

func podTemplateWithImage(image string) corev1.PodTemplateSpec {
	return corev1.PodTemplateSpec{
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "user", Image: image}},
		},
	}
}

// TestReconcileResources_RefuseUnownedObjects verifies that the reconciler
// never adopts or overwrites existing objects it does not control: an
// object with no controller owner, or one controlled by another
// controller, must produce a conflict error and remain untouched.
func TestReconcileResources_RefuseUnownedObjects(t *testing.T) {
	t.Parallel()

	scheme := proxyTestScheme(t)
	proxy := guardTestProxy()

	tests := []struct {
		name      string
		existing  client.Object
		reconcile func(ctx context.Context, r *ProxyReconciler) error
		key       types.NamespacedName
		fetch     func() client.Object
		check     func(t *testing.T, got client.Object)
	}{
		{
			name:     "configmap without controller owner",
			existing: unownedConfigMap(),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileConfigMap(ctx, proxy, "rendered-config")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw-config"},
			fetch: func() client.Object { return &corev1.ConfigMap{} },
			check: func(t *testing.T, got client.Object) {
				cm := got.(*corev1.ConfigMap)
				if cm.Data[configFileName] != "user-managed" {
					t.Errorf("unowned ConfigMap was overwritten: %q", cm.Data[configFileName])
				}
				if owner := metav1.GetControllerOf(cm); owner != nil {
					t.Errorf("unowned ConfigMap was adopted by %q", owner.Name)
				}
			},
		},
		{
			name:     "configmap controlled by another controller",
			existing: withForeignOwner(unownedConfigMap()),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileConfigMap(ctx, proxy, "rendered-config")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw-config"},
			fetch: func() client.Object { return &corev1.ConfigMap{} },
			check: func(t *testing.T, got client.Object) {
				cm := got.(*corev1.ConfigMap)
				if cm.Data[configFileName] != "user-managed" {
					t.Errorf("foreign-owned ConfigMap was overwritten: %q", cm.Data[configFileName])
				}
				if owner := metav1.GetControllerOf(cm); owner == nil || owner.UID != "foreign-uid" {
					t.Errorf("foreign controller owner was replaced: %+v", owner)
				}
			},
		},
		{
			name:     "deployment without controller owner",
			existing: unownedDeployment(),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileDeployment(ctx, proxy, "rev", "")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch: func() client.Object { return &appsv1.Deployment{} },
			check: func(t *testing.T, got client.Object) {
				deploy := got.(*appsv1.Deployment)
				if len(deploy.Spec.Template.Spec.Containers) != 1 || deploy.Spec.Template.Spec.Containers[0].Image != "user-image:custom" {
					t.Errorf("unowned Deployment was overwritten: %+v", deploy.Spec.Template.Spec.Containers)
				}
				if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 7 {
					t.Errorf("unowned Deployment replicas were changed: %v", deploy.Spec.Replicas)
				}
				if owner := metav1.GetControllerOf(deploy); owner != nil {
					t.Errorf("unowned Deployment was adopted by %q", owner.Name)
				}
			},
		},
		{
			name:     "deployment controlled by another controller",
			existing: withForeignOwner(unownedDeployment()),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileDeployment(ctx, proxy, "rev", "")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch: func() client.Object { return &appsv1.Deployment{} },
			check: func(t *testing.T, got client.Object) {
				deploy := got.(*appsv1.Deployment)
				if len(deploy.Spec.Template.Spec.Containers) != 1 || deploy.Spec.Template.Spec.Containers[0].Image != "user-image:custom" {
					t.Errorf("foreign-owned Deployment was overwritten: %+v", deploy.Spec.Template.Spec.Containers)
				}
				if owner := metav1.GetControllerOf(deploy); owner == nil || owner.UID != "foreign-uid" {
					t.Errorf("foreign controller owner was replaced: %+v", owner)
				}
			},
		},
		{
			name:      "service without controller owner",
			existing:  unownedService(),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error { return r.reconcileService(ctx, proxy) },
			key:       types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch:     func() client.Object { return &corev1.Service{} },
			check: func(t *testing.T, got client.Object) {
				svc := got.(*corev1.Service)
				if svc.Spec.Type != corev1.ServiceTypeNodePort {
					t.Errorf("unowned Service type was changed: %s", svc.Spec.Type)
				}
				if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 9999 {
					t.Errorf("unowned Service ports were changed: %+v", svc.Spec.Ports)
				}
				if owner := metav1.GetControllerOf(svc); owner != nil {
					t.Errorf("unowned Service was adopted by %q", owner.Name)
				}
			},
		},
		{
			name:      "service controlled by another controller",
			existing:  withForeignOwner(unownedService()),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error { return r.reconcileService(ctx, proxy) },
			key:       types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch:     func() client.Object { return &corev1.Service{} },
			check: func(t *testing.T, got client.Object) {
				svc := got.(*corev1.Service)
				if svc.Spec.Type != corev1.ServiceTypeNodePort {
					t.Errorf("foreign-owned Service type was changed: %s", svc.Spec.Type)
				}
				if owner := metav1.GetControllerOf(svc); owner == nil || owner.UID != "foreign-uid" {
					t.Errorf("foreign controller owner was replaced: %+v", owner)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			// Seed a per-subtest copy: the fake client assigns resource
			// versions to seeded objects during Build, and subtests run
			// in parallel.
			cl := fake.NewClientBuilder().
				WithScheme(scheme).
				WithObjects(proxy.DeepCopy(), tt.existing).
				Build()
			r := &ProxyReconciler{Client: cl, Scheme: scheme}

			err := tt.reconcile(context.Background(), r)
			if !apierrors.IsConflict(err) {
				t.Fatalf("expected conflict error, got: %v", err)
			}

			got := tt.fetch()
			if err := cl.Get(context.Background(), tt.key, got); err != nil {
				t.Fatalf("fetch object after reconcile: %v", err)
			}
			tt.check(t, got)
		})
	}
}

// TestReconcileResources_MutateOwnedAndCreateObjects verifies the guard
// does not interfere with the intended flows: objects already controlled
// by the proxy are updated, and absent objects are created.
func TestReconcileResources_MutateOwnedAndCreateObjects(t *testing.T) {
	t.Parallel()

	scheme := proxyTestScheme(t)
	proxy := guardTestProxy()

	tests := []struct {
		name      string
		existing  client.Object // nil: object absent
		reconcile func(ctx context.Context, r *ProxyReconciler) error
		key       types.NamespacedName
		fetch     func() client.Object
		check     func(t *testing.T, got client.Object)
	}{
		{
			name:     "configmap absent is created",
			existing: nil,
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileConfigMap(ctx, proxy, "rendered-config")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw-config"},
			fetch: func() client.Object { return &corev1.ConfigMap{} },
			check: func(t *testing.T, got client.Object) {
				cm := got.(*corev1.ConfigMap)
				if cm.Data[configFileName] != "rendered-config" {
					t.Errorf("config data: %q", cm.Data[configFileName])
				}
				if owner := metav1.GetControllerOf(cm); owner == nil || owner.UID != proxy.UID {
					t.Errorf("controller owner: %+v", owner)
				}
			},
		},
		{
			name:     "configmap owned by proxy is updated",
			existing: withControllerOwner(unownedConfigMap(), proxy),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileConfigMap(ctx, proxy, "rendered-config")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw-config"},
			fetch: func() client.Object { return &corev1.ConfigMap{} },
			check: func(t *testing.T, got client.Object) {
				cm := got.(*corev1.ConfigMap)
				if cm.Data[configFileName] != "rendered-config" {
					t.Errorf("config data: %q", cm.Data[configFileName])
				}
			},
		},
		{
			name:     "deployment absent is created",
			existing: nil,
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileDeployment(ctx, proxy, "rev", "")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch: func() client.Object { return &appsv1.Deployment{} },
			check: func(t *testing.T, got client.Object) {
				deploy := got.(*appsv1.Deployment)
				if len(deploy.Spec.Template.Spec.Containers) != 1 || deploy.Spec.Template.Spec.Containers[0].Name != containerName {
					t.Errorf("praxis container missing: %+v", deploy.Spec.Template.Spec.Containers)
				}
				assertLiveConfigMount(t, deploy)
				if owner := metav1.GetControllerOf(deploy); owner == nil || owner.UID != proxy.UID {
					t.Errorf("controller owner: %+v", owner)
				}
			},
		},
		{
			name:     "deployment owned by proxy is updated",
			existing: withControllerOwner(unownedDeployment(), proxy),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error {
				return r.reconcileDeployment(ctx, proxy, "rev", "")
			},
			key:   types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch: func() client.Object { return &appsv1.Deployment{} },
			check: func(t *testing.T, got client.Object) {
				deploy := got.(*appsv1.Deployment)
				if len(deploy.Spec.Template.Spec.Containers) != 1 || deploy.Spec.Template.Spec.Containers[0].Name != containerName {
					t.Errorf("praxis container missing: %+v", deploy.Spec.Template.Spec.Containers)
				}
				if deploy.Spec.Replicas == nil || *deploy.Spec.Replicas != 1 {
					t.Errorf("replicas: %v", deploy.Spec.Replicas)
				}
			},
		},
		{
			name:      "service absent is created",
			existing:  nil,
			reconcile: func(ctx context.Context, r *ProxyReconciler) error { return r.reconcileService(ctx, proxy) },
			key:       types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch:     func() client.Object { return &corev1.Service{} },
			check: func(t *testing.T, got client.Object) {
				svc := got.(*corev1.Service)
				if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 || svc.Spec.Ports[0].Name != "http" {
					t.Errorf("service ports: %+v", svc.Spec.Ports)
				}
				if owner := metav1.GetControllerOf(svc); owner == nil || owner.UID != proxy.UID {
					t.Errorf("controller owner: %+v", owner)
				}
			},
		},
		{
			name:      "service owned by proxy is updated",
			existing:  withControllerOwner(unownedService(), proxy),
			reconcile: func(ctx context.Context, r *ProxyReconciler) error { return r.reconcileService(ctx, proxy) },
			key:       types.NamespacedName{Namespace: "default", Name: "gw"},
			fetch:     func() client.Object { return &corev1.Service{} },
			check: func(t *testing.T, got client.Object) {
				svc := got.(*corev1.Service)
				if svc.Spec.Type != corev1.ServiceTypeClusterIP {
					t.Errorf("service type: %s", svc.Spec.Type)
				}
				if len(svc.Spec.Ports) != 1 || svc.Spec.Ports[0].Port != 8080 {
					t.Errorf("service ports: %+v", svc.Spec.Ports)
				}
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			t.Parallel()
			builder := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proxy.DeepCopy())
			if tt.existing != nil {
				builder = builder.WithObjects(tt.existing)
			}
			cl := builder.Build()
			r := &ProxyReconciler{Client: cl, Scheme: scheme}

			if err := tt.reconcile(context.Background(), r); err != nil {
				t.Fatalf("unexpected error: %v", err)
			}

			got := tt.fetch()
			if err := cl.Get(context.Background(), tt.key, got); err != nil {
				t.Fatalf("fetch object after reconcile: %v", err)
			}
			tt.check(t, got)
		})
	}
}

func withForeignOwner(obj client.Object) client.Object {
	obj.SetOwnerReferences([]metav1.OwnerReference{foreignControllerRef()})
	return obj
}

func withControllerOwner(obj client.Object, proxy *proxyv1alpha1.Proxy) client.Object {
	obj.SetOwnerReferences([]metav1.OwnerReference{controllerRefFor(proxy)})
	return obj
}

func fetchConfigMap(t *testing.T, cl client.Client, name string) *corev1.ConfigMap {
	t.Helper()
	cm := &corev1.ConfigMap{}
	if err := cl.Get(context.Background(), types.NamespacedName{Namespace: "default", Name: name}, cm); err != nil {
		t.Fatalf("get configmap %q: %v", name, err)
	}
	return cm
}

// TestReconcileProxy_RenderErrorBoundsConditionMessage verifies the
// ConfigRenderError conditions stay within the condition-message limit
// the API server enforces: render errors quote spec content (the
// reserved-key rejection names the filter entry), and validation does
// not bound entry-name length, so the raw error can exceed the limit
// and make the status patch itself fail. Both conditions carry the
// same bounded, rune-safe message.
func TestReconcileProxy_RenderErrorBoundsConditionMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := proxyTestScheme(t)

	// A multibyte entry name long enough to push the render error past
	// the limit, with the truncation point landing inside a rune: a
	// naive byte-slice cut would leave invalid UTF-8 in the condition.
	proxy := guardTestProxy()
	proxy.Spec.FilterChains[0].Filters[0].Name = "a" + strings.Repeat("€", maxConditionMessageLen)
	proxy.Spec.FilterChains[0].Filters[0].InlineFilter.Config = &runtime.RawExtension{
		Raw: []byte(`{"filter":"evil"}`),
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&proxyv1alpha1.Proxy{}).
		WithObjects(proxy).Build()
	r := &ProxyReconciler{Client: cl, Scheme: scheme}
	key := client.ObjectKeyFromObject(proxy)

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile proxy: %v", err)
	}
	got := &proxyv1alpha1.Proxy{}
	if err := cl.Get(ctx, key, got); err != nil {
		t.Fatalf("get proxy: %v", err)
	}
	for _, conditionType := range []string{
		proxyv1alpha1.ConditionTypeAccepted, proxyv1alpha1.ConditionTypeResolvedRefs,
	} {
		cond := apimeta.FindStatusCondition(got.Status.Conditions, conditionType)
		if cond == nil || cond.Status != metav1.ConditionFalse || cond.Reason != "ConfigRenderError" {
			t.Fatalf("%s condition = %+v, want False/ConfigRenderError", conditionType, cond)
		}
		if len(cond.Message) > maxConditionMessageLen {
			t.Errorf("%s message is %d bytes, want at most %d", conditionType, len(cond.Message), maxConditionMessageLen)
		}
		if !utf8.ValidString(cond.Message) {
			t.Errorf("%s message is not valid UTF-8", conditionType)
		}
	}
	accepted := apimeta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
	resolved := apimeta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs)
	if accepted.Message != resolved.Message {
		t.Errorf("Accepted message %q differs from ResolvedRefs message %q", accepted.Message, resolved.Message)
	}
}

// TestReconcileProxy_PublishedRevisionRecordsCompletedWrites verifies
// PublishedRevision advances only when every publish write completes:
// the assignment sits after the Deployment and Service reconciles,
// whose error returns skip it, so a failed write leaves the previously
// published revision in status — even when an earlier write in the
// same reconcile already succeeded.
func TestReconcileProxy_PublishedRevisionRecordsCompletedWrites(t *testing.T) {
	t.Parallel()

	for _, tc := range []struct {
		name                     string
		unowned                  client.Object
		wantErrSub               string
		deploymentWriteCompletes bool
	}{
		{
			name:       "deployment write fails",
			unowned:    unownedDeployment(),
			wantErrSub: "reconcile deployment:",
		},
		{
			name:                     "service write fails",
			unowned:                  unownedService(),
			wantErrSub:               "reconcile service:",
			deploymentWriteCompletes: true,
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			ctx := context.Background()
			scheme := proxyTestScheme(t)

			// The proxy carries a revision published by an earlier
			// reconcile; the failed publish must leave it in place.
			proxy := guardTestProxy()
			proxy.Status.PublishedRevision = "published-earlier"

			cl := fake.NewClientBuilder().WithScheme(scheme).
				WithStatusSubresource(&proxyv1alpha1.Proxy{}).
				WithObjects(proxy, tc.unowned).Build()
			r := &ProxyReconciler{Client: cl, Scheme: scheme}
			key := client.ObjectKeyFromObject(proxy)

			if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err == nil || !strings.Contains(err.Error(), tc.wantErrSub) {
				t.Fatalf("reconcile error = %v, want one containing %q", err, tc.wantErrSub)
			}
			got := &proxyv1alpha1.Proxy{}
			if err := cl.Get(ctx, key, got); err != nil {
				t.Fatalf("get proxy: %v", err)
			}
			if got.Status.PublishedRevision != "published-earlier" {
				t.Errorf("PublishedRevision = %q, want the previously published revision", got.Status.PublishedRevision)
			}
			if tc.deploymentWriteCompletes {
				// The Deployment write finished before the Service
				// write failed: a completed write alone must not
				// advance PublishedRevision.
				deploy := &appsv1.Deployment{}
				if err := cl.Get(ctx, key, deploy); err != nil || !metav1.IsControlledBy(deploy, proxy) {
					t.Errorf("deployment should be published and controlled before the service write fails, got %v", err)
				}
			}
		})
	}
}

// TestReconcileProxy_UnresolvedReferences verifies reference
// resolution gates publication: a spec referencing a Secret that does
// not exist is reported as ResolvedRefs=False and publishes nothing —
// the Deployment would mount or project the missing Secret and never
// start — and the proxy publishes once the reference resolves.
// Referenced Secrets are not owned by the proxy, so their creation
// cannot trigger a reconcile; the unresolved path requeues to poll.
func TestReconcileProxy_UnresolvedReferences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := proxyTestScheme(t)

	proxy := guardTestProxy()
	proxy.Spec.Listeners[0].TLS = &proxyv1alpha1.PraxisListenerTLS{
		CertificateRef: proxyv1alpha1.PraxisLocalReference{Name: "gw-cert"},
	}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&proxyv1alpha1.Proxy{}).
		WithObjects(proxy).Build()
	r := &ProxyReconciler{Client: cl, Scheme: scheme}
	key := client.ObjectKeyFromObject(proxy)

	res, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key})
	if err != nil {
		t.Fatalf("reconcile proxy: %v", err)
	}
	if res.RequeueAfter <= 0 {
		t.Errorf("reconcile of unresolved references must requeue, got %+v", res)
	}
	got := &proxyv1alpha1.Proxy{}
	if err := cl.Get(ctx, key, got); err != nil {
		t.Fatalf("get proxy: %v", err)
	}
	accepted := apimeta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeAccepted)
	if accepted == nil || accepted.Status != metav1.ConditionTrue || accepted.Reason != "Valid" {
		t.Errorf("Accepted condition = %+v, want True/Valid: the spec itself is valid", accepted)
	}
	resolved := apimeta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs)
	if resolved == nil || resolved.Status != metav1.ConditionFalse || resolved.Reason != "UnresolvedRefs" {
		t.Fatalf("ResolvedRefs condition = %+v, want False/UnresolvedRefs", resolved)
	}
	if !strings.Contains(resolved.Message, `secret "gw-cert" not found`) {
		t.Errorf("ResolvedRefs message %q does not name the missing secret", resolved.Message)
	}
	for _, obj := range []client.Object{
		&corev1.ConfigMap{},
		&appsv1.Deployment{},
		&corev1.Service{},
	} {
		if err := cl.Get(ctx, key, obj); !apierrors.IsNotFound(err) {
			t.Errorf("%T must not be published while references are unresolved, got %v", obj, err)
		}
	}

	// Creating the referenced secret lets the next reconcile publish.
	cert := &corev1.Secret{
		ObjectMeta: metav1.ObjectMeta{Name: "gw-cert", Namespace: proxy.Namespace},
		Data:       map[string][]byte{tlsCertificateKey: []byte("cert"), tlsPrivateKeyKey: []byte("key")},
	}
	if err := cl.Create(ctx, cert); err != nil {
		t.Fatalf("create secret: %v", err)
	}
	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile proxy after the secret appears: %v", err)
	}
	got = &proxyv1alpha1.Proxy{}
	if err := cl.Get(ctx, key, got); err != nil {
		t.Fatalf("get proxy after the secret appears: %v", err)
	}
	resolved = apimeta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs)
	if resolved == nil || resolved.Status != metav1.ConditionTrue || resolved.Reason != "ResolvedRefs" {
		t.Errorf("ResolvedRefs condition = %+v, want True/ResolvedRefs after the secret appears", resolved)
	}
	programmed := apimeta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeProgrammed)
	if programmed == nil || programmed.Status != metav1.ConditionTrue {
		t.Errorf("Programmed condition = %+v, want True after the secret appears", programmed)
	}
}

// TestReconcileProxy_UnresolvedReferencesBoundsMessage verifies the
// unresolved-reference report stays within the condition-message limit
// the API server enforces: the report quotes reference names from the
// spec, which validation does not length-bound.
func TestReconcileProxy_UnresolvedReferencesBoundsMessage(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := proxyTestScheme(t)

	proxy := guardTestProxy()
	proxy.Spec.SecretEnv = []proxyv1alpha1.PraxisSecretEnv{{
		Name:      strings.Repeat("e", maxConditionMessageLen),
		SecretRef: proxyv1alpha1.PraxisSecretKeyRef{Name: "missing", Key: "api_key"},
	}}

	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&proxyv1alpha1.Proxy{}).
		WithObjects(proxy).Build()
	r := &ProxyReconciler{Client: cl, Scheme: scheme}
	key := client.ObjectKeyFromObject(proxy)

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile proxy: %v", err)
	}
	got := &proxyv1alpha1.Proxy{}
	if err := cl.Get(ctx, key, got); err != nil {
		t.Fatalf("get proxy: %v", err)
	}
	resolved := apimeta.FindStatusCondition(got.Status.Conditions, proxyv1alpha1.ConditionTypeResolvedRefs)
	if resolved == nil || resolved.Status != metav1.ConditionFalse || resolved.Reason != "UnresolvedRefs" {
		t.Fatalf("ResolvedRefs condition = %+v, want False/UnresolvedRefs", resolved)
	}
	if len(resolved.Message) > maxConditionMessageLen {
		t.Errorf("ResolvedRefs message is %d bytes, want at most %d", len(resolved.Message), maxConditionMessageLen)
	}
}

// TestResolveReferences verifies the pre-publication reference check:
// every Secret the spec references — listener TLS certificates and
// projected environment keys — must exist with the keys the Deployment
// mounts and projects, every unresolved reference is reported, and a
// spec without references resolves trivially.
func TestResolveReferences(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := proxyTestScheme(t)
	tlsListener := func(secret string) proxyv1alpha1.PraxisListener {
		return proxyv1alpha1.PraxisListener{
			Name:         "https",
			Port:         8443,
			FilterChains: []string{"main"},
			TLS:          &proxyv1alpha1.PraxisListenerTLS{CertificateRef: proxyv1alpha1.PraxisLocalReference{Name: secret}},
		}
	}
	certSecret := func(name string, keys ...string) *corev1.Secret {
		data := map[string][]byte{}
		for _, key := range keys {
			data[key] = []byte(key)
		}
		return &corev1.Secret{ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"}, Data: data}
	}

	tests := []struct {
		name    string
		proxy   *proxyv1alpha1.Proxy
		objects []client.Object
		want    []string
	}{
		{
			name:  "inline-only spec resolves trivially",
			proxy: guardTestProxy(),
		},
		{
			name: "missing TLS certificate secret",
			proxy: func() *proxyv1alpha1.Proxy {
				proxy := guardTestProxy()
				proxy.Spec.Listeners[0].TLS = &proxyv1alpha1.PraxisListenerTLS{
					CertificateRef: proxyv1alpha1.PraxisLocalReference{Name: "missing-cert"},
				}
				return proxy
			}(),
			want: []string{`listener "http": TLS certificate secret "missing-cert" not found`},
		},
		{
			name: "TLS certificate secret lacking the private key",
			proxy: func() *proxyv1alpha1.Proxy {
				proxy := guardTestProxy()
				proxy.Spec.Listeners[0] = tlsListener("partial-cert")
				return proxy
			}(),
			objects: []client.Object{certSecret("partial-cert", tlsCertificateKey)},
			want:    []string{`listener "https": TLS certificate secret "partial-cert" lacks key "tls.key"`},
		},
		{
			name: "complete TLS certificate secret resolves",
			proxy: func() *proxyv1alpha1.Proxy {
				proxy := guardTestProxy()
				proxy.Spec.Listeners[0] = tlsListener("full-cert")
				return proxy
			}(),
			objects: []client.Object{certSecret("full-cert", tlsCertificateKey, tlsPrivateKeyKey)},
		},
		{
			name: "missing secretEnv secret",
			proxy: func() *proxyv1alpha1.Proxy {
				proxy := guardTestProxy()
				proxy.Spec.SecretEnv = []proxyv1alpha1.PraxisSecretEnv{{
					Name:      "API_KEY",
					SecretRef: proxyv1alpha1.PraxisSecretKeyRef{Name: "api-creds", Key: "api_key"},
				}}
				return proxy
			}(),
			want: []string{`secretEnv "API_KEY": secret "api-creds" not found`},
		},
		{
			name: "secretEnv secret lacking the key",
			proxy: func() *proxyv1alpha1.Proxy {
				proxy := guardTestProxy()
				proxy.Spec.SecretEnv = []proxyv1alpha1.PraxisSecretEnv{{
					Name:      "API_KEY",
					SecretRef: proxyv1alpha1.PraxisSecretKeyRef{Name: "api-creds", Key: "api_key"},
				}}
				return proxy
			}(),
			objects: []client.Object{certSecret("api-creds", "other_key")},
			want:    []string{`secretEnv "API_KEY": secret "api-creds" lacks key "api_key"`},
		},
		{
			name: "complete secretEnv secret resolves",
			proxy: func() *proxyv1alpha1.Proxy {
				proxy := guardTestProxy()
				proxy.Spec.SecretEnv = []proxyv1alpha1.PraxisSecretEnv{{
					Name:      "API_KEY",
					SecretRef: proxyv1alpha1.PraxisSecretKeyRef{Name: "api-creds", Key: "api_key"},
				}}
				return proxy
			}(),
			objects: []client.Object{certSecret("api-creds", "api_key")},
		},
		{
			name: "every unresolved reference is reported",
			proxy: func() *proxyv1alpha1.Proxy {
				proxy := guardTestProxy()
				proxy.Spec.Listeners[0] = tlsListener("missing-cert")
				proxy.Spec.SecretEnv = []proxyv1alpha1.PraxisSecretEnv{{
					Name:      "API_KEY",
					SecretRef: proxyv1alpha1.PraxisSecretKeyRef{Name: "api-creds", Key: "api_key"},
				}}
				return proxy
			}(),
			want: []string{
				`listener "https": TLS certificate secret "missing-cert" not found`,
				`secretEnv "API_KEY": secret "api-creds" not found`,
			},
		},
	}
	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			t.Parallel()

			cl := fake.NewClientBuilder().WithScheme(scheme).
				WithObjects(tc.objects...).Build()
			r := &ProxyReconciler{Client: cl, Scheme: scheme}

			got := r.resolveReferences(ctx, tc.proxy)
			if !reflect.DeepEqual(got, tc.want) {
				t.Errorf("resolveReferences() = %q, want %q", got, tc.want)
			}
		})
	}
}

// TestBuildPodTemplate_ReadinessProbe verifies the generated container
// carries a readiness probe wired to a listener the kubelet can reach
// through the pod IP. The Praxis runtime has no dedicated health
// endpoint, so a TCP probe on a bound, pod-IP-reachable listener is the
// readiness signal: a replacement pod remains unready during startup
// until Praxis has bound the listener and can accept traffic.
func TestBuildPodTemplate_ReadinessProbe(t *testing.T) {
	t.Parallel()

	t.Run("probes the first listener port by name", func(t *testing.T) {
		t.Parallel()
		proxy := guardTestProxy()
		// Bound to the dual-stack wildcard so the probe can target it
		// directly; the default bind renders the IPv4 wildcard, which is
		// not reachable through an IPv6 pod IP.
		dualStack := "::"
		proxy.Spec.Listeners[0].BindAddress = &dualStack
		template := buildPodTemplate(proxy, "rev", "")
		containers := template.Spec.Containers
		if len(containers) != 1 {
			t.Fatalf("containers: %d", len(containers))
		}
		probe := containers[0].ReadinessProbe
		if probe == nil || probe.TCPSocket == nil {
			t.Fatalf("readiness probe must be a TCP probe, got: %+v", probe)
		}
		if got := probe.TCPSocket.Port.StrVal; got != "http" {
			t.Errorf("probe port %q must reference the first listener's named port", got)
		}
	})

	t.Run("keeps targeting the first listener with several listeners", func(t *testing.T) {
		t.Parallel()
		proxy := guardTestProxy()
		dualStack := "::"
		proxy.Spec.Listeners[0].BindAddress = &dualStack
		proxy.Spec.Listeners = append(proxy.Spec.Listeners, proxyv1alpha1.PraxisListener{
			Name:         "health",
			Port:         8081,
			FilterChains: []string{"main"},
		})
		probe := buildPodTemplate(proxy, "rev", "").Spec.Containers[0].ReadinessProbe
		if probe == nil || probe.TCPSocket == nil || probe.TCPSocket.Port.StrVal != "http" {
			t.Fatalf("probe must target the first listener, got: %+v", probe)
		}
	})

	t.Run("omits the probe without listeners", func(t *testing.T) {
		t.Parallel()
		proxy := guardTestProxy()
		proxy.Spec.Listeners = nil
		if probe := buildPodTemplate(proxy, "rev", "").Spec.Containers[0].ReadinessProbe; probe != nil {
			t.Errorf("expected no readiness probe without listeners, got: %+v", probe)
		}
	})

	t.Run("probes the first listener bound to the dual-stack wildcard", func(t *testing.T) {
		t.Parallel()
		loopback := "127.0.0.1"
		dualStack := "::"
		proxy := guardTestProxy()
		proxy.Spec.Listeners[0].BindAddress = &loopback
		proxy.Spec.Listeners = append(proxy.Spec.Listeners, proxyv1alpha1.PraxisListener{
			Name:         "public",
			Port:         8081,
			BindAddress:  &dualStack,
			FilterChains: []string{"main"},
		})
		probe := buildPodTemplate(proxy, "rev", "").Spec.Containers[0].ReadinessProbe
		if probe == nil || probe.TCPSocket == nil || probe.TCPSocket.Port.StrVal != "public" {
			t.Fatalf("probe must target the first pod-IP-reachable listener, got: %+v", probe)
		}
	})

	t.Run("targets the dedicated readiness listener for IPv4-wildcard binds", func(t *testing.T) {
		t.Parallel()
		// The IPv4 wildcard is the API default; it never answers the
		// kubelet's dial to an IPv6 pod IP, so the probe must target the
		// controller's dedicated dual-stack listener instead.
		ipv4Wildcard := "0.0.0.0"
		proxy := guardTestProxy()
		proxy.Spec.Listeners[0].BindAddress = &ipv4Wildcard
		template := buildPodTemplate(proxy, "rev", "")
		probe := template.Spec.Containers[0].ReadinessProbe
		if probe == nil || probe.TCPSocket == nil || probe.TCPSocket.Port.StrVal != "readiness" {
			t.Fatalf("probe must target the dedicated readiness listener, got: %+v", probe)
		}
		var readinessPort int32
		for _, port := range template.Spec.Containers[0].Ports {
			if port.Name == "readiness" {
				readinessPort = port.ContainerPort
			}
		}
		if readinessPort != 19902 {
			t.Errorf("container must declare the readiness port 19902, got: %d", readinessPort)
		}
	})

	t.Run("targets the dedicated readiness listener without a reachable listener", func(t *testing.T) {
		t.Parallel()
		loopback := "127.0.0.1"
		proxy := guardTestProxy()
		proxy.Spec.Listeners[0].BindAddress = &loopback
		template := buildPodTemplate(proxy, "rev", "")
		probe := template.Spec.Containers[0].ReadinessProbe
		if probe == nil || probe.TCPSocket == nil || probe.TCPSocket.Port.StrVal != "readiness" {
			t.Fatalf("probe must target the dedicated readiness listener, got: %+v", probe)
		}
		var readinessPort int32
		for _, port := range template.Spec.Containers[0].Ports {
			if port.Name == "readiness" {
				readinessPort = port.ContainerPort
			}
		}
		if readinessPort != 19902 {
			t.Errorf("container must declare the readiness port 19902, got: %d", readinessPort)
		}
	})

}

// TestBuildPodTemplate_ReadinessProbeFamily verifies the probe wiring
// follows the pod network's primary family instead of always relying
// on a :: bind, which an IPv6-less or IPv6-only pod network cannot
// serve: on IPv4-primary clusters the default bind is probed directly
// and the dedicated readiness listener binds the IPv4 wildcard.
func TestBuildPodTemplate_ReadinessProbeFamily(t *testing.T) {
	t.Parallel()

	t.Run("probes the default bind directly on IPv4-primary clusters", func(t *testing.T) {
		t.Parallel()
		// The default bind renders the IPv4 wildcard, which answers the
		// kubelet's dial to the pod's IPv4 address: the probe targets
		// the spec listener and the container declares no dedicated
		// readiness port.
		proxy := guardTestProxy()
		template := buildPodTemplate(proxy, "rev", corev1.IPv4Protocol)
		probe := template.Spec.Containers[0].ReadinessProbe
		if probe == nil || probe.TCPSocket == nil || probe.TCPSocket.Port.StrVal != "http" {
			t.Fatalf("probe must target the default-bound listener, got: %+v", probe)
		}
		for _, port := range template.Spec.Containers[0].Ports {
			if port.Name == "readiness" {
				t.Errorf("container must not declare a readiness port, got: %d", port.ContainerPort)
			}
		}
	})

	t.Run("targets the dedicated readiness listener for loopback binds on IPv4-primary clusters", func(t *testing.T) {
		t.Parallel()
		// Loopback binds are unreachable through the pod IP on any
		// family, so the probe targets the dedicated readiness listener;
		// it binds the primary family's IPv4 wildcard, not ::.
		loopback := "127.0.0.1"
		proxy := guardTestProxy()
		proxy.Spec.Listeners[0].BindAddress = &loopback
		template := buildPodTemplate(proxy, "rev", corev1.IPv4Protocol)
		probe := template.Spec.Containers[0].ReadinessProbe
		if probe == nil || probe.TCPSocket == nil || probe.TCPSocket.Port.StrVal != "readiness" {
			t.Fatalf("probe must target the dedicated readiness listener, got: %+v", probe)
		}
		var readinessPort int32
		for _, port := range template.Spec.Containers[0].Ports {
			if port.Name == "readiness" {
				readinessPort = port.ContainerPort
			}
		}
		if readinessPort != 19902 {
			t.Errorf("container must declare the readiness port 19902, got: %d", readinessPort)
		}
	})
}

func TestReconcileConfigMap_LiveUpdates(t *testing.T) {
	t.Parallel()
	proxy := guardTestProxy()
	scheme := proxyTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(proxy).Build()
	r := &ProxyReconciler{Client: cl, Scheme: scheme}
	ctx := context.Background()
	for _, config := range []string{"config-v1", "config-v2", "config-v2"} {
		if err := r.reconcileConfigMap(ctx, proxy, config); err != nil {
			t.Fatalf("publish config: %v", err)
		}
		cm := fetchConfigMap(t, cl, configMapName(proxy))
		if len(cm.Data) != 1 || cm.Data[configFileName] != config {
			t.Errorf("published config = %v, want only %q", cm.Data, config)
		}
		if owner := metav1.GetControllerOf(cm); owner == nil || owner.UID != proxy.UID {
			t.Errorf("config controller owner = %+v", owner)
		}
	}
	var maps corev1.ConfigMapList
	if err := cl.List(ctx, &maps); err != nil {
		t.Fatal(err)
	}
	if len(maps.Items) != 1 {
		t.Errorf("live publication created %d ConfigMaps, want 1", len(maps.Items))
	}
}

func assertLiveConfigMount(t *testing.T, deploy *appsv1.Deployment) {
	t.Helper()
	mounts := deploy.Spec.Template.Spec.Containers[0].VolumeMounts
	if len(mounts) != 1 {
		t.Fatalf("config volume mounts = %d, want 1", len(mounts))
	}
	if mounts[0].SubPath != "" || mounts[0].MountPath != configMountPath {
		t.Errorf("config volume must mount the live directory, got %+v", mounts[0])
	}
	volumes := deploy.Spec.Template.Spec.Volumes
	if len(volumes) != 1 || volumes[0].ConfigMap == nil || volumes[0].ConfigMap.Name != "gw-config" {
		t.Errorf("config volume must reference the live ConfigMap, got %+v", volumes)
	}
}

// TestReconcileProxy_ChildNamingAndSelectors pins the derivations the
// child resources share with each other: the proxy name — validated by
// the CEL rules on the Proxy type to be a DNS label of at most 63
// characters — names the Deployment, the Service, and the
// app.kubernetes.io/instance label value (within the 63-character
// label-value limit), and the same label set selects the Deployment's
// pods and the Service's endpoints. Listener names — validated against
// the IANA_SVC_NAME port-name constraints (15 characters) — are reused
// as the container port name, the Service port name, and the Service
// targetPort.
func TestReconcileProxy_ChildNamingAndSelectors(t *testing.T) {
	t.Parallel()

	ctx := context.Background()
	scheme := proxyTestScheme(t)
	proxy := guardTestProxy()
	cl := fake.NewClientBuilder().WithScheme(scheme).
		WithStatusSubresource(&proxyv1alpha1.Proxy{}).
		WithObjects(proxy).Build()
	r := &ProxyReconciler{Client: cl, Scheme: scheme}
	key := client.ObjectKeyFromObject(proxy)

	if _, err := r.Reconcile(ctx, ctrl.Request{NamespacedName: key}); err != nil {
		t.Fatalf("reconcile proxy: %v", err)
	}

	deploy := &appsv1.Deployment{}
	if err := cl.Get(ctx, key, deploy); err != nil {
		t.Fatalf("get deployment: %v", err)
	}
	svc := &corev1.Service{}
	if err := cl.Get(ctx, key, svc); err != nil {
		t.Fatalf("get service: %v", err)
	}

	if deploy.Name != proxy.Name || svc.Name != proxy.Name {
		t.Errorf("child names = deployment %q, service %q, want the proxy name %q for both", deploy.Name, svc.Name, proxy.Name)
	}
	labels := proxyLabels(proxy)
	if instance := labels["app.kubernetes.io/instance"]; instance != proxy.Name {
		t.Errorf("app.kubernetes.io/instance = %q, want the proxy name %q", instance, proxy.Name)
	}
	if !reflect.DeepEqual(deploy.Spec.Selector.MatchLabels, labels) {
		t.Errorf("deployment selector = %v, want %v", deploy.Spec.Selector.MatchLabels, labels)
	}
	if !reflect.DeepEqual(deploy.Spec.Template.Labels, labels) {
		t.Errorf("pod template labels = %v, want %v", deploy.Spec.Template.Labels, labels)
	}
	if !reflect.DeepEqual(svc.Spec.Selector, labels) {
		t.Errorf("service selector = %v, want %v", svc.Spec.Selector, labels)
	}

	var containerPort *corev1.ContainerPort
	for i := range deploy.Spec.Template.Spec.Containers[0].Ports {
		if deploy.Spec.Template.Spec.Containers[0].Ports[i].Name == "http" {
			containerPort = &deploy.Spec.Template.Spec.Containers[0].Ports[i]
		}
	}
	if containerPort == nil {
		t.Fatalf("container ports %+v have no port named %q", deploy.Spec.Template.Spec.Containers[0].Ports, "http")
	}
	if containerPort.ContainerPort != 8080 {
		t.Errorf("container port = %d, want the listener port 8080", containerPort.ContainerPort)
	}
	if len(svc.Spec.Ports) != 1 {
		t.Fatalf("service ports = %+v, want one", svc.Spec.Ports)
	}
	if svc.Spec.Ports[0].Name != "http" || svc.Spec.Ports[0].TargetPort.String() != "http" {
		t.Errorf("service port = %+v, want name and targetPort to reuse the listener name %q", svc.Spec.Ports[0], "http")
	}
	if svc.Spec.Ports[0].Port != 8080 {
		t.Errorf("service port number = %d, want the listener port 8080", svc.Spec.Ports[0].Port)
	}
}
