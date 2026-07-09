package nodeops

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParseTaint(t *testing.T) {
	tests := []struct {
		name       string
		in         string
		wantTaint  corev1.Taint
		wantRemove bool
		wantErr    bool
	}{
		{
			name:      "add key=value:NoSchedule",
			in:        "dedicated=gpu:NoSchedule",
			wantTaint: corev1.Taint{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
		},
		{
			name:      "add key:Effect empty value",
			in:        "dedicated:PreferNoSchedule",
			wantTaint: corev1.Taint{Key: "dedicated", Effect: corev1.TaintEffectPreferNoSchedule},
		},
		{
			name:       "remove key:Effect-",
			in:         "dedicated:NoExecute-",
			wantTaint:  corev1.Taint{Key: "dedicated", Effect: corev1.TaintEffectNoExecute},
			wantRemove: true,
		},
		{
			name:       "remove key- all effects",
			in:         "dedicated-",
			wantTaint:  corev1.Taint{Key: "dedicated"},
			wantRemove: true,
		},
		{
			name:    "invalid effect errors",
			in:      "dedicated=gpu:Bogus",
			wantErr: true,
		},
		{
			name:    "missing effect on add errors",
			in:      "dedicated=gpu",
			wantErr: true,
		},
		{
			name:    "empty spec errors",
			in:      "",
			wantErr: true,
		},
		{
			name:    "missing key errors",
			in:      "=gpu:NoSchedule",
			wantErr: true,
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			taint, remove, err := ParseTaint(tc.in)
			if tc.wantErr {
				if err == nil {
					t.Fatalf("ParseTaint(%q): expected error, got nil", tc.in)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParseTaint(%q): unexpected error: %v", tc.in, err)
			}
			if remove != tc.wantRemove {
				t.Errorf("ParseTaint(%q): remove = %v, want %v", tc.in, remove, tc.wantRemove)
			}
			if taint != tc.wantTaint {
				t.Errorf("ParseTaint(%q): taint = %+v, want %+v", tc.in, taint, tc.wantTaint)
			}
		})
	}
}

// hasTaint reports whether a node carries a taint matching key+effect, used to
// assert the round-trip through the fake clientset.
func hasTaint(taints []corev1.Taint, key string, effect corev1.TaintEffect) bool {
	for _, t := range taints {
		if t.Key == key && t.Effect == effect {
			return true
		}
	}
	return false
}

func TestTaint(t *testing.T) {
	ctx := context.Background()
	node := &corev1.Node{ObjectMeta: metav1.ObjectMeta{Name: "node-1"}}
	cs := fake.NewClientset(node)

	tnt := corev1.Taint{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule}
	if err := Taint(ctx, cs, "node-1", tnt); err != nil {
		t.Fatalf("Taint: unexpected error: %v", err)
	}

	got, err := cs.CoreV1().Nodes().Get(ctx, "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after Taint: %v", err)
	}
	if !hasTaint(got.Spec.Taints, "dedicated", corev1.TaintEffectNoSchedule) {
		t.Fatalf("taint not present after Taint: %+v", got.Spec.Taints)
	}

	// Re-applying the same key+effect with a new value replaces in place
	// rather than duplicating.
	tnt2 := corev1.Taint{Key: "dedicated", Value: "tpu", Effect: corev1.TaintEffectNoSchedule}
	if err := Taint(ctx, cs, "node-1", tnt2); err != nil {
		t.Fatalf("Taint (replace): unexpected error: %v", err)
	}
	got, err = cs.CoreV1().Nodes().Get(ctx, "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after replace: %v", err)
	}
	if n := len(got.Spec.Taints); n != 1 {
		t.Fatalf("expected 1 taint after replace, got %d: %+v", n, got.Spec.Taints)
	}
	if got.Spec.Taints[0].Value != "tpu" {
		t.Errorf("expected replaced value \"tpu\", got %q", got.Spec.Taints[0].Value)
	}
}

func TestUntaint(t *testing.T) {
	ctx := context.Background()
	node := &corev1.Node{
		ObjectMeta: metav1.ObjectMeta{Name: "node-1"},
		Spec: corev1.NodeSpec{
			Taints: []corev1.Taint{
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoSchedule},
				{Key: "dedicated", Value: "gpu", Effect: corev1.TaintEffectNoExecute},
				{Key: "other", Effect: corev1.TaintEffectNoSchedule},
			},
		},
	}
	cs := fake.NewClientset(node)

	// Effect-scoped removal drops only the matching pair.
	if err := Untaint(ctx, cs, "node-1", "dedicated", corev1.TaintEffectNoSchedule); err != nil {
		t.Fatalf("Untaint (scoped): unexpected error: %v", err)
	}
	got, err := cs.CoreV1().Nodes().Get(ctx, "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after scoped Untaint: %v", err)
	}
	if hasTaint(got.Spec.Taints, "dedicated", corev1.TaintEffectNoSchedule) {
		t.Errorf("scoped taint still present: %+v", got.Spec.Taints)
	}
	if !hasTaint(got.Spec.Taints, "dedicated", corev1.TaintEffectNoExecute) {
		t.Errorf("unrelated effect wrongly removed: %+v", got.Spec.Taints)
	}
	if !hasTaint(got.Spec.Taints, "other", corev1.TaintEffectNoSchedule) {
		t.Errorf("unrelated key wrongly removed: %+v", got.Spec.Taints)
	}

	// Empty effect removes every taint sharing the key.
	if err := Untaint(ctx, cs, "node-1", "dedicated", ""); err != nil {
		t.Fatalf("Untaint (all effects): unexpected error: %v", err)
	}
	got, err = cs.CoreV1().Nodes().Get(ctx, "node-1", metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get after all-effects Untaint: %v", err)
	}
	if hasTaint(got.Spec.Taints, "dedicated", corev1.TaintEffectNoExecute) {
		t.Errorf("dedicated taint still present after all-effects untaint: %+v", got.Spec.Taints)
	}
	if !hasTaint(got.Spec.Taints, "other", corev1.TaintEffectNoSchedule) {
		t.Errorf("unrelated key wrongly removed: %+v", got.Spec.Taints)
	}
}
