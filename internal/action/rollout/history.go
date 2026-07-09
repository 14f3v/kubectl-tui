package rollout

// This file adds revision history + undo on top of the restart/status actions
// in rollout.go. It mirrors `kubectl rollout history` and `kubectl rollout undo`
// so the UI can show past revisions of a workload and roll one back.
//
// For Deployments the source of truth is the set of ReplicaSets the controller
// owns: each carries a "deployment.kubernetes.io/revision" annotation. For
// StatefulSets and DaemonSets the controller instead snapshots each revision
// into a ControllerRevision object whose Revision field is the number. In both
// cases we filter by OwnerReference UID so we never pick up an unrelated object
// that happens to share a namespace.

import (
	"context"
	"encoding/json"
	"fmt"
	"sort"
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// revisionAnnotation is the key the Deployment controller stamps on each owned
// ReplicaSet to record its revision number. changeCauseAnnotation is the
// optional human-readable reason kubectl records with --record / an explicit
// annotation; we surface it verbatim when present.
const (
	revisionAnnotation    = "deployment.kubernetes.io/revision"
	changeCauseAnnotation = "kubernetes.io/change-cause"
)

// Revision is one point in a workload's rollout history. Number is the revision
// number (monotonic per workload), Name is the backing object (ReplicaSet or
// ControllerRevision), Cause is the change-cause annotation if the operator
// recorded one, and Current marks the revision the workload is running now.
type Revision struct {
	Number    int64
	Name      string
	CreatedAt time.Time
	Cause     string
	Current   bool
}

// History returns the revision list for a workload, newest first, mirroring
// `kubectl rollout history`. The kind selects where revisions live: Deployments
// keep them as owned ReplicaSets, StatefulSets/DaemonSets as owned
// ControllerRevisions. An unknown kind is rejected so a UI bug can never
// silently return an empty history.
func History(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) ([]Revision, error) {
	switch kind {
	case "deployments":
		return deploymentHistory(ctx, cs, namespace, name)
	case "statefulsets", "daemonsets":
		return controllerRevisionHistory(ctx, cs, kind, namespace, name)
	default:
		return nil, fmt.Errorf("history not supported for %q", kind)
	}
}

// deploymentHistory lists the ReplicaSets the Deployment owns and turns each
// into a Revision. Ownership is decided by UID so a ReplicaSet from a different
// Deployment (or a bare one) is skipped. The revision annotation is parsed to an
// int64; a ReplicaSet without a parseable revision is ignored, matching how
// kubectl treats an un-revisioned RS as not part of the history.
func deploymentHistory(ctx context.Context, cs kubernetes.Interface, namespace, name string) ([]Revision, error) {
	dep, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return nil, err
	}
	rsList, err := cs.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var revisions []Revision
	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !ownedBy(rs.OwnerReferences, dep.UID) {
			continue
		}
		num, ok := parseRevision(rs.Annotations[revisionAnnotation])
		if !ok {
			continue
		}
		revisions = append(revisions, Revision{
			Number:    num,
			Name:      rs.Name,
			CreatedAt: rs.CreationTimestamp.Time,
			Cause:     rs.Annotations[changeCauseAnnotation],
		})
	}

	sortAndMarkCurrent(revisions)
	return revisions, nil
}

// controllerRevisionHistory lists the ControllerRevisions owned by a
// StatefulSet or DaemonSet. The workload is fetched first only to learn its UID
// for the ownership filter; the revision number comes straight from each
// ControllerRevision's Revision field.
func controllerRevisionHistory(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) ([]Revision, error) {
	uid, err := workloadUID(ctx, cs, kind, namespace, name)
	if err != nil {
		return nil, err
	}
	crList, err := cs.AppsV1().ControllerRevisions(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return nil, err
	}

	var revisions []Revision
	for i := range crList.Items {
		cr := &crList.Items[i]
		if !ownedBy(cr.OwnerReferences, uid) {
			continue
		}
		revisions = append(revisions, Revision{
			Number:    cr.Revision,
			Name:      cr.Name,
			CreatedAt: cr.CreationTimestamp.Time,
			Cause:     cr.Annotations[changeCauseAnnotation],
		})
	}

	sortAndMarkCurrent(revisions)
	return revisions, nil
}

// Undo rolls a workload back to a prior revision, mirroring
// `kubectl rollout undo --to-revision`. For Deployments it copies the pod
// template from the matching ReplicaSet; for StatefulSets/DaemonSets it copies
// it from the matching ControllerRevision. Both use a strategic-merge patch of
// only spec.template so unrelated spec fields the UI never loaded are left
// untouched. Unknown kinds are rejected.
func Undo(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string, toRevision int64) error {
	switch kind {
	case "deployments":
		return undoDeployment(ctx, cs, namespace, name, toRevision)
	case "statefulsets", "daemonsets":
		return undoControllerRevision(ctx, cs, kind, namespace, name, toRevision)
	default:
		return fmt.Errorf("undo not supported for %q", kind)
	}
}

