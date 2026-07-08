package overview

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/api/resource"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

func pod(name, node string, phase corev1.PodPhase, mut func(*corev1.Pod)) corev1.Pod {
	p := corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default"},
		Spec:       corev1.PodSpec{NodeName: node},
		Status:     corev1.PodStatus{Phase: phase},
	}
	if mut != nil {
		mut(&p)
	}
	return p
}

func TestPodStats(t *testing.T) {
	pods := []corev1.Pod{
		pod("a", "n1", corev1.PodRunning, nil),
		pod("b", "n1", corev1.PodRunning, nil),
		pod("c", "n2", corev1.PodPending, nil),
		pod("d", "n2", corev1.PodSucceeded, nil),
		pod("e", "n2", corev1.PodRunning, func(p *corev1.Pod) {
			p.Status.ContainerStatuses = []corev1.ContainerStatus{
				{State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "CrashLoopBackOff"}}},
			}
		}),
	}
	running, pending, failed, succeeded, phases := PodStats(pods)
	if running != 2 || pending != 1 || failed != 1 || succeeded != 1 {
		t.Fatalf("counts = run %d pend %d fail %d succ %d, want 2/1/1/1", running, pending, failed, succeeded)
	}
	// Phase percentages sum to ~100 and are labeled.
	if len(phases) != 4 || phases[0].Label != "Running" || phases[3].Label != "Failed" {
		t.Fatalf("phase rows unexpected: %+v", phases)
	}
	if phases[0].N != 2 || phases[0].Class != columns.StatusOK {
		t.Fatalf("running phase = %+v", phases[0])
	}
}

func node(name string, ready, cordoned bool) corev1.Node {
	st := corev1.ConditionFalse
	if ready {
		st = corev1.ConditionTrue
	}
	return corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: name},
		Spec:       corev1.NodeSpec{Unschedulable: cordoned},
		Status: corev1.NodeStatus{
			Conditions:  []corev1.NodeCondition{{Type: corev1.NodeReady, Status: st}},
			Allocatable: corev1.ResourceList{corev1.ResourcePods: resource.MustParse("60")},
		},
	}
}

func TestNodeStats(t *testing.T) {
	nodes := []corev1.Node{
		node("n1", true, false),
		node("n2", true, true), // cordoned
		node("n3", false, false),
	}
	podsByNode := map[string]int{"n1": 42, "n2": 8}
	// Ready counts schedulable-and-ready nodes only; the cordoned node (n2) is
	// reported separately, matching the design's "5/6 ready · 1 cordoned".
	ready, total, cordoned, rows := NodeStats(nodes, nil, nil, false, podsByNode)
	if ready != 1 || total != 3 || cordoned != 1 {
		t.Fatalf("node counts = ready %d total %d cordoned %d, want 1/3/1", ready, total, cordoned)
	}
	if rows[0].Pods != "42/60" {
		t.Fatalf("n1 pods = %q, want 42/60", rows[0].Pods)
	}
	if rows[1].Status != "Cordoned" || rows[1].StatusClass != columns.StatusWarn {
		t.Fatalf("n2 status = %q/%v", rows[1].Status, rows[1].StatusClass)
	}
	if rows[2].Status != "NotReady" || rows[2].StatusClass != columns.StatusError {
		t.Fatalf("n3 status = %q/%v", rows[2].Status, rows[2].StatusClass)
	}
}

func TestWorkloads(t *testing.T) {
	three := int32(3)
	deploys := []appsv1.Deployment{
		{Spec: appsv1.DeploymentSpec{Replicas: &three}, Status: appsv1.DeploymentStatus{ReadyReplicas: 3}},
		{Spec: appsv1.DeploymentSpec{Replicas: &three}, Status: appsv1.DeploymentStatus{ReadyReplicas: 1}},
	}
	jobs := []batchv1.Job{
		{Status: batchv1.JobStatus{Succeeded: 1}},
		{Status: batchv1.JobStatus{Succeeded: 0}},
	}
	wl := Workloads(deploys, nil, nil, jobs, nil)
	byName := map[string]Workload{}
	for _, w := range wl {
		byName[w.Name] = w
	}
	if d := byName["Deployments"]; d.Ready != 4 || d.Total != 6 {
		t.Fatalf("deployments = %d/%d, want 4/6", d.Ready, d.Total)
	}
	if j := byName["Jobs"]; j.Ready != 1 || j.Total != 2 {
		t.Fatalf("jobs = %d/%d, want 1/2", j.Ready, j.Total)
	}
}

func TestCapacityOf(t *testing.T) {
	nodes := []corev1.Node{{Status: corev1.NodeStatus{Allocatable: corev1.ResourceList{
		corev1.ResourceCPU:    resource.MustParse("4"), // 4000m
		corev1.ResourceMemory: resource.MustParse("8Gi"),
		corev1.ResourcePods:   resource.MustParse("60"),
	}}}}
	cap := CapacityOf(nodes, 2000, 4*1024*1024*1024, 30, true)
	if cap.CPUPct < 49.9 || cap.CPUPct > 50.1 {
		t.Fatalf("cpu pct = %.1f, want 50", cap.CPUPct)
	}
	if cap.PodsPct < 49.9 || cap.PodsPct > 50.1 {
		t.Fatalf("pods pct = %.1f, want 50", cap.PodsPct)
	}
	if !contains(cap.PodsText, "30 / 60") {
		t.Fatalf("pods text = %q", cap.PodsText)
	}
}

func TestTopConsumers(t *testing.T) {
	usage := map[string]int64{"default/a": 100, "default/b": 890, "kube-system/c": 312}
	top := TopConsumers(usage, 2)
	if len(top) != 2 || top[0].Name != "b" || top[0].Millis != 890 {
		t.Fatalf("top[0] = %+v, want b/890", top[0])
	}
	if top[1].Name != "c" {
		t.Fatalf("top[1] = %+v, want c", top[1])
	}
}

func TestRecentEvents(t *testing.T) {
	now := time.Now()
	events := []corev1.Event{
		{Reason: "Old", Type: "Normal", LastTimestamp: metav1.NewTime(now.Add(-10 * time.Minute)), InvolvedObject: corev1.ObjectReference{Name: "x"}},
		{Reason: "New", Type: "Warning", LastTimestamp: metav1.NewTime(now.Add(-30 * time.Second)), InvolvedObject: corev1.ObjectReference{Name: "y"}, Message: "boom"},
	}
	rows := RecentEvents(events, now, 5)
	if len(rows) != 2 || rows[0].Reason != "New" {
		t.Fatalf("events not sorted newest-first: %+v", rows)
	}
	if rows[0].Class != columns.StatusWarn || rows[0].Age != "30s" {
		t.Fatalf("new event = %+v", rows[0])
	}
}

func TestAlertsFrom(t *testing.T) {
	total, warn, crit := AlertsFrom(4, 1, 2)
	if crit != 5 || warn != 2 || total != 7 {
		t.Fatalf("alerts = total %d warn %d crit %d, want 7/2/5", total, warn, crit)
	}
}

func contains(s, sub string) bool {
	return len(s) >= len(sub) && (s == sub || indexOf(s, sub) >= 0)
}

func indexOf(s, sub string) int {
	for i := 0; i+len(sub) <= len(s); i++ {
		if s[i:i+len(sub)] == sub {
			return i
		}
	}
	return -1
}
