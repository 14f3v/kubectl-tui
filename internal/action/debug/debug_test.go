package debug

import (
	"context"
	"testing"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes/fake"
)

func TestEphemeralSpec(t *testing.T) {
	ec := EphemeralSpec("busybox:1.36", "app", "debugger")

	if ec.Name != "debugger" {
		t.Errorf("Name = %q, want %q", ec.Name, "debugger")
	}
	if ec.Image != "busybox:1.36" {
		t.Errorf("Image = %q, want %q", ec.Image, "busybox:1.36")
	}
	if !ec.Stdin {
		t.Errorf("Stdin = false, want true")
	}
	if !ec.TTY {
		t.Errorf("TTY = false, want true")
	}
	if ec.TargetContainerName != "app" {
		t.Errorf("TargetContainerName = %q, want %q", ec.TargetContainerName, "app")
	}
	// The keep-alive command must be a long sleep so the container stays Running
	// for the UI to attach a shell.
	want := []string{"sh", "-c", "sleep 2147483647"}
	if len(ec.Command) != len(want) {
		t.Fatalf("Command = %v, want %v", ec.Command, want)
	}
	for i := range want {
		if ec.Command[i] != want[i] {
			t.Errorf("Command[%d] = %q, want %q", i, ec.Command[i], want[i])
		}
	}

	// With an empty target the field must stay unset (fall back to the pod's
	// default shared process namespace).
	if got := EphemeralSpec("busybox", "", "debugger"); got.TargetContainerName != "" {
		t.Errorf("empty target: TargetContainerName = %q, want empty", got.TargetContainerName)
	}
}

func TestNodeDebugPod(t *testing.T) {
	p := NodeDebugPod("node-1", "busybox:1.36")

	if p.Spec.NodeName != "node-1" {
		t.Errorf("NodeName = %q, want %q", p.Spec.NodeName, "node-1")
	}
	if !p.Spec.HostPID {
		t.Errorf("HostPID = false, want true")
	}
	if !p.Spec.HostNetwork {
		t.Errorf("HostNetwork = false, want true")
	}
	if !p.Spec.HostIPC {
		t.Errorf("HostIPC = false, want true")
	}
	if p.Spec.RestartPolicy != corev1.RestartPolicyNever {
		t.Errorf("RestartPolicy = %q, want %q", p.Spec.RestartPolicy, corev1.RestartPolicyNever)
	}
	if p.GenerateName != "node-debugger-" {
		t.Errorf("GenerateName = %q, want %q", p.GenerateName, "node-debugger-")
	}
	if p.Namespace != "default" {
		t.Errorf("Namespace = %q, want %q", p.Namespace, "default")
	}

	// Exactly one privileged debugger container mounting the host root at /host.
	if len(p.Spec.Containers) != 1 {
		t.Fatalf("Containers = %d, want 1", len(p.Spec.Containers))
	}
	c := p.Spec.Containers[0]
	if c.SecurityContext == nil || c.SecurityContext.Privileged == nil || !*c.SecurityContext.Privileged {
		t.Errorf("container SecurityContext.Privileged = %v, want true", c.SecurityContext)
	}
	var mounted bool
	for _, m := range c.VolumeMounts {
		if m.Name == "host" && m.MountPath == "/host" {
			mounted = true
		}
	}
	if !mounted {
		t.Errorf("host volume not mounted at /host: %v", c.VolumeMounts)
	}

	// The "host" volume must be a HostPath at the node's root "/".
	var hostVol bool
	for _, v := range p.Spec.Volumes {
		if v.Name == "host" && v.HostPath != nil && v.HostPath.Path == "/" {
			hostVol = true
		}
	}
	if !hostVol {
		t.Errorf("host volume is not a HostPath at /: %v", p.Spec.Volumes)
	}

	// An Exists toleration lets the pod schedule onto tainted/cordoned nodes.
	var existsTol bool
	for _, tol := range p.Spec.Tolerations {
		if tol.Operator == corev1.TolerationOpExists {
			existsTol = true
		}
	}
	if !existsTol {
		t.Errorf("missing Exists toleration: %v", p.Spec.Tolerations)
	}
}

func TestAddEphemeralContainer(t *testing.T) {
	const (
		ns  = "default"
		pod = "web"
	)
	base := &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: pod, Namespace: ns},
		Spec: corev1.PodSpec{
			Containers: []corev1.Container{{Name: "app", Image: "nginx"}},
		},
	}
	cs := fake.NewClientset(base)
	ctx := context.Background()

	name1, err := AddEphemeralContainer(ctx, cs, ns, pod, "busybox:1.36", "app")
	if err != nil {
		t.Fatalf("AddEphemeralContainer (first): %v", err)
	}
	if name1 != "debugger" {
		t.Errorf("first container name = %q, want %q", name1, "debugger")
	}

	got, err := cs.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pod after first add: %v", err)
	}
	if len(got.Spec.EphemeralContainers) != 1 {
		t.Fatalf("EphemeralContainers = %d, want 1", len(got.Spec.EphemeralContainers))
	}
	ec := got.Spec.EphemeralContainers[0]
	if ec.Name != name1 {
		t.Errorf("appended container name = %q, want %q", ec.Name, name1)
	}
	if ec.Image != "busybox:1.36" {
		t.Errorf("appended container image = %q, want %q", ec.Image, "busybox:1.36")
	}
	if ec.TargetContainerName != "app" {
		t.Errorf("appended TargetContainerName = %q, want %q", ec.TargetContainerName, "app")
	}

	// A second call must pick a distinct, non-colliding name.
	name2, err := AddEphemeralContainer(ctx, cs, ns, pod, "busybox:1.36", "")
	if err != nil {
		t.Fatalf("AddEphemeralContainer (second): %v", err)
	}
	if name2 == name1 {
		t.Errorf("second container name = %q, want distinct from %q", name2, name1)
	}

	got, err = cs.CoreV1().Pods(ns).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		t.Fatalf("Get pod after second add: %v", err)
	}
	if len(got.Spec.EphemeralContainers) != 2 {
		t.Fatalf("EphemeralContainers = %d, want 2", len(got.Spec.EphemeralContainers))
	}
}

// WaitRunning is not exercised against the fake clientset: the fake never flips
// an ephemeral container's status to Running (there is no kubelet), so a happy
// path would only ever hit the timeout. The Running/Terminated decision logic is
// straightforward status inspection; it is verified against a live cluster
// rather than the fake. See the verified-vs-deferred note in the task report.
