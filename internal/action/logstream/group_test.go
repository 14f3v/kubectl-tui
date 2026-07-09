package logstream

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func pod(name string, labels map[string]string, containers ...string) *corev1.Pod {
	cs := make([]corev1.Container, 0, len(containers))
	for _, c := range containers {
		cs = append(cs, corev1.Container{Name: c})
	}
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: labels},
		Spec:       corev1.PodSpec{Containers: cs},
	}
}

// TestPodRefsForSelector verifies selector expansion: only matching pods are
// included, single-container pods tag by pod name, and multi-container pods
// disambiguate with pod/container.
func TestPodRefsForSelector(t *testing.T) {
	web := map[string]string{"app": "web"}
	cs := fake.NewClientset(
		pod("web-1", web, "c1"),                     // single container
		pod("web-2", web, "c1", "c2"),               // two containers
		pod("db-1", map[string]string{"app": "db"}), // non-matching (excluded)
	)

	sel := &metav1.LabelSelector{MatchLabels: web}
	refs, err := PodRefsForSelector(context.Background(), cs, "default", sel)
	if err != nil {
		t.Fatalf("PodRefsForSelector: unexpected error: %v", err)
	}

	// web-1 -> 1 ref, web-2 -> 2 refs, db-1 excluded => 3 total.
	if len(refs) != 3 {
		t.Fatalf("got %d refs, want 3: %+v", len(refs), refs)
	}

	want := map[string]PodRef{
		"web-1":    {Pod: "web-1", Container: "c1", Tag: "web-1"},
		"web-2/c1": {Pod: "web-2", Container: "c1", Tag: "web-2/c1"},
		"web-2/c2": {Pod: "web-2", Container: "c2", Tag: "web-2/c2"},
	}
	got := map[string]PodRef{}
	for _, r := range refs {
		got[r.Tag] = r
		if r.Pod == "db-1" {
			t.Fatalf("non-matching pod db-1 leaked into refs: %+v", r)
		}
	}
	for tag, w := range want {
		g, ok := got[tag]
		if !ok {
			t.Fatalf("missing ref for tag %q; got tags %v", tag, keys(got))
		}
		if g != w {
			t.Fatalf("ref[%q] = %+v, want %+v", tag, g, w)
		}
	}
}

// TestPodRefsForSelectorNil ensures a workload with no selector is rejected rather
// than silently tailing the whole namespace.
func TestPodRefsForSelectorNil(t *testing.T) {
	cs := fake.NewClientset()
	if _, err := PodRefsForSelector(context.Background(), cs, "default", nil); err == nil {
		t.Fatalf("expected error for nil selector, got nil")
	}
}

func TestTaggedLine(t *testing.T) {
	if got := taggedLine("p", "hello"); got != "[p] hello" {
		t.Fatalf("taggedLine = %q, want %q", got, "[p] hello")
	}
}

func keys(m map[string]PodRef) []string {
	ks := make([]string, 0, len(m))
	for k := range m {
		ks = append(ks, k)
	}
	return ks
}
