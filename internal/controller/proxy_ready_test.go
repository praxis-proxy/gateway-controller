package controller

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	"k8s.io/apimachinery/pkg/api/meta"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"sigs.k8s.io/controller-runtime/pkg/client/fake"

	proxyv1alpha1 "github.com/praxis-proxy/gateway-controller/api/proxy/v1alpha1"
)

func TestUpdateReadyStatus_DesiredWorkload(t *testing.T) {
	t.Parallel()
	for _, current := range []bool{false, true} {
		name := "stale cached deployment"
		if current {
			name = "desired deployment"
		}
		t.Run(name, func(t *testing.T) {
			t.Parallel()
			proxy := guardTestProxy()
			proxy.Status.DesiredRevision = "new-config"
			proxy.Status.PublishedRevision = "new-config"
			startupRevision, err := computeStartupRevision(&proxy.Spec)
			if err != nil {
				t.Fatal(err)
			}
			if !current {
				startupRevision = "previous-startup-config"
			}
			deploy := &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Name: proxy.Name, Namespace: proxy.Namespace, Generation: 1},
				Spec:       appsv1.DeploymentSpec{Replicas: new(int32(1)), Template: buildPodTemplate(proxy, startupRevision, "")},
				Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1},
			}
			scheme := proxyTestScheme(t)
			cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deploy).Build()
			r := &ProxyReconciler{Client: cl, Scheme: scheme}
			if err := r.updateReadyStatus(context.Background(), proxy); err != nil {
				t.Fatal(err)
			}
			condition := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeReady)
			want := metav1.ConditionFalse
			if current {
				want = metav1.ConditionTrue
			}
			if condition == nil || condition.Status != want {
				t.Errorf("Ready = %+v, want %s", condition, want)
			}
			if proxy.Status.PublishedRevision != "new-config" {
				t.Errorf("workload observation changed the published config revision")
			}
		})
	}
}

func TestUpdateReadyStatus_StaleScale(t *testing.T) {
	t.Parallel()
	proxy := guardTestProxy()
	proxy.Spec.Replicas = new(int32(3))
	startupRevision, err := computeStartupRevision(&proxy.Spec)
	if err != nil {
		t.Fatal(err)
	}
	deploy := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: proxy.Name, Namespace: proxy.Namespace, Generation: 1},
		Spec:       appsv1.DeploymentSpec{Replicas: new(int32(1)), Template: buildPodTemplate(proxy, startupRevision, "")},
		Status:     appsv1.DeploymentStatus{ObservedGeneration: 1, Replicas: 1, UpdatedReplicas: 1, ReadyReplicas: 1},
	}
	scheme := proxyTestScheme(t)
	cl := fake.NewClientBuilder().WithScheme(scheme).WithObjects(deploy).Build()
	r := &ProxyReconciler{Client: cl, Scheme: scheme}
	if err := r.updateReadyStatus(context.Background(), proxy); err != nil {
		t.Fatal(err)
	}
	condition := meta.FindStatusCondition(proxy.Status.Conditions, proxyv1alpha1.ConditionTypeReady)
	if condition == nil || condition.Status != metav1.ConditionFalse || condition.Reason != "RollingOut" {
		t.Errorf("Ready = %+v, want False/RollingOut for desired scale", condition)
	}
}
