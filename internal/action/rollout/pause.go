package rollout

import (
	"context"
	"fmt"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/client-go/kubernetes"
)

// Pausable reports whether a kind supports pause/resume of its rollout. Only
// Deployments carry a spec.paused field (StatefulSets and DaemonSets do not), so
// the UI uses this to gate the pause action to Deployments alone.
func Pausable(kind string) bool {
	return kind == "deployments"
}

// SetPaused toggles a Deployment's rollout by patching spec.paused. A strategic-
// merge patch is used — matching `kubectl rollout pause/resume` — so only the
// paused field is touched and no unrelated spec the UI never loaded is clobbered.
func SetPaused(ctx context.Context, cs kubernetes.Interface, namespace, name string, paused bool) error {
	patch := []byte(fmt.Sprintf(`{"spec":{"paused":%t}}`, paused))
	_, err := cs.AppsV1().Deployments(namespace).Patch(ctx, name, types.StrategicMergePatchType, patch, metav1.PatchOptions{})
	return err
}
