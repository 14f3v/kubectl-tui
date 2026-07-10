package nodeops

import (
	"context"
	"fmt"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// validEffect reports whether s names one of the three taint effects the API
// server accepts, returning the typed constant. Validating here — rather than
// letting the API reject an Update — lets ParseTaint give the operator an
// immediate, specific error on the value they typed.
func validEffect(s string) (corev1.TaintEffect, bool) {
	switch corev1.TaintEffect(s) {
	case corev1.TaintEffectNoSchedule:
		return corev1.TaintEffectNoSchedule, true
	case corev1.TaintEffectPreferNoSchedule:
		return corev1.TaintEffectPreferNoSchedule, true
	case corev1.TaintEffectNoExecute:
		return corev1.TaintEffectNoExecute, true
	default:
		return "", false
	}
}

// ParseTaint turns a single kubectl-style taint spec into a Taint plus a remove
// flag, mirroring the grammar of `kubectl taint nodes <node> <spec>`. Add forms
// carry an effect: "key=value:Effect" or "key:Effect" (empty value). Remove
// forms end in "-": "key:Effect-" drops that one key+effect pair, and the bare
// "key-" drops every effect for the key (signalled by an empty Effect on the
// returned Taint). The effect, when present, must be one of NoSchedule /
// PreferNoSchedule / NoExecute; anything else is an error rather than a request
// the API server would later refuse.
func ParseTaint(s string) (taint corev1.Taint, remove bool, err error) {
	s = strings.TrimSpace(s)
	if s == "" {
		return corev1.Taint{}, false, fmt.Errorf("empty taint")
	}

	// A trailing "-" marks a removal; strip it and remember the intent.
	if strings.HasSuffix(s, "-") {
		remove = true
		s = strings.TrimSuffix(s, "-")
	}

	// Split off the effect (after ":"), which may be absent for the bare
	// "key-" removal form.
	var effectStr string
	if i := strings.IndexByte(s, ':'); i >= 0 {
		effectStr = s[i+1:]
		s = s[:i]
	}

	// The remainder is "key" or "key=value"; the value is optional.
	var key, value string
	if i := strings.IndexByte(s, '='); i >= 0 {
		key, value = s[:i], s[i+1:]
	} else {
		key = s
	}
	if key == "" {
		return corev1.Taint{}, false, fmt.Errorf("taint key is required")
	}

	// "key-" (remove all effects for key) is the only case where an empty
	// effect is legal; every other form must name a valid effect.
	var effect corev1.TaintEffect
	if effectStr != "" {
		e, ok := validEffect(effectStr)
		if !ok {
			return corev1.Taint{}, false, fmt.Errorf("invalid taint effect %q: must be NoSchedule, PreferNoSchedule, or NoExecute", effectStr)
		}
		effect = e
	} else if !remove {
		return corev1.Taint{}, false, fmt.Errorf("taint effect is required")
	}

	return corev1.Taint{Key: key, Value: value, Effect: effect}, remove, nil
}

// Taint adds or replaces a taint on a node. It reads the node, replaces any
// existing taint sharing the same Key+Effect (so re-applying with a new value
// updates in place rather than duplicating), otherwise appends, then writes the
// whole node back with Update — matching how `kubectl taint` mutates the object.
func Taint(ctx context.Context, cs kubernetes.Interface, node string, t corev1.Taint) error {
	n, err := cs.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
	if err != nil {
		return err
	}

	replaced := false
	for i := range n.Spec.Taints {
		if n.Spec.Taints[i].Key == t.Key && n.Spec.Taints[i].Effect == t.Effect {
			n.Spec.Taints[i] = t
			replaced = true
			break
		}
	}
	if !replaced {
		n.Spec.Taints = append(n.Spec.Taints, t)
	}

	_, err = cs.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
	return err
}

// Untaint removes taints from a node by key, optionally narrowed to a single
// effect. When effect is empty every taint with the key is dropped (the "key-"
// case); otherwise only the matching key+effect pair is removed. The surviving
// taints are written back with Update.
func Untaint(ctx context.Context, cs kubernetes.Interface, node, key string, effect corev1.TaintEffect) error {
	n, err := cs.CoreV1().Nodes().Get(ctx, node, metav1.GetOptions{})
	if err != nil {
		return err
	}

	kept := n.Spec.Taints[:0:0]
	for _, t := range n.Spec.Taints {
		if t.Key == key && (effect == "" || t.Effect == effect) {
			continue
		}
		kept = append(kept, t)
	}
	n.Spec.Taints = kept

	_, err = cs.CoreV1().Nodes().Update(ctx, n, metav1.UpdateOptions{})
	return err
}
