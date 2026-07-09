// Package scale sets the desired replica count on scalable workloads. It writes
// through the Kubernetes scale subresource (autoscaling/v1 Scale) rather than
// patching the parent object, so it stays consistent with `kubectl scale` and
// avoids clobbering unrelated spec fields the UI never loaded. Only the workload
// kinds that expose a scale subresource are supported.
package scale

import (
	"context"
	"fmt"
	"strconv"
	"strings"

	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// maxReplicas guards against fat-finger input that would otherwise be accepted by
// the API server. It is an arbitrary sanity ceiling (2^20), far above any real
// cluster's capacity, so we reject it before making a request.
const maxReplicas = 1048576

// ParseReplicas turns free-form user text into a validated replica count. It
// returns friendly, UI-ready messages (not raw strconv errors) because the string
// comes straight from a text input the operator typed.
func ParseReplicas(s string) (int32, error) {
	n, err := strconv.Atoi(strings.TrimSpace(s))
	if err != nil {
		return 0, fmt.Errorf("replicas must be a whole number")
	}
	if n < 0 {
		return 0, fmt.Errorf("replicas cannot be negative")
	}
	if n > maxReplicas {
		return 0, fmt.Errorf("replicas too large")
	}
	return int32(n), nil
}

// Scalable reports whether a resource kind (plural, lowercase) can be scaled via
// this package. The UI uses it to decide whether to offer the scale action at all.
func Scalable(kind string) bool {
	switch kind {
	case "deployments", "statefulsets", "replicasets":
		return true
	default:
		return false
	}
}

// Scale sets the replica count on a scalable workload through its scale
// subresource. It reads the current Scale, mutates Spec.Replicas, and writes it
// back so the update carries the correct resourceVersion. An unknown kind is a
// programming error surfaced as an explicit failure.
func Scale(ctx context.Context, cs kubernetes.Interface, kind, namespace, name string, replicas int32) error {
	switch kind {
	case "deployments":
		sc, err := cs.AppsV1().Deployments(namespace).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		sc.Spec.Replicas = replicas
		_, err = cs.AppsV1().Deployments(namespace).UpdateScale(ctx, name, sc, metav1.UpdateOptions{})
		return err
	case "statefulsets":
		sc, err := cs.AppsV1().StatefulSets(namespace).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		sc.Spec.Replicas = replicas
		_, err = cs.AppsV1().StatefulSets(namespace).UpdateScale(ctx, name, sc, metav1.UpdateOptions{})
		return err
	case "replicasets":
		sc, err := cs.AppsV1().ReplicaSets(namespace).GetScale(ctx, name, metav1.GetOptions{})
		if err != nil {
			return err
		}
		sc.Spec.Replicas = replicas
		_, err = cs.AppsV1().ReplicaSets(namespace).UpdateScale(ctx, name, sc, metav1.UpdateOptions{})
		return err
	default:
		return fmt.Errorf("scale not supported for %q", kind)
	}
}
