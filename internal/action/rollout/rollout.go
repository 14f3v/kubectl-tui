// Package rollout implements the restart + status actions for rollable
// workloads (Deployments, StatefulSets, DaemonSets). Restart stamps the pod
// template's restartedAt annotation via a strategic-merge patch — the same
// mechanism as `kubectl rollout restart` — so the controller rolls pods without
// any spec change. Status mirrors `kubectl rollout status` so the UI can poll a
// freshly-fetched object and tell the user when the roll is done. Undo/history
// is intentionally out of scope here.
package rollout

import (
	"context"
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Restartable reports whether a kind supports a rolling restart. The UI uses it
// to gate the restart action so it never offers restart on an unrollable kind.
func Restartable(kind string) bool {
	switch kind {
	case "deployments", "statefulsets", "daemonsets":
		return true
	default:
		return false
	}
}

// RestartPatch builds the strategic-merge patch that stamps the pod template's
// restart annotation. Passing the timestamp in lets callers (and tests) control
// the value; it is normalized to UTC RFC3339 to match kubectl's behavior.
func RestartPatch(now time.Time) []byte {
	ts := now.UTC().Format(time.RFC3339)
	return []byte(fmt.Sprintf(`{"spec":{"template":{"metadata":{"annotations":{"kubectl.kubernetes.io/restartedAt":%q}}}}}`, ts))
}

// Restart triggers a rolling restart of the named workload by patching its pod
// template with a fresh restartedAt annotation. The typed client is used per
// kind because the annotation lives on the pod template, which the dynamic path
// does not model as conveniently. Unknown kinds are rejected so a UI bug can
// never silently no-op.
func Restart(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string, now time.Time) error {
	patch := RestartPatch(now)
	switch kind {
	case "deployments":
		_, err := cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		return err
	case "statefulsets":
		_, err := cs.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		return err
	case "daemonsets":
		_, err := cs.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		return err
	default:
		return fmt.Errorf("restart not supported for %q", kind)
	}
}

// Status summarizes a workload's rollout progress, mirroring the phase logic of
// `kubectl rollout status`. done is true only once the roll has fully settled,
// so a poller can stop. Unknown types yield a non-terminal "unknown" summary
// rather than panicking, keeping the UI robust to unexpected inputs.
func Status(obj any) (summary string, done bool) {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return deploymentStatus(o)
	case *appsv1.StatefulSet:
		return statefulSetStatus(o)
	case *appsv1.DaemonSet:
		return daemonSetStatus(o)
	default:
		return "unknown workload type", false
	}
}

// deploymentStatus follows the Deployment progress phases: spec observed,
// replicas updated, old replicas terminated, updated replicas available.
func deploymentStatus(d *appsv1.Deployment) (string, bool) {
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	if d.Generation > d.Status.ObservedGeneration {
		return "waiting for spec update to be observed", false
	}
	if d.Status.UpdatedReplicas < desired {
		return fmt.Sprintf("waiting: %d of %d new replicas updated", d.Status.UpdatedReplicas, desired), false
	}
	if d.Status.Replicas > d.Status.UpdatedReplicas {
		return "waiting for old replicas to terminate", false
	}
	if d.Status.AvailableReplicas < d.Status.UpdatedReplicas {
		return "waiting for updated replicas to become available", false
	}
	return "successfully rolled out", true
}

// statefulSetStatus follows the StatefulSet progress phases: spec observed,
// replicas updated, replicas ready, and revision converged.
func statefulSetStatus(s *appsv1.StatefulSet) (string, bool) {
	desired := int32(1)
	if s.Spec.Replicas != nil {
		desired = *s.Spec.Replicas
	}
	if s.Status.ObservedGeneration < s.Generation {
		return "waiting for spec update to be observed", false
	}
	if s.Status.UpdatedReplicas < desired {
		return fmt.Sprintf("waiting: %d of %d updated", s.Status.UpdatedReplicas, desired), false
	}
	if s.Status.ReadyReplicas < desired {
		return fmt.Sprintf("waiting: %d of %d ready", s.Status.ReadyReplicas, desired), false
	}
	if s.Status.UpdateRevision != s.Status.CurrentRevision {
		return "waiting for rollout to finish", false
	}
	return "successfully rolled out", true
}

// daemonSetStatus follows the DaemonSet progress phases: spec observed, pods
// updated on all scheduled nodes, and updated pods available.
func daemonSetStatus(d *appsv1.DaemonSet) (string, bool) {
	if d.Status.ObservedGeneration < d.Generation {
		return "waiting for spec update to be observed", false
	}
	if d.Status.UpdatedNumberScheduled < d.Status.DesiredNumberScheduled {
		return fmt.Sprintf("waiting: %d of %d updated", d.Status.UpdatedNumberScheduled, d.Status.DesiredNumberScheduled), false
	}
	if d.Status.NumberAvailable < d.Status.DesiredNumberScheduled {
		return fmt.Sprintf("waiting: %d of %d available", d.Status.NumberAvailable, d.Status.DesiredNumberScheduled), false
	}
	return "successfully rolled out", true
}
