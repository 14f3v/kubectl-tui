// Package cronjob implements the CronJob-specific actions: manually triggering
// a run and toggling suspension. TriggerJob mirrors `kubectl create job --from`:
// it materializes a one-off Job from the CronJob's jobTemplate, tagging it with
// the manual-instantiate annotation and an owner reference back to the CronJob so
// the run shows up alongside the controller's scheduled runs. SetSuspend flips
// Spec.Suspend via a strategic-merge patch — the same mechanism as editing the
// field with `kubectl patch` — so we toggle scheduling without reading and
// rewriting the whole object.
package cronjob

import (
	"context"
	"fmt"

	batchv1 "k8s.io/api/batch/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// manualInstantiateAnnotation marks a Job as a manual run of its CronJob, the
// same annotation `kubectl create job --from` stamps. The CronJob controller
// uses it to distinguish operator-triggered runs from scheduled ones.
const manualInstantiateAnnotation = "cronjob.kubernetes.io/instantiate"

// TriggerJob creates a one-off Job from a CronJob's jobTemplate, mirroring
// `kubectl create job --from=cronjob/<name>`. It reads the CronJob so the new
// Job carries the exact template spec the controller would use, stamps the
// manual-instantiate annotation, and sets an owner reference back to the CronJob
// (non-controller: a manual run is not managed by the CronJob's controller loop,
// so Controller stays false and BlockOwnerDeletion is left unset). The Job uses a
// GenerateName so repeated triggers never collide; the server-assigned name is
// returned for the UI to surface.
func TriggerJob(ctx context.Context, cs kubernetes.Interface, namespace, name string) (jobName string, err error) {
	cj, err := cs.BatchV1().CronJobs(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return "", err
	}

	job := &batchv1.Job{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: name + "-manual-",
			Namespace:    namespace,
			Annotations: map[string]string{
				manualInstantiateAnnotation: "manual",
			},
			OwnerReferences: []metav1.OwnerReference{{
				APIVersion: "batch/v1",
				Kind:       "CronJob",
				Name:       cj.Name,
				UID:        cj.UID,
			}},
		},
		Spec: cj.Spec.JobTemplate.Spec,
	}

	created, err := cs.BatchV1().Jobs(namespace).Create(ctx, job, metav1.CreateOptions{})
	if err != nil {
		return "", err
	}
	return created.Name, nil
}

// SetSuspend toggles a CronJob's Spec.Suspend flag via a strategic-merge patch.
// Suspending stops the controller from creating new scheduled runs (already
// running Jobs are unaffected); resuming re-enables scheduling. The patch is
// idempotent, so setting the flag to its current value is a harmless no-op.
func SetSuspend(ctx context.Context, cs kubernetes.Interface, namespace, name string, suspend bool) error {
	patch := []byte(fmt.Sprintf(`{"spec":{"suspend":%t}}`, suspend))
	_, err := cs.BatchV1().CronJobs(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	return err
}
