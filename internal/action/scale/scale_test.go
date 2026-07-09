package scale

import (
	"context"
	"testing"

	appsv1 "k8s.io/api/apps/v1"
	autoscalingv1 "k8s.io/api/autoscaling/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
)

func TestParseReplicas(t *testing.T) {
	cases := []struct {
		in      string
		want    int32
		wantErr string
	}{
		{in: "3", want: 3},
		{in: " 0 ", want: 0},
		{in: "42", want: 42},
		{in: "-1", wantErr: "replicas cannot be negative"},
		{in: "abc", wantErr: "replicas must be a whole number"},
		{in: "", wantErr: "replicas must be a whole number"},
		{in: "1048577", wantErr: "replicas too large"},
	}
	for _, c := range cases {
		got, err := ParseReplicas(c.in)
		if c.wantErr != "" {
			if err == nil {
				t.Errorf("ParseReplicas(%q): expected error %q, got nil", c.in, c.wantErr)
				continue
			}
			if err.Error() != c.wantErr {
				t.Errorf("ParseReplicas(%q): error = %q, want %q", c.in, err.Error(), c.wantErr)
			}
			continue
		}
		if err != nil {
			t.Errorf("ParseReplicas(%q): unexpected error %v", c.in, err)
			continue
		}
		if got != c.want {
			t.Errorf("ParseReplicas(%q) = %d, want %d", c.in, got, c.want)
		}
	}
}

func TestScalable(t *testing.T) {
	cases := map[string]bool{
		"deployments":  true,
		"statefulsets": true,
		"replicasets":  true,
		"pods":         false,
		"daemonsets":   false,
		"":             false,
		"Deployments":  false, // case-sensitive: only lowercase plural is accepted
	}
	for kind, want := range cases {
		if got := Scalable(kind); got != want {
			t.Errorf("Scalable(%q) = %v, want %v", kind, got, want)
		}
	}
}

func TestScale(t *testing.T) {
	const (
		ns   = "default"
		name = "web"
	)
	one := int32(1)
	dep := &appsv1.Deployment{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Spec:       appsv1.DeploymentSpec{Replicas: &one},
	}
	cs := fake.NewClientset(dep)
	ctx := context.Background()

	// FAKE LIMITATION: the apps/v1 fake does NOT synthesize a Scale object from
	// the parent Deployment. Its default "get scale" reactor fetches the
	// *Deployment from the object tracker and blindly type-asserts it to *Scale,
	// which panics. So we install our own scale subresource reactors backed by a
	// small in-memory replica count. This lets the test exercise the real
	// Get -> mutate -> Update flow inside Scale and assert the value round-trips,
	// rather than only checking that Scale returned nil.
	current := *dep.Spec.Replicas
	cs.PrependReactor("get", "deployments/scale", func(a k8stesting.Action) (bool, runtime.Object, error) {
		return true, &autoscalingv1.Scale{
			ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
			Spec:       autoscalingv1.ScaleSpec{Replicas: current},
		}, nil
	})
	cs.PrependReactor("update", "deployments/scale", func(a k8stesting.Action) (bool, runtime.Object, error) {
		sc := a.(k8stesting.UpdateAction).GetObject().(*autoscalingv1.Scale)
		current = sc.Spec.Replicas
		return true, sc, nil
	})

	if err := Scale(ctx, cs, "deployments", ns, name, 3); err != nil {
		t.Fatalf("Scale(deployments): unexpected error %v", err)
	}
	if current != 3 {
		t.Errorf("after Scale, replica count = %d, want 3", current)
	}

	sc, err := cs.AppsV1().Deployments(ns).GetScale(ctx, name, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("GetScale after Scale: unexpected error %v", err)
	}
	if sc.Spec.Replicas != 3 {
		t.Errorf("GetScale Spec.Replicas = %d, want 3", sc.Spec.Replicas)
	}

	// Unsupported kinds must fail loudly rather than silently no-op.
	if err := Scale(ctx, cs, "pods", ns, name, 3); err == nil {
		t.Errorf("Scale(pods): expected error for unsupported kind, got nil")
	}
}
