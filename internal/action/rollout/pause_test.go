package rollout

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestPausable(t *testing.T) {
	cases := map[string]bool{
		"deployments":  true,
		"statefulsets": false,
		"daemonsets":   false,
		"replicasets":  false,
		"pods":         false,
		"":             false,
		"Deployments":  false, // case-sensitive: only lowercase plural is accepted
	}
	for kind, want := range cases {
		if got := Pausable(kind); got != want {
			t.Errorf("Pausable(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestSetPaused(t *testing.T) {
	const (
		ns   = "default"
		name = "web"
	)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
	}
	cs := fake.NewClientset(dep)
	ctx := context.Background()

	// Pause: patch spec.paused=true, then confirm it round-trips via Get.
	if err := SetPaused(ctx, cs, ns, name, true); err != nil {
		t.Fatalf("SetPaused(true): unexpected error %v", err)
	}
	got, err := cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after pause: unexpected error %v", err)
	}
	if !got.Spec.Paused {
		t.Errorf("after SetPaused(true), Spec.Paused = false, want true")
	}

	// Resume: patch spec.paused=false, then confirm it round-trips via Get.
	if err := SetPaused(ctx, cs, ns, name, false); err != nil {
		t.Fatalf("SetPaused(false): unexpected error %v", err)
	}
	got, err = cs.AppsV1().Deployments(ns).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after resume: unexpected error %v", err)
	}
	if got.Spec.Paused {
		t.Errorf("after SetPaused(false), Spec.Paused = true, want false")
	}
}
