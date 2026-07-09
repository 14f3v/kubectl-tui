package rollout

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes/fake"
)

// ownedRS builds a ReplicaSet owned by dep with the given revision annotation and
// a container image so its pod template is distinguishable from others.
func ownedRS(name string, dep *appsv1.Deployment, revision, cause, image string) *appsv1.ReplicaSet {
	rs := &appsv1.ReplicaSet{
		ObjectMeta: metav1.ObjectMeta{
			Name:      name,
			Namespace: dep.Namespace,
			Annotations: map[string]string{
				revisionAnnotation: revision,
			},
			OwnerReferences: []metav1.OwnerReference{
				{APIVersion: "apps/v1", Kind: "Deployment", Name: dep.Name, UID: dep.UID},
			},
		},
		Spec: appsv1.ReplicaSetSpec{
			Replicas: int32p(1),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: image}},
				},
			},
		},
	}
	if cause != "" {
		rs.Annotations[changeCauseAnnotation] = cause
	}
	return rs
}

// TestHistoryDeployment verifies that History returns only the owned ReplicaSets,
// newest revision first, with Current on the highest revision.
func TestHistoryDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", UID: types.UID("dep-uid")},
	}
	rs1 := ownedRS("web-1", dep, "1", "initial", "web:v1")
	rs2 := ownedRS("web-2", dep, "2", "image bump", "web:v2")

	// An unrelated ReplicaSet in the same namespace, owned by a different UID.
	unrelated := ownedRS("other-1", dep, "5", "", "other:v1")
	unrelated.Name = "other-1"
	unrelated.OwnerReferences[0].UID = types.UID("someone-else")

	cs := fake.NewClientset(dep, rs1, rs2, unrelated)

	revs, err := History(context.Background(), cs, "deployments", "default", "web")
	if err != nil {
		t.Fatalf("History returned error: %v", err)
	}
	if len(revs) != 2 {
		t.Fatalf("expected 2 revisions, got %d: %+v", len(revs), revs)
	}
	if revs[0].Number != 2 {
		t.Errorf("expected newest revision 2 first, got %d", revs[0].Number)
	}
	if revs[1].Number != 1 {
		t.Errorf("expected revision 1 second, got %d", revs[1].Number)
	}
	if !revs[0].Current {
		t.Errorf("expected revision 2 to be Current")
	}
	if revs[1].Current {
		t.Errorf("expected revision 1 to not be Current")
	}
	if revs[0].Name != "web-2" {
		t.Errorf("expected Name web-2, got %q", revs[0].Name)
	}
	if revs[0].Cause != "image bump" {
		t.Errorf("expected Cause 'image bump', got %q", revs[0].Cause)
	}
}

// TestHistoryUnknownKind ensures an unsupported kind is rejected rather than
// silently returning an empty slice.
func TestHistoryUnknownKind(t *testing.T) {
	cs := fake.NewClientset()
	_, err := History(context.Background(), cs, "pods", "default", "web")
	if err == nil {
		t.Fatalf("expected error for unknown kind, got nil")
	}
}

// TestUndoDeployment rolls a Deployment back to revision 1 and asserts its pod
// template moved toward the revision-1 ReplicaSet's template.
func TestUndoDeployment(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", UID: types.UID("dep-uid")},
		Spec: appsv1.DeploymentSpec{
			Replicas: int32p(2),
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{
					Containers: []corev1.Container{{Name: "app", Image: "web:v2"}},
				},
			},
		},
	}
	// Revision 1 ran a distinct image; undo should restore it.
	rs1 := ownedRS("web-1", dep, "1", "initial", "web:v1")

	cs := fake.NewClientset(dep, rs1)

	if err := Undo(context.Background(), cs, "deployments", "default", "web", 1); err != nil {
		t.Fatalf("Undo returned error: %v", err)
	}

	got, err := cs.AppsV1().Deployments("default").Get(context.Background(), "web", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after undo failed: %v", err)
	}
	if len(got.Spec.Template.Spec.Containers) != 1 {
		t.Fatalf("expected 1 container after undo, got %d", len(got.Spec.Template.Spec.Containers))
	}
	if img := got.Spec.Template.Spec.Containers[0].Image; img != "web:v1" {
		t.Errorf("expected template image restored to web:v1, got %q", img)
	}
}

// TestUndoDeploymentMissingRevision confirms a clear error when the requested
// revision has no backing ReplicaSet.
func TestUndoDeploymentMissingRevision(t *testing.T) {
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default", UID: types.UID("dep-uid")},
		Spec: appsv1.DeploymentSpec{
			Template: corev1.PodTemplateSpec{
				Spec: corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "web:v2"}}},
			},
		},
	}
	rs2 := ownedRS("web-2", dep, "2", "", "web:v2")
	cs := fake.NewClientset(dep, rs2)

	err := Undo(context.Background(), cs, "deployments", "default", "web", 1)
	if err == nil {
		t.Fatalf("expected error undoing to missing revision, got nil")
	}
}

// TestUndoUnknownKind ensures Undo rejects unsupported kinds.
func TestUndoUnknownKind(t *testing.T) {
	cs := fake.NewClientset()
	if err := Undo(context.Background(), cs, "pods", "default", "web", 1); err == nil {
		t.Fatalf("expected error for unknown kind, got nil")
	}
}
