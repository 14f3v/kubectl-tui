package columns

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
)

func makePod(name string, mut func(*corev1.Pod)) *corev1.Pod {
	p := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name:              name,
			Namespace:         "default",
			UID:               types.UID("uid-" + name),
			ResourceVersion:   "1",
			CreationTimestamp: metav1.NewTime(time.Now().Add(-3 * time.Hour)),
		},
		Spec: corev1.PodSpec{
			NodeName:   "node-a",
			Containers: []corev1.Container{{Name: "app"}, {Name: "sidecar"}},
		},
		Status: corev1.PodStatus{Phase: corev1.PodRunning, PodIP: "10.0.0.5"},
	}
	if mut != nil {
		mut(p)
	}
	return p
}

func cellByTitle(t *testing.T, proj Projector, row Row, title string) Cell {
	t.Helper()
	for i, c := range proj.Columns() {
		if c.Title == title {
			return row.Cells[i]
		}
	}
	t.Fatalf("column %q not found", title)
	return Cell{}
}

func TestPodsProject_RunningReady(t *testing.T) {
	proj := For("pods")
	if proj == nil {
		t.Fatal("pods projector not registered")
	}
	pod := makePod("web", func(p *corev1.Pod) {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{
			{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
			{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		}
	})
	row, ok := proj.Project(pod, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.UID != "uid-web" || row.Name != "web" {
		t.Fatalf("identity mismatch: %+v", row)
	}
	if got := cellByTitle(t, proj, row, "READY"); got.Text != "2/2" || got.Status != StatusOK {
		t.Fatalf("READY = %q/%v, want 2/2/OK", got.Text, got.Status)
	}
	if got := cellByTitle(t, proj, row, "STATUS"); got.Text != "Running" || got.Status != StatusOK || got.Role != RoleStatus {
		t.Fatalf("STATUS = %q/%v/%v, want Running/OK/RoleStatus", got.Text, got.Status, got.Role)
	}
}

func TestPodsProject_CrashLoop(t *testing.T) {
	proj := For("pods")
	pod := makePod("worker", func(p *corev1.Pod) {
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Ready:        false,
			RestartCount: 7,
			State: corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{
				Reason: "CrashLoopBackOff",
			}},
		}}
		p.Spec.Containers = []corev1.Container{{Name: "app"}}
	})
	row, _ := proj.Project(pod, time.Now())
	if got := cellByTitle(t, proj, row, "STATUS"); got.Text != "CrashLoopBackOff" || got.Status != StatusError {
		t.Fatalf("STATUS = %q/%v, want CrashLoopBackOff/Error", got.Text, got.Status)
	}
	if got := cellByTitle(t, proj, row, "READY"); got.Text != "0/1" || got.Status != StatusError {
		t.Fatalf("READY = %q/%v, want 0/1/Error", got.Text, got.Status)
	}
	if got := cellByTitle(t, proj, row, "RESTARTS"); got.Text != "7" || got.Status != StatusError {
		t.Fatalf("RESTARTS = %q/%v, want 7/Error", got.Text, got.Status)
	}
}

func TestPodsProject_Terminating(t *testing.T) {
	proj := For("pods")
	pod := makePod("dying", func(p *corev1.Pod) {
		now := metav1.Now()
		p.DeletionTimestamp = &now
		p.Status.ContainerStatuses = []corev1.ContainerStatus{
			{Ready: true, State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}},
		}
	})
	row, _ := proj.Project(pod, time.Now())
	if got := cellByTitle(t, proj, row, "STATUS"); got.Text != "Terminating" || got.Status != StatusMuted {
		t.Fatalf("STATUS = %q/%v, want Terminating/Muted", got.Text, got.Status)
	}
}

func TestPodsProject_RestartClasses(t *testing.T) {
	proj := For("pods")
	cases := []struct {
		restarts int
		want     StatusClass
	}{
		{0, StatusMuted},
		{3, StatusWarn},
		{9, StatusError},
	}
	for _, tc := range cases {
		pod := makePod("p", func(p *corev1.Pod) {
			p.Spec.Containers = []corev1.Container{{Name: "app"}}
			p.Status.ContainerStatuses = []corev1.ContainerStatus{{
				Ready: true, RestartCount: int32(tc.restarts),
				State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
			}}
		})
		row, _ := proj.Project(pod, time.Now())
		if got := cellByTitle(t, proj, row, "RESTARTS"); got.Status != tc.want {
			t.Fatalf("restarts=%d class=%v, want %v", tc.restarts, got.Status, tc.want)
		}
	}
}

func TestPodsProject_SortKeysTyped(t *testing.T) {
	proj := For("pods")
	pod := makePod("z-name", func(p *corev1.Pod) {
		p.Spec.Containers = []corev1.Container{{Name: "app"}}
		p.Status.ContainerStatuses = []corev1.ContainerStatus{{
			Ready: true, RestartCount: 12,
			State: corev1.ContainerState{Running: &corev1.ContainerStateRunning{}},
		}}
	})
	row, _ := proj.Project(pod, time.Now())
	// The RESTARTS sort key must be numeric so 12 sorts after 2, not before.
	cols := proj.Columns()
	var restartsIdx int
	for i, c := range cols {
		if c.Title == "RESTARTS" {
			restartsIdx = i
		}
	}
	sk := row.SortKeys[restartsIdx]
	if !sk.IsNum || sk.Num != 12 {
		t.Fatalf("RESTARTS sort key = %+v, want numeric 12", sk)
	}
}

func TestHumanAge(t *testing.T) {
	cases := []struct {
		d    time.Duration
		want string
	}{
		{30 * time.Second, "30s"},
		{5 * time.Minute, "5m"},
		{90 * time.Minute, "1h30m"},
		{26 * time.Hour, "1d2h"},
		{11 * 24 * time.Hour, "11d"},
	}
	for _, tc := range cases {
		if got := humanAge(tc.d); got != tc.want {
			t.Errorf("humanAge(%s) = %q, want %q", tc.d, got, tc.want)
		}
	}
}
