package nodeops

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestCordonPatch(t *testing.T) {
	for _, unschedulable := range []bool{true, false} {
		patch := cordonPatch(unschedulable)
		if !json.Valid(patch) {
			t.Errorf("cordonPatch(%v) is not valid JSON: %s", unschedulable, patch)
		}
		if !strings.Contains(string(patch), "unschedulable") {
			t.Errorf("cordonPatch(%v) = %s, want it to contain %q", unschedulable, patch, "unschedulable")
		}
		// The strategic-merge patch must decode back to the exact spec we intend,
		// so a typo in the literal can never silently ship the wrong value.
		var decoded struct {
			Spec struct {
				Unschedulable bool `json:"unschedulable"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(patch, &decoded); err != nil {
			t.Fatalf("cordonPatch(%v): unmarshal: %v", unschedulable, err)
		}
		if decoded.Spec.Unschedulable != unschedulable {
			t.Errorf("cordonPatch(%v) decoded unschedulable = %v", unschedulable, decoded.Spec.Unschedulable)
		}
	}
}

func TestCordon(t *testing.T) {
	const name = "node-1"
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: name}}
	cs := fake.NewClientset(node)
	ctx := context.Background()

	if err := Cordon(ctx, cs, name); err != nil {
		t.Fatalf("Cordon: unexpected error %v", err)
	}
	got, err := cs.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get node after Cordon: %v", err)
	}
	if !got.Spec.Unschedulable {
		t.Errorf("after Cordon, Spec.Unschedulable = false, want true")
	}
}

func TestUncordon(t *testing.T) {
	const name = "node-1"
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: true},
	}
	cs := fake.NewClientset(node)
	ctx := context.Background()

	if err := Uncordon(ctx, cs, name); err != nil {
		t.Fatalf("Uncordon: unexpected error %v", err)
	}
	got, err := cs.CoreV1().Nodes().Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get node after Uncordon: %v", err)
	}
	if got.Spec.Unschedulable {
		t.Errorf("after Uncordon, Spec.Unschedulable = true, want false")
	}
}

func TestDrainablePods(t *testing.T) {
	dsPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "ds-pod",
			Namespace: "kube-system",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "DaemonSet", Name: "kube-proxy"},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	mirrorPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:        "mirror-pod",
			Namespace:   "kube-system",
			Annotations: map[string]string{mirrorPodAnnotation: "abc123"},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
	succeededPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "job-pod", Namespace: "batch"},
		Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
	}
	failedPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "crash-pod", Namespace: "batch"},
		Status:     corev1.PodStatus{Phase: corev1.PodFailed},
	}
	normalPod := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:      "web",
			Namespace: "default",
			OwnerReferences: []metav1.OwnerReference{
				{Kind: "ReplicaSet", Name: "web-abc"},
			},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}

	pods := []corev1.Pod{dsPod, mirrorPod, succeededPod, failedPod, normalPod}
	evict, skip := drainablePods(pods)

	evictNames := podNames(evict)
	skipNames := podNames(skip)

	if len(evict) != 1 || evictNames["web"] != true {
		t.Errorf("evict = %v, want only [web]", keys(evictNames))
	}
	for _, want := range []string{"ds-pod", "mirror-pod", "job-pod", "crash-pod"} {
		if !skipNames[want] {
			t.Errorf("skip = %v, want it to contain %q", keys(skipNames), want)
		}
	}
	if skipNames["web"] {
		t.Errorf("normal Running pod %q was skipped, want evicted", "web")
	}
}

func podNames(pods []corev1.Pod) map[string]bool {
	m := make(map[string]bool, len(pods))
	for _, p := range pods {
		m[p.Name] = true
	}
	return m
}

func keys(m map[string]bool) []string {
	out := make([]string, 0, len(m))
	for k := range m {
		out = append(out, k)
	}
	return out
}
