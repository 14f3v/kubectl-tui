package inspect

import (
	"strings"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
)

func TestYAML_StampsGVKAndDoesNotMutate(t *testing.T) {
	pod := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
		Spec:       corev1.PodSpec{Containers: []corev1.Container{{Name: "app", Image: "nginx"}}},
	}
	out, err := YAML(pod)
	if err != nil {
		t.Fatal(err)
	}
	for _, want := range []string{"apiVersion: v1", "kind: Pod", "name: web", "image: nginx"} {
		if !strings.Contains(out, want) {
			t.Fatalf("yaml missing %q:\n%s", want, out)
		}
	}
	// The cached object must not be mutated: its TypeMeta stays empty.
	if pod.APIVersion != "" || pod.Kind != "" {
		t.Fatalf("YAML mutated the source object's TypeMeta: %q/%q", pod.APIVersion, pod.Kind)
	}
}

func TestYAML_RejectsNonRuntimeObject(t *testing.T) {
	if _, err := YAML("not an object"); err == nil {
		t.Fatal("expected error for non-runtime.Object")
	}
}

func TestGroupKindFor(t *testing.T) {
	if gk, ok := GroupKindFor("deployments"); !ok || gk.Group != "apps" || gk.Kind != "Deployment" {
		t.Fatalf("deployments GroupKind = %v (ok=%v)", gk, ok)
	}
	if _, ok := GroupKindFor("widgets"); ok {
		t.Fatal("unexpected GroupKind for unknown kind")
	}
}
