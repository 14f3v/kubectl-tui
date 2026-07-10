package portforward

import (
	"context"
	"reflect"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestParsePorts(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		want    []string
		wantErr bool
	}{
		{name: "single mapping", in: "8080:80", want: []string{"8080:80"}},
		{name: "bare port expands to same", in: "9090", want: []string{"9090:9090"}},
		{name: "multiple mixed", in: "8080:80, 9090, 5432:5432", want: []string{"8080:80", "9090:9090", "5432:5432"}},
		{name: "surrounding whitespace", in: "  8080 : 80  ", want: []string{"8080:80"}},
		{name: "max port", in: "65535:65535", want: []string{"65535:65535"}},
		{name: "empty string", in: "", wantErr: true},
		{name: "only whitespace", in: "   ", wantErr: true},
		{name: "stray comma", in: "8080,,9090", wantErr: true},
		{name: "non-numeric local", in: "abc:80", wantErr: true},
		{name: "non-numeric remote", in: "80:abc", wantErr: true},
		{name: "zero port", in: "0:80", wantErr: true},
		{name: "port too large", in: "80:70000", wantErr: true},
		{name: "negative port", in: "-1:80", wantErr: true},
		{name: "missing remote side", in: "8080:", wantErr: true},
		{name: "missing local side", in: ":80", wantErr: true},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			got, err := ParsePorts(c.in)
			if c.wantErr {
				if err == nil {
					t.Fatalf("ParsePorts(%q): expected error, got %v", c.in, got)
				}
				return
			}
			if err != nil {
				t.Fatalf("ParsePorts(%q): unexpected error %v", c.in, err)
			}
			if !reflect.DeepEqual(got, c.want) {
				t.Fatalf("ParsePorts(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// readyPod builds a pod in the given phase with an explicit PodReady condition.
func readyPod(name, phase string, ready bool) corev1.Pod {
	status := corev1.ConditionFalse
	if ready {
		status = corev1.ConditionTrue
	}
	return corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: "default", Labels: map[string]string{"app": "web"}},
		Status: corev1.PodStatus{
			Phase: corev1.PodPhase(phase),
			Conditions: []corev1.PodCondition{
				{Type: corev1.PodReady, Status: status},
			},
		},
	}
}

func TestPodReady(t *testing.T) {
	cases := []struct {
		name string
		pod  corev1.Pod
		want bool
	}{
		{name: "running and ready", pod: readyPod("a", "Running", true), want: true},
		{name: "running but not ready", pod: readyPod("b", "Running", false), want: false},
		{name: "ready condition true but pending phase", pod: readyPod("c", "Pending", true), want: false},
		{
			name: "no ready condition at all",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "d"},
				Status:     corev1.PodStatus{Phase: corev1.PodRunning},
			},
			want: false,
		},
		{
			name: "succeeded (completed) pod",
			pod: corev1.Pod{
				ObjectMeta: metav1.ObjectMeta{Name: "e"},
				Status:     corev1.PodStatus{Phase: corev1.PodSucceeded},
			},
			want: false,
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := podReady(&c.pod); got != c.want {
				t.Errorf("podReady(%s) = %v, want %v", c.pod.Name, got, c.want)
			}
		})
	}
}

func TestFirstReadyPod(t *testing.T) {
	t.Run("skips not-ready and returns first ready", func(t *testing.T) {
		pods := []corev1.Pod{
			readyPod("pending", "Pending", true),
			readyPod("notready", "Running", false),
			readyPod("good", "Running", true),
			readyPod("also-good", "Running", true),
		}
		name, ok := firstReadyPod(pods)
		if !ok {
			t.Fatalf("firstReadyPod: expected a ready pod, got none")
		}
		if name != "good" {
			t.Errorf("firstReadyPod = %q, want %q", name, "good")
		}
	})

	t.Run("none ready", func(t *testing.T) {
		pods := []corev1.Pod{
			readyPod("a", "Running", false),
			readyPod("b", "Pending", true),
		}
		if name, ok := firstReadyPod(pods); ok {
			t.Errorf("firstReadyPod = %q, want no match", name)
		}
	})

	t.Run("empty slice", func(t *testing.T) {
		if _, ok := firstReadyPod(nil); ok {
			t.Errorf("firstReadyPod(nil): expected no match")
		}
	})
}

func TestPodForSelector(t *testing.T) {
	ctx := context.Background()

	t.Run("returns first ready matching pod", func(t *testing.T) {
		notReady := readyPod("web-0", "Running", false)
		ready := readyPod("web-1", "Running", true)
		// A pod that is ready but does NOT match the selector must be ignored.
		other := readyPod("db-0", "Running", true)
		other.Labels = map[string]string{"app": "db"}

		cs := fake.NewClientset(&notReady, &ready, &other)
		got, err := PodForSelector(ctx, cs, "default", map[string]string{"app": "web"})
		if err != nil {
			t.Fatalf("PodForSelector: unexpected error %v", err)
		}
		if got != "web-1" {
			t.Errorf("PodForSelector = %q, want %q", got, "web-1")
		}
	})

	t.Run("no ready pod is an error", func(t *testing.T) {
		notReady := readyPod("web-0", "Running", false)
		cs := fake.NewClientset(&notReady)
		if _, err := PodForSelector(ctx, cs, "default", map[string]string{"app": "web"}); err == nil {
			t.Errorf("PodForSelector: expected error when no ready pod, got nil")
		}
	})

	t.Run("empty selector is rejected", func(t *testing.T) {
		cs := fake.NewClientset()
		if _, err := PodForSelector(ctx, cs, "default", nil); err == nil {
			t.Errorf("PodForSelector(nil): expected error, got nil")
		}
	})
}

func TestPodForService(t *testing.T) {
	ctx := context.Background()

	t.Run("resolves service selector to ready pod", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "web", Namespace: "default"},
			Spec:       corev1.ServiceSpec{Selector: map[string]string{"app": "web"}},
		}
		ready := readyPod("web-1", "Running", true)
		cs := fake.NewClientset(svc, &ready)

		got, err := PodForService(ctx, cs, "default", "web")
		if err != nil {
			t.Fatalf("PodForService: unexpected error %v", err)
		}
		if got != "web-1" {
			t.Errorf("PodForService = %q, want %q", got, "web-1")
		}
	})

	t.Run("service without selector is an error", func(t *testing.T) {
		svc := &corev1.Service{
			ObjectMeta: metav1.ObjectMeta{Name: "external", Namespace: "default"},
			Spec:       corev1.ServiceSpec{Type: corev1.ServiceTypeExternalName, ExternalName: "example.com"},
		}
		cs := fake.NewClientset(svc)
		if _, err := PodForService(ctx, cs, "default", "external"); err == nil {
			t.Errorf("PodForService: expected error for selector-less service, got nil")
		}
	})

	t.Run("missing service surfaces the get error", func(t *testing.T) {
		cs := fake.NewClientset()
		if _, err := PodForService(ctx, cs, "default", "ghost"); err == nil {
			t.Errorf("PodForService: expected error for missing service, got nil")
		}
	})
}

// TestForwardRejectsEmptyPorts covers the one synchronous guard in Forward that
// is testable without a live apiserver. The rest of Forward opens real SPDY
// streams and is exercised end-to-end against a cluster, not in unit tests.
func TestForwardRejectsEmptyPorts(t *testing.T) {
	cs := fake.NewClientset()
	if _, _, err := Forward(context.Background(), nil, cs, "default", "web", nil, nil); err == nil {
		t.Errorf("Forward with no ports: expected error, got nil")
	}
}
