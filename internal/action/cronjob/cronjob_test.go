package cronjob

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestTriggerJob(t *testing.T) {
	const (
		ns    = "default"
		name  = "backup"
		image = "backup-tool:1.2.3"
		uid   = "cj-uid-123"
	)
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns, UID: uid},
		Spec: batchv1.CronJobSpec{
			Schedule: "0 * * * *",
			JobTemplate: batchv1.JobTemplateSpec{
				Spec: batchv1.JobSpec{
					Template: corev1.PodTemplateSpec{
						Spec: corev1.PodSpec{
							RestartPolicy: corev1.RestartPolicyNever,
							Containers: []corev1.Container{{
								Name:  "worker",
								Image: image,
							}},
						},
					},
				},
			},
		},
	}
	cs := fake.NewClientset(cj)
	ctx := context.Background()

	// FAKE LIMITATION: the object tracker does not synthesize a name from
	// ObjectMeta.GenerateName on Create, so created.Name would come back empty and
	// the tracker would store a nameless Job. We install a create reactor that
	// stamps a concrete name (prefix + suffix) exactly as the real API server
	// would, letting us exercise TriggerJob's real Get -> build -> Create flow and
	// assert the returned name (and the stored Job) instead of only that Create
	// returned nil.
	var genCount int
	cs.PrependReactor("create", "jobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		job := a.(k8stesting.CreateAction).GetObject().(*batchv1.Job)
		if job.Name == "" && job.GenerateName != "" {
			genCount++
			job.Name = fmt.Sprintf("%s%05d", job.GenerateName, genCount)
		}
		if err := cs.Tracker().Create(a.GetResource(), job, a.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, job, nil
	})

	jobName, err := TriggerJob(ctx, cs, ns, name)
	if err != nil {
		t.Fatalf("TriggerJob: unexpected error %v", err)
	}

	// The fake honors GenerateName, so the returned name should carry the prefix.
	if !strings.HasPrefix(jobName, name+"-manual-") {
		t.Errorf("returned job name %q: want prefix %q", jobName, name+"-manual-")
	}

	list, err := cs.BatchV1().Jobs(ns).List(ctx, metav1.ListOptions{})
	if err != nil {
		t.Fatalf("list jobs: unexpected error %v", err)
	}
	if len(list.Items) != 1 {
		t.Fatalf("job count = %d, want 1", len(list.Items))
	}
	job := list.Items[0]

	if !strings.HasPrefix(job.GenerateName, name+"-manual-") {
		t.Errorf("job GenerateName = %q, want prefix %q", job.GenerateName, name+"-manual-")
	}
	if got := job.Annotations[manualInstantiateAnnotation]; got != "manual" {
		t.Errorf("annotation %s = %q, want %q", manualInstantiateAnnotation, got, "manual")
	}

	// The Job must carry the CronJob's template spec verbatim, including the image.
	if len(job.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("container count = %d, want 1", len(job.Spec.Template.Spec.Containers))
	}
	if got := job.Spec.Template.Spec.Containers[0].Image; got != image {
		t.Errorf("container image = %q, want %q", got, image)
	}
	if got := job.Spec.Template.Spec.RestartPolicy; got != corev1.RestartPolicyNever {
		t.Errorf("restart policy = %q, want %q", got, corev1.RestartPolicyNever)
	}

	// Exactly one owner reference, pointing back to the CronJob (non-controller).
	if len(job.OwnerReferences) != 1 {
		t.Fatalf("owner reference count = %d, want 1", len(job.OwnerReferences))
	}
	ref := job.OwnerReferences[0]
	if ref.Kind != "CronJob" {
		t.Errorf("owner ref Kind = %q, want %q", ref.Kind, "CronJob")
	}
	if ref.APIVersion != "batch/v1" {
		t.Errorf("owner ref APIVersion = %q, want %q", ref.APIVersion, "batch/v1")
	}
	if ref.Name != name {
		t.Errorf("owner ref Name = %q, want %q", ref.Name, name)
	}
	if ref.UID != uid {
		t.Errorf("owner ref UID = %q, want %q", ref.UID, uid)
	}

	// A missing CronJob must surface an error rather than creating an empty Job.
	if _, err := TriggerJob(ctx, cs, ns, "nope"); err == nil {
		t.Errorf("TriggerJob(missing cronjob): expected error, got nil")
	}
}

func TestSetSuspend(t *testing.T) {
	const (
		ns   = "default"
		name = "backup"
	)
	falsePtr := false
	cj := &batchv1.CronJob{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       batchv1.CronJobSpec{Schedule: "0 * * * *", Suspend: &falsePtr},
	}
	cs := fake.NewClientset(cj)
	ctx := context.Background()

	// FAKE LIMITATION: the object tracker's default reactor does not apply a
	// strategic-merge patch to a typed CronJob (it treats the patch bytes as a
	// full object), so Spec.Suspend would never actually flip. We install a patch
	// reactor that decodes the merge patch and mutates the tracked CronJob, which
	// lets us exercise the real Patch call SetSuspend issues and assert the value
	// round-trips through a subsequent Get.
	cs.PrependReactor("patch", "cronjobs", func(a k8stesting.Action) (bool, runtime.Object, error) {
		pa := a.(k8stesting.PatchAction)
		obj, err := cs.Tracker().Get(a.GetResource(), a.GetNamespace(), pa.GetName())
		if err != nil {
			return true, nil, err
		}
		cur := obj.(*batchv1.CronJob)

		var patch struct {
			Spec struct {
				Suspend *bool `json:"suspend"`
			} `json:"spec"`
		}
		if err := json.Unmarshal(pa.GetPatch(), &patch); err != nil {
			return true, nil, err
		}
		if patch.Spec.Suspend != nil {
			v := *patch.Spec.Suspend
			cur.Spec.Suspend = &v
		}
		if err := cs.Tracker().Update(a.GetResource(), cur, a.GetNamespace()); err != nil {
			return true, nil, err
		}
		return true, cur, nil
	})

	assertSuspend := func(want bool) {
		t.Helper()
		got, err := cs.BatchV1().CronJobs(ns).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			t.Fatalf("get cronjob: unexpected error %v", err)
		}
		if got.Spec.Suspend == nil {
			t.Fatalf("Spec.Suspend is nil, want %v", want)
		}
		if *got.Spec.Suspend != want {
			t.Errorf("Spec.Suspend = %v, want %v", *got.Spec.Suspend, want)
		}
	}

	if err := SetSuspend(ctx, cs, ns, name, true); err != nil {
		t.Fatalf("SetSuspend(true): unexpected error %v", err)
	}
	assertSuspend(true)

	if err := SetSuspend(ctx, cs, ns, name, false); err != nil {
		t.Fatalf("SetSuspend(false): unexpected error %v", err)
	}
	assertSuspend(false)
}
