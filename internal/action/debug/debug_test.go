package debug

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/kubernetes/fake"
	k8stesting "k8s.io/client-go/testing"
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

// podWithContainerState builds a pod whose regular container `container` is in
// the given state, so the wait helpers can be exercised against the fake
// clientset by seeding the status the kubelet would otherwise write.
func podWithContainerState(ns, name, container string, state corev1.ContainerState) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{Name: name, Namespace: ns},
		Status: corev1.PodStatus{
			ContainerStatuses: []corev1.ContainerStatus{{Name: container, State: state}},
		},
	}
}

func TestNodeDebugPodSetsActiveDeadline(t *testing.T) {
	p := NodeDebugPod("node-1", "busybox")

	// A privileged host-namespace pod must carry a server-side deadline: the TUI
	// can be quit or killed while the create is still in flight, and then no
	// client-side cleanup path runs at all.
	if p.Spec.ActiveDeadlineSeconds == nil {
		t.Fatal("ActiveDeadlineSeconds = nil, want a server-side backstop")
	}
	if got := *p.Spec.ActiveDeadlineSeconds; got <= 0 || got > 24*60*60 {
		t.Errorf("ActiveDeadlineSeconds = %d, want positive and under 24h", got)
	}
}

func TestWaitPodContainerRunningObservesRegularContainer(t *testing.T) {
	// The node-debug container is a REGULAR container, so its status lands in
	// Status.ContainerStatuses — not Status.EphemeralContainerStatuses.
	cs := fake.NewClientset(podWithContainerState("default", "node-debugger-x", "debugger",
		corev1.ContainerState{Running: &corev1.ContainerStateRunning{}}))

	if err := WaitPodContainerRunning(context.Background(), cs, "default", "node-debugger-x", "debugger", 2*time.Second); err != nil {
		t.Fatalf("WaitPodContainerRunning = %v, want nil", err)
	}
}

func TestWaitPodContainerRunningTimeoutIsNotTerminal(t *testing.T) {
	// A slow image pull must report a timeout the caller can distinguish, so it
	// keeps the pod alive instead of destroying a container that would have come
	// up a moment later.
	cs := fake.NewClientset(podWithContainerState("default", "p", "debugger",
		corev1.ContainerState{Waiting: &corev1.ContainerStateWaiting{Reason: "ContainerCreating"}}))

	err := WaitPodContainerRunning(context.Background(), cs, "default", "p", "debugger", 50*time.Millisecond)
	if !errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want ErrWaitTimeout", err)
	}
}

func TestWaitPodContainerRunningTerminatedIsTerminal(t *testing.T) {
	cs := fake.NewClientset(podWithContainerState("default", "p", "debugger",
		corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1}}))

	err := WaitPodContainerRunning(context.Background(), cs, "default", "p", "debugger", 2*time.Second)
	if err == nil {
		t.Fatal("err = nil, want a terminal error")
	}
	if errors.Is(err, ErrWaitTimeout) {
		t.Fatalf("err = %v, want a terminal error distinct from ErrWaitTimeout", err)
	}
}

// withGeneratedNames teaches the fake clientset to honor GenerateName the way a
// real API server does. The fake's tracker stores the object verbatim, leaving
// Name empty, which would make every generated-name assertion meaningless.
func withGeneratedNames(cs *fake.Clientset) {
	var n int
	cs.PrependReactor("create", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		p, ok := action.(k8stesting.CreateAction).GetObject().(*corev1.Pod)
		if !ok || p.GenerateName == "" || p.Name != "" {
			return false, nil, nil
		}
		n++
		p.Name = fmt.Sprintf("%s%d", p.GenerateName, n)
		return false, nil, nil // fall through so the tracker still stores it
	})
}

func TestCreateNodeDebugKeepsPodOnTimeout(t *testing.T) {
	// The fake never starts containers, so the wait always times out here. A
	// timeout must NOT delete: the pod may still be pulling and the operator can
	// still attach to it once it comes up.
	cs := fake.NewClientset()
	withGeneratedNames(cs)

	ns, pod, err := createNodeDebug(context.Background(), cs, "node-1", "busybox", 50*time.Millisecond)
	if err == nil {
		t.Fatal("err = nil, want a timeout error")
	}
	if ns == "" || pod == "" {
		t.Fatalf("ns/pod = %q/%q, want the real names so the caller can still act on the pod", ns, pod)
	}

	got, gerr := cs.CoreV1().Pods(ns).Get(context.Background(), pod, metav1.GetOptions{})
	if gerr != nil || got == nil {
		t.Fatalf("pod was deleted on a plain timeout (get: %v); a slow pull must survive", gerr)
	}
}

func TestCreateNodeDebugDeletesPodOnTerminalFailure(t *testing.T) {
	cs := fake.NewClientset()
	withGeneratedNames(cs)
	// Once created, report the debugger as terminated so the wait fails terminally.
	cs.PrependReactor("get", "pods", func(action k8stesting.Action) (bool, runtime.Object, error) {
		g := action.(k8stesting.GetAction)
		return true, podWithContainerState(g.GetNamespace(), g.GetName(), "debugger",
			corev1.ContainerState{Terminated: &corev1.ContainerStateTerminated{Reason: "Error", ExitCode: 1}}), nil
	})

	ns, pod, err := createNodeDebug(context.Background(), cs, "node-1", "busybox", 2*time.Second)
	if err == nil {
		t.Fatal("err = nil, want a terminal error")
	}
	if ns == "" || pod == "" {
		t.Fatalf("ns/pod = %q/%q, want the real names even on failure", ns, pod)
	}

	var deleted bool
	for _, a := range cs.Actions() {
		if a.GetVerb() == "delete" && a.GetResource().Resource == "pods" {
			deleted = true
		}
	}
	if !deleted {
		t.Error("no delete issued; a terminally-failed privileged pod must be cleaned up")
	}
}
