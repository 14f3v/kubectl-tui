package write

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	dynamicfake "k8s.io/client-go/dynamic/fake"
	"k8s.io/client-go/kubernetes/scheme"
)

var podGVR = schema.GroupVersionResource{Group: "", Version: "v1", Resource: "pods"}

func TestDeleteNamespaced(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"}}
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme, pod)

	if err := Delete(context.Background(), dyn, podGVR, true, "default", "web", false); err != nil {
		t.Fatalf("delete: %v", err)
	}
	_, err := dyn.Resource(podGVR).Namespace("default").Get(context.Background(), "web", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("pod still present after delete: err=%v", err)
	}
}

func TestForceDeleteRemoves(t *testing.T) {
	pod := &corev1.Pod{ObjectMeta: metav1.ObjectMeta{Name: "stuck", Namespace: "kube-system"}}
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme, pod)

	if err := Delete(context.Background(), dyn, podGVR, true, "kube-system", "stuck", true); err != nil {
		t.Fatalf("force delete: %v", err)
	}
	_, err := dyn.Resource(podGVR).Namespace("kube-system").Get(context.Background(), "stuck", metav1.GetOptions{})
	if !apierrors.IsNotFound(err) {
		t.Fatalf("pod still present after force delete: err=%v", err)
	}
}

func TestDeleteMissingIsNotFound(t *testing.T) {
	dyn := dynamicfake.NewSimpleDynamicClient(scheme.Scheme)
	err := Delete(context.Background(), dyn, podGVR, true, "default", "ghost", false)
	if !apierrors.IsNotFound(err) {
		t.Fatalf("deleting a missing object should be NotFound, got %v", err)
	}
}

// keep runtime imported for scheme usage clarity
var _ runtime.Object = (*corev1.Pod)(nil)
