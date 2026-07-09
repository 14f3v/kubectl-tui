package columns

import (
	"testing"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
)

func TestStatefulSetsProject(t *testing.T) {
	proj := For("statefulsets")
	desired := int32(3)
	sts := &appsv1.StatefulSet{
		ObjectMeta: meta("db"),
		Spec:       appsv1.StatefulSetSpec{Replicas: &desired},
		Status:     appsv1.StatefulSetStatus{ReadyReplicas: 1},
	}
	row, ok := proj.Project(sts, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "READY"); got.Text != "1/3" {
		t.Fatalf("READY = %q, want 1/3", got.Text)
	}
	if row.Health != StatusWarn {
		t.Fatalf("health = %v, want warn", row.Health)
	}
}

func TestDaemonSetsProject(t *testing.T) {
	proj := For("daemonsets")
	ds := &appsv1.DaemonSet{
		ObjectMeta: meta("agent"),
		Status: appsv1.DaemonSetStatus{
			DesiredNumberScheduled: 4,
			CurrentNumberScheduled: 4,
			NumberReady:            4,
			UpdatedNumberScheduled: 4,
			NumberAvailable:        4,
		},
	}
	row, ok := proj.Project(ds, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "DESIRED"); got.Text != "4" {
		t.Fatalf("DESIRED = %q, want 4", got.Text)
	}
	if got := find(t, proj, row, "READY"); got.Text != "4" {
		t.Fatalf("READY = %q, want 4", got.Text)
	}
	if row.Health != StatusOK {
		t.Fatalf("health = %v, want ok", row.Health)
	}
}

func TestReplicaSetsProject(t *testing.T) {
	proj := For("replicasets")
	desired := int32(2)
	rs := &appsv1.ReplicaSet{
		ObjectMeta: meta("rs"),
		Spec:       appsv1.ReplicaSetSpec{Replicas: &desired},
		Status:     appsv1.ReplicaSetStatus{Replicas: 2, ReadyReplicas: 0},
	}
	row, ok := proj.Project(rs, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "DESIRED"); got.Text != "2" {
		t.Fatalf("DESIRED = %q, want 2", got.Text)
	}
	if got := find(t, proj, row, "READY"); got.Text != "0" {
		t.Fatalf("READY = %q, want 0", got.Text)
	}
	if row.Health != StatusError {
		t.Fatalf("health = %v, want error", row.Health)
	}
}

func TestJobsProject(t *testing.T) {
	proj := For("jobs")
	want := int32(1)
	job := &batchv1.Job{
		ObjectMeta: meta("backup"),
		Spec:       batchv1.JobSpec{Completions: &want},
		Status: batchv1.JobStatus{
			Succeeded: 1,
			Conditions: []batchv1.JobCondition{
				{Type: batchv1.JobComplete, Status: corev1.ConditionTrue},
			},
		},
	}
	row, ok := proj.Project(job, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "COMPLETIONS"); got.Text != "1/1" {
		t.Fatalf("COMPLETIONS = %q, want 1/1", got.Text)
	}
	if row.Health != StatusOK {
		t.Fatalf("health = %v, want ok", row.Health)
	}
}

func TestCronJobsProject(t *testing.T) {
	proj := For("cronjobs")
	suspend := true
	cj := &batchv1.CronJob{
		ObjectMeta: meta("nightly"),
		Spec:       batchv1.CronJobSpec{Schedule: "0 0 * * *", Suspend: &suspend},
		Status: batchv1.CronJobStatus{
			Active: []corev1.ObjectReference{{Name: "run-1"}},
		},
	}
	row, ok := proj.Project(cj, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "SCHEDULE"); got.Text != "0 0 * * *" {
		t.Fatalf("SCHEDULE = %q, want 0 0 * * *", got.Text)
	}
	if got := find(t, proj, row, "SUSPEND"); got.Text != "True" {
		t.Fatalf("SUSPEND = %q, want True", got.Text)
	}
	if got := find(t, proj, row, "ACTIVE"); got.Text != "1" {
		t.Fatalf("ACTIVE = %q, want 1", got.Text)
	}
	if row.Health != StatusMuted {
		t.Fatalf("health = %v, want muted", row.Health)
	}
}
