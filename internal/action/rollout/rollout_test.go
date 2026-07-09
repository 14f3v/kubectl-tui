package rollout

import (
	"context"
	"encoding/json"
	"strings"
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

// int32p is a local helper for building *int32 spec fields in tests.
func int32p(v int32) *int32 { return &v }

func TestRestartable(t *testing.T) {
	cases := map[string]bool{
		"deployments":  true,
		"statefulsets": true,
		"daemonsets":   true,
		"pods":         false,
		"jobs":         false,
		"":             false,
	}
	for kind, want := range cases {
		if got := Restartable(kind); got != want {
			t.Errorf("Restartable(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestRestartPatch(t *testing.T) {
	now := time.Date(2026, 7, 9, 12, 30, 0, 0, time.UTC)
	patch := RestartPatch(now)

	if !json.Valid(patch) {
		t.Fatalf("patch is not valid JSON: %s", patch)
	}
	if !strings.Contains(string(patch), "kubectl.kubernetes.io/restartedAt") {
		t.Fatalf("patch missing restartedAt annotation: %s", patch)
	}

	// The stamped value must be a valid RFC3339 time equal to the input in UTC.
	var decoded struct {
		Spec struct {
			Template struct {
				Metadata struct {
					Annotations map[string]string `json:"annotations"`
				} `json:"metadata"`
			} `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(patch, &decoded); err != nil {
		t.Fatalf("unmarshal patch: %v", err)
	}
	ts := decoded.Spec.Template.Metadata.Annotations["kubectl.kubernetes.io/restartedAt"]
	parsed, err := time.Parse(time.RFC3339, ts)
	if err != nil {
		t.Fatalf("restartedAt %q is not RFC3339: %v", ts, err)
	}
	if !parsed.Equal(now) {
		t.Fatalf("stamped time = %v, want %v", parsed, now)
	}
}

func TestRestart(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       appsv1.DeploymentSpec{Replicas: int32p(3)},
	}
	cs := fake.NewClientset(dep)
	now := time.Date(2026, 7, 9, 8, 0, 0, 0, time.UTC)

	if err := Restart(context.Background(), cs, "deployments", "default", "web", now); err != nil {
		t.Fatalf("Restart: %v", err)
	}

	got, err := cs.AppsV1().Deployments("default").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("get after restart: %v", err)
	}
	ann := got.Spec.Template.ObjectMeta.Annotations["kubectl.kubernetes.io/restartedAt"]
	if ann == "" {
		t.Fatalf("restart annotation not stamped on pod template: %+v", got.Spec.Template.ObjectMeta.Annotations)
	}
	if want := now.UTC().Format(time.RFC3339); ann != want {
		t.Fatalf("stamped annotation = %q, want %q", ann, want)
	}
}

func TestRestartUnknownKind(t *testing.T) {
	cs := fake.NewClientset()
	err := Restart(context.Background(), cs, "pods", "default", "web", time.Now())
	if err == nil {
		t.Fatal("expected error for unsupported kind, got nil")
	}
	if !strings.Contains(err.Error(), "restart not supported") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStatus(t *testing.T) {
	tests := []struct {
		name     string
		obj      any
		wantSub  string
		wantDone bool
	}{
		{
			name: "deployment in progress: spec not observed",
			obj: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 2},
				Spec:       appsv1.DeploymentSpec{Replicas: int32p(3)},
				Status:     appsv1.DeploymentStatus{ObservedGeneration: 1},
			},
			wantSub:  "waiting for spec update to be observed",
			wantDone: false,
		},
		{
			name: "deployment in progress: replicas updating",
			obj: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       appsv1.DeploymentSpec{Replicas: int32p(3)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					UpdatedReplicas:    1,
				},
			},
			wantSub:  "1 of 3 new replicas updated",
			wantDone: false,
		},
		{
			name: "deployment complete",
			obj: &appsv1.Deployment{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       appsv1.DeploymentSpec{Replicas: int32p(3)},
				Status: appsv1.DeploymentStatus{
					ObservedGeneration: 1,
					Replicas:           3,
					UpdatedReplicas:    3,
					AvailableReplicas:  3,
				},
			},
			wantSub:  "successfully rolled out",
			wantDone: true,
		},
		{
			name: "statefulset in progress: not enough ready",
			obj: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       appsv1.StatefulSetSpec{Replicas: int32p(3)},
				Status: appsv1.StatefulSetStatus{
					ObservedGeneration: 1,
					UpdatedReplicas:    3,
					ReadyReplicas:      2,
				},
			},
			wantSub:  "2 of 3 ready",
			wantDone: false,
		},
		{
			name: "statefulset complete",
			obj: &appsv1.StatefulSet{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Spec:       appsv1.StatefulSetSpec{Replicas: int32p(3)},
				Status: appsv1.StatefulSetStatus{
					ObservedGeneration: 1,
					UpdatedReplicas:    3,
					ReadyReplicas:      3,
					UpdateRevision:     "rev-2",
					CurrentRevision:    "rev-2",
				},
			},
			wantSub:  "successfully rolled out",
			wantDone: true,
		},
		{
			name: "daemonset in progress: updating",
			obj: &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: appsv1.DaemonSetStatus{
					ObservedGeneration:     1,
					DesiredNumberScheduled: 4,
					UpdatedNumberScheduled: 2,
				},
			},
			wantSub:  "2 of 4 updated",
			wantDone: false,
		},
		{
			name: "daemonset complete",
			obj: &appsv1.DaemonSet{
				ObjectMeta: metav1.ObjectMeta{Generation: 1},
				Status: appsv1.DaemonSetStatus{
					ObservedGeneration:     1,
					DesiredNumberScheduled: 4,
					UpdatedNumberScheduled: 4,
					NumberAvailable:        4,
				},
			},
			wantSub:  "successfully rolled out",
			wantDone: true,
		},
		{
			name:     "unknown type",
			obj:      &appsv1.ReplicaSet{},
			wantSub:  "unknown workload type",
			wantDone: false,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			summary, done := Status(tc.obj)
			if !strings.Contains(summary, tc.wantSub) {
				t.Errorf("summary = %q, want substring %q", summary, tc.wantSub)
			}
			if done != tc.wantDone {
				t.Errorf("done = %v, want %v", done, tc.wantDone)
			}
		})
	}
}
