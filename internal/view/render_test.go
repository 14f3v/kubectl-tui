package view

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/tenant"
)

func TestTenantsPageRender(t *testing.T) {
	p := &tenantsPage{
		theme:     style.Default(),
		loaded:    true,
		available: true,
		views: []tenant.View{
			{Name: "payments", Tier: "gold", Owner: "fin", NS: 6, Pods: 42,
				CPUUsed: 12400, CPUQuota: 20000, MemUsed: 40 << 30, MemQuota: 64 << 30,
				Status: "Healthy", StatusClass: columns.StatusOK},
			{Name: "ml-serving", Tier: "gold", Owner: "ml", NS: 5, Pods: 31,
				CPUUsed: 34000, CPUQuota: 30000, MemUsed: 61 << 30, MemQuota: 56 << 30,
				Status: "Over quota", StatusClass: columns.StatusError},
		},
	}
	out := p.View(140, 20)
	for _, want := range []string{"payments", "ml-serving", "Healthy", "Over quota", "TENANT", "gold"} {
		if !strings.Contains(out, want) {
			t.Fatalf("tenants render missing %q:\n%s", want, out)
		}
	}
	// Summary tallies by status class.
	s := p.Summary()
	if s.Total != 2 || s.OK != 1 || s.Err != 1 {
		t.Fatalf("tenants summary = %+v, want 2/1ok/1err", s)
	}
}

func TestTenantsUnavailableRender(t *testing.T) {
	p := &tenantsPage{theme: style.Default(), loaded: true, available: false, reason: "forbidden"}
	out := p.View(120, 10)
	if !strings.Contains(out, "not permitted") {
		t.Fatalf("forbidden banner missing:\n%s", out)
	}
}

func TestContainersPageRender(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web-0", Namespace: "default"},
		Spec: corev1.PodSpec{Containers: []corev1.Container{
			{Name: "app", Image: "nginx:1.27"},
			{Name: "sidecar", Image: "envoy:1.30"},
		}},
		Status: corev1.PodStatus{ContainerStatuses: []corev1.ContainerStatus{
			{Name: "app", Ready: true, RestartCount: 2, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Name: "sidecar", Ready: false, State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
		}},
	}
	p := newContainersPage(nil, style.Default(), pod)
	if len(p.containers) != 2 {
		t.Fatalf("expected 2 containers, got %d", len(p.containers))
	}
	out := p.View(120, 12)
	for _, want := range []string{"app", "sidecar", "nginx:1.27", "CrashLoopBackOff", "Running"} {
		if !strings.Contains(out, want) {
			t.Fatalf("containers render missing %q:\n%s", want, out)
		}
	}
}