// undoDeployment finds the owned ReplicaSet at the requested revision and
// patches the Deployment's pod template from it. We build the patch from the
// RS's Spec.Template (the exact pod spec that revision ran) so the rollback is
// faithful. A missing revision is a clear error rather than a silent no-op.
func undoDeployment(ctx context.Context, cs kubernetes.Interface, namespace, name string, toRevision int64) error {
	dep, err := cs.AppsV1().Deployments(namespace).Get(ctx, name, metav1.GetOptions{})
	if err != nil {
		return err
	}
	rsList, err := cs.AppsV1().ReplicaSets(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	for i := range rsList.Items {
		rs := &rsList.Items[i]
		if !ownedBy(rs.OwnerReferences, dep.UID) {
			continue
		}
		num, ok := parseRevision(rs.Annotations[revisionAnnotation])
		if !ok || num != toRevision {
			continue
		}
		patch, err := templatePatch(rs.Spec.Template)
		if err != nil {
			return err
		}
		_, err = cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		return err
	}

	return fmt.Errorf("no revision %d found for deployment %q", toRevision, name)
}

// undoControllerRevision is the StatefulSet/DaemonSet path. It finds the owned
// ControllerRevision at the requested revision, pulls the pod template out of
// its serialized Data, and strategic-merge patches only spec.template back onto
// the workload. The template is extracted rather than replaying the whole
// snapshot so we touch nothing else. A missing revision or a snapshot without a
// resolvable template is a clear error, never a panic.
func undoControllerRevision(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string, toRevision int64) error {
	uid, err := workloadUID(ctx, cs, kind, namespace, name)
	if err != nil {
		return err
	}
	crList, err := cs.AppsV1().ControllerRevisions(namespace).List(ctx, metav1.ListOptions{})
	if err != nil {
		return err
	}

	for i := range crList.Items {
		cr := &crList.Items[i]
		if !ownedBy(cr.OwnerReferences, uid) || cr.Revision != toRevision {
			continue
		}
		patch, err := revisionTemplatePatch(cr.Data.Raw)
		if err != nil {
			return fmt.Errorf("revision %d of %s %q has no usable template: %w", toRevision, kind, name, err)
		}
		switch kind {
		case "statefulsets":
			_, err = cs.AppsV1().StatefulSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		case "daemonsets":
			_, err = cs.AppsV1().DaemonSets(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
		}
		return err
	}

	return fmt.Errorf("no revision %d found for %s %q", toRevision, kind, name)
}

// workloadUID fetches a StatefulSet or DaemonSet just to read its UID, which the
// ownership filter needs. Keeping it in one place avoids duplicating the
// per-kind Get in both history and undo.
func workloadUID(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string) (types.UID, error) {
	switch kind {
	case "statefulsets":
		s, err := cs.AppsV1().StatefulSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return s.UID, nil
	case "daemonsets":
		d, err := cs.AppsV1().DaemonSets(namespace).Get(ctx, name, metav1.GetOptions{})
		if err != nil {
			return "", err
		}
		return d.UID, nil
	default:
		return "", fmt.Errorf("unsupported kind %q", kind)
	}
}

// ownedBy reports whether any of the owner references points at the given UID.
// Matching on UID (not name) is what lets us safely ignore look-alike objects
// from a re-created workload of the same name.
func ownedBy(owners []metav1.OwnerReference, uid types.UID) bool {
	for _, o := range owners {
		if o.UID == uid {
			return true
		}
	}
	return false
}

// parseRevision converts a revision annotation to an int64. It returns ok=false
// (rather than an error) for missing or malformed values so callers can simply
// skip objects that aren't part of the numbered history.
func parseRevision(s string) (int64, bool) {
	if s == "" {
		return 0, false
	}
	n, err := strconv.ParseInt(s, 10, 64)
	if err != nil {
		return 0, false
	}
	return n, true
}

// sortAndMarkCurrent orders revisions newest-first by number and flags the
// highest one as Current — the revision the controller is actively running.
func sortAndMarkCurrent(revisions []Revision) {
	sort.Slice(revisions, func(i, j int) bool {
		return revisions[i].Number > revisions[j].Number
	})
	if len(revisions) > 0 {
		revisions[0].Current = true
	}
}

// templatePatch builds the strategic-merge patch body that replaces only
// spec.template with the given pod template. Marshaling the typed template keeps
// the patch faithful to the exact revision without hand-writing JSON.
func templatePatch(tmpl corev1.PodTemplateSpec) ([]byte, error) {
	body := map[string]any{
		"spec": map[string]any{
			"template": tmpl,
		},
	}
	return json.Marshal(body)
}

// revisionTemplatePatch pulls the pod template out of a ControllerRevision's
// serialized Data and wraps it as a spec.template strategic-merge patch. The
// snapshot is a partial workload object shaped like {"spec":{"template":{...}}};
// we re-emit just that template so the patch mutates nothing else. A snapshot
// missing the template yields an error so the caller can report it cleanly.
func revisionTemplatePatch(raw []byte) ([]byte, error) {
	if len(raw) == 0 {
		return nil, fmt.Errorf("empty revision data")
	}
	var snapshot struct {
		Spec struct {
			Template *corev1.PodTemplateSpec `json:"template"`
		} `json:"spec"`
	}
	if err := json.Unmarshal(raw, &snapshot); err != nil {
		return nil, fmt.Errorf("decode revision data: %w", err)
	}
	if snapshot.Spec.Template == nil {
		return nil, fmt.Errorf("revision data has no spec.template")
	}
	return templatePatch(*snapshot.Spec.Template)
}
