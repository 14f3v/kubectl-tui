// Package nodeops implements the node maintenance actions: cordon, uncordon,
// and drain. Cordon/uncordon flip Spec.Unschedulable via a strategic-merge
// patch — the same mechanism as `kubectl cordon` — so we mark the node without
// reading and rewriting the whole object. Drain builds on cordon: it fences the
// node, then evicts the pods that can safely move, mirroring the skip rules of
// `kubectl drain` (leave DaemonSet-managed and static/mirror pods in place, and
// don't bother with pods that have already terminated). Eviction goes through
// the eviction subresource so PodDisruptionBudgets are honored, exactly like
// kubectl. Node deletion and the --force/--grace-period knobs are out of scope.
package nodeops

import (
	"context"
	"fmt"

	corev1 "k8s.io/api/core/v1"
	policyv1 "k8s.io/api/policy/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/fields"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// mirrorPodAnnotation marks a static pod that the kubelet mirrors into the API
// server. Such a pod is owned by the node's kubelet, not a controller, so
// evicting it does nothing useful (the kubelet immediately recreates it) — we
// skip it during drain, matching kubectl.
const mirrorPodAnnotation = "kubernetes.io/config.mirror"

// cordonPatch builds the strategic-merge patch that toggles a node's
// schedulability. It is factored out so both Cordon and Uncordon share one
// definition and a test can assert the exact bytes without hitting the API.
func cordonPatch(unschedulable bool) []byte {
	return []byte(fmt.Sprintf(`{"spec":{"unschedulable":%t}}`, unschedulable))
}

// Cordon marks a node unschedulable so the scheduler places no new pods on it.
// Existing pods are left running; use Drain to also move them off. The patch is
// idempotent, so cordoning an already-cordoned node is a harmless no-op.
func Cordon(ctx context.Context, cs kubernetes.Interface, node string) error {
	_, err := cs.CoreV1().Nodes().Patch(ctx, node, types.StrategicMergePatchType, cordonPatch(true), metav1.PatchOptions{})
	return err
}

// Uncordon clears the unschedulable flag, returning a node to the scheduler's
// rotation. Like Cordon it is idempotent.
func Uncordon(ctx context.Context, cs kubernetes.Interface, node string) error {
	_, err := cs.CoreV1().Nodes().Patch(ctx, node, types.StrategicMergePatchType, cordonPatch(false), metav1.PatchOptions{})
	return err
}

// drainablePods partitions the pods on a node into those safe to evict and those
// to leave alone. It is the pure heart of Drain: given a snapshot of pods it
// makes the same keep/evict decision as `kubectl drain` with no I/O, so the
// policy is fully unit-testable. A pod is skipped when it is DaemonSet-managed
// (the DaemonSet controller would just recreate it on the same fenced node), a
// static/mirror pod (owned by the kubelet, likewise recreated), or already
// terminated (Succeeded/Failed — nothing to move). Everything else is evictable.
func drainablePods(pods []corev1.Pod) (evict, skip []corev1.Pod) {
	for _, p := range pods {
		if isDaemonSetPod(&p) || isMirrorPod(&p) || isTerminated(&p) {
			skip = append(skip, p)
			continue
		}
		evict = append(evict, p)
	}
	return evict, skip
}

// isDaemonSetPod reports whether the pod is managed by a DaemonSet, detected via
// its owner references. DaemonSet pods are intentionally pinned to their node,
// so draining leaves them in place.
func isDaemonSetPod(p *corev1.Pod) bool {
	for _, ref := range p.OwnerReferences {
		if ref.Kind == "DaemonSet" {
			return true
		}
	}
	return false
}

// isMirrorPod reports whether the pod is a kubelet mirror of a static pod,
// identified by the config.mirror annotation.
func isMirrorPod(p *corev1.Pod) bool {
	_, ok := p.Annotations[mirrorPodAnnotation]
	return ok
}

// isTerminated reports whether the pod has already reached a terminal phase, in
// which case there is nothing to evict.
func isTerminated(p *corev1.Pod) bool {
	return p.Status.Phase == corev1.PodSucceeded || p.Status.Phase == corev1.PodFailed
}

// DrainResult reports the outcome of a Drain call. Evicted and Skipped hold the
// "namespace/name" of each pod that was evicted or intentionally left in place,
// and Errors maps "namespace/name" to the eviction error string for any pod
// whose eviction request failed (for example, a PodDisruptionBudget block). A
// non-empty Errors does not mean Drain itself failed — it means individual pods
// need attention — so the UI can present partial progress.
type DrainResult struct {
	Evicted []string
	Skipped []string
	Errors  map[string]string
}

// Drain fences a node and evicts the pods that can safely move off it. It first
// cordons the node (returning early if that fails, so we never start evicting
// from a node the scheduler could still fill back up), then lists the node's
// pods and evicts each drainable one through the eviction subresource so
// PodDisruptionBudgets are respected. Per-pod eviction errors are collected in
// the result rather than aborting the whole drain, matching how an operator
// expects `kubectl drain` to report progress. The returned error is reserved for
// failures that prevent draining at all (cordon or the pod list).
func Drain(ctx context.Context, cs kubernetes.Interface, node string) (DrainResult, error) {
	res := DrainResult{Errors: map[string]string{}}

	if err := Cordon(ctx, cs, node); err != nil {
		return res, fmt.Errorf("cordon node %q: %w", node, err)
	}

	// Scope the list to this node's pods server-side; listing every pod in the
	// cluster and filtering client-side would be wasteful on large clusters.
	sel := fields.OneTermEqualSelector("spec.nodeName", node).String()
	list, err := cs.CoreV1().Pods("").List(ctx, metav1.ListOptions{FieldSelector: sel})
	if err != nil {
		return res, fmt.Errorf("list pods on node %q: %w", node, err)
	}

	evict, skip := drainablePods(list.Items)
	for i := range skip {
		res.Skipped = append(res.Skipped, key(&skip[i]))
	}
	for i := range evict {
		p := &evict[i]
		id := key(p)
		err := cs.PolicyV1().Evictions(p.Namespace).Evict(ctx, &policyv1.Eviction{
			ObjectMeta: metav1.ObjectMeta{Name: p.Name, Namespace: p.Namespace},
		})
		if err != nil {
			res.Errors[id] = err.Error()
			continue
		}
		res.Evicted = append(res.Evicted, id)
	}
	return res, nil
}

// key formats a pod's identity as "namespace/name" for the result maps/slices.
func key(p *corev1.Pod) string {
	return p.Namespace + "/" + p.Name
}
