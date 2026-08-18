// Package debug implements the two "get me a shell into a broken thing" actions,
// mirroring `kubectl debug`. AddEphemeralContainer injects an ephemeral debug
// container into a running pod's process/network namespaces so the operator can
// poke at a distroless or crash-looping workload that has no shell of its own.
// NodeDebugPod/CreateNodeDebug launch a privileged host-namespace pod pinned to a
// node with the host filesystem mounted at /host, the standard trick for
// inspecting a node you can't SSH to. Both keep their container alive with a long
// sleep so the UI can attach an interactive shell afterwards — this package only
// creates and waits; the exec/attach is wired separately by the UI, which is why
// we deliberately do not import the exec-shell package here.
package debug

import (
	"context"
	"errors"
	"fmt"
	"time"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/client-go/kubernetes"
)

// keepAliveCommand parks the debug container in a near-infinite sleep. Without a
// command the image's entrypoint would run (and possibly exit); with this the
// container stays Running so the UI has something live to exec a shell into. The
// value is INT_MAX seconds — the largest sleep argument that is portable across
// busybox/coreutils `sleep`, i.e. effectively forever.
var keepAliveCommand = []string{"sh", "-c", "sleep 2147483647"}

// debugImageName is the base name we assign to injected ephemeral containers.
// Successive injections into the same pod get -1, -2, … suffixes so names never
// collide (the API server rejects duplicate ephemeral container names).
const debugContainerBaseName = "debugger"

// EphemeralSpec builds the ephemeral container we graft onto a target pod. Stdin
// and TTY are enabled up front so the container is ready for the UI to attach an
// interactive shell; the keep-alive command holds it Running until then. When
// target is non-empty we set TargetContainerName so the debugger shares that
// container's process namespace (the point of `kubectl debug --target`); leaving
// it empty falls back to the pod's default shared namespace.
func EphemeralSpec(image, target, name string) corev1.EphemeralContainer {
	ec := corev1.EphemeralContainer{
		EphemeralContainerCommon: corev1.EphemeralContainerCommon{
			Name:    name,
			Image:   image,
			Stdin:   true,
			TTY:     true,
			Command: keepAliveCommand,
		},
	}
	if target != "" {
		ec.TargetContainerName = target
	}
	return ec
}

// uniqueEphemeralName returns a debug container name that does not collide with
// any already present on the pod. It walks "debugger", "debugger-1", "debugger-2"
// … until it finds a free one, so re-running debug on the same pod always
// succeeds instead of failing on a duplicate-name error from the API server.
func uniqueEphemeralName(existing []corev1.EphemeralContainer) string {
	taken := make(map[string]bool, len(existing))
	for _, ec := range existing {
		taken[ec.Name] = true
	}
	name := debugContainerBaseName
	for i := 1; taken[name]; i++ {
		name = fmt.Sprintf("%s-%d", debugContainerBaseName, i)
	}
	return name
}

// AddEphemeralContainer injects a debug container into a running pod and returns
// the name it chose. It reads the pod to pick a non-colliding name and to carry
// the current object into the update, appends the ephemeral container, then
// writes through the dedicated ephemeralcontainers subresource — the only path
// the API server accepts for adding one to an existing pod. The returned name is
// what the UI later execs into.
func AddEphemeralContainer(ctx context.Context, cs kubernetes.Interface, namespace, pod, image, target string) (containerName string, err error) {
	p, err := cs.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
	if err != nil {
		return "", fmt.Errorf("get pod %s/%s: %w", namespace, pod, err)
	}

	name := uniqueEphemeralName(p.Spec.EphemeralContainers)
	p.Spec.EphemeralContainers = append(p.Spec.EphemeralContainers, EphemeralSpec(image, target, name))

	if _, err := cs.CoreV1().Pods(namespace).UpdateEphemeralContainers(ctx, pod, p, metav1.UpdateOptions{}); err != nil {
		return "", fmt.Errorf("add ephemeral container to %s/%s: %w", namespace, pod, err)
	}
	return name, nil
}

// waitPollInterval and waitTimeout bound the wait helpers' polling. We poll
// rather than watch because the caller just created the container and only needs
// a short, bounded wait for it to come up; a full watch would be heavier than the
// interaction warrants.
//
// nodeWaitTimeout is longer than waitTimeout because a node-debug pod is a fresh
// pod that must be scheduled and have its image pulled, whereas an ephemeral
// container joins a pod whose node is already running.
const (
	waitPollInterval = 500 * time.Millisecond
	waitTimeout      = 30 * time.Second
	nodeWaitTimeout  = 120 * time.Second
	deleteTimeout    = 15 * time.Second
)

// nodeDebugDeadlineSeconds caps how long a privileged node-debug pod may live,
// enforced by the API server. Two hours is far longer than any interactive
// debugging session but short enough that a pod orphaned by a crashed or killed
// client cannot sit on a node indefinitely.
const nodeDebugDeadlineSeconds int64 = 7200

// ErrWaitTimeout reports that a debug container did not reach Running before the
// deadline. It is deliberately distinct from a terminal failure: a pod that is
// merely slow to pull will still come up, so callers must NOT treat a timeout as
// licence to delete the pod they just created.
var ErrWaitTimeout = errors.New("timed out waiting for debug container to start")

// containerStater picks the state of the container we are waiting on out of a
// pod's status. Ephemeral and regular containers report into different status
// lists, and picking the wrong one means the wait can never succeed.
type containerStater func(p *corev1.Pod, container string) *corev1.ContainerState

func ephemeralState(p *corev1.Pod, container string) *corev1.ContainerState {
	for i := range p.Status.EphemeralContainerStatuses {
		if p.Status.EphemeralContainerStatuses[i].Name == container {
			return &p.Status.EphemeralContainerStatuses[i].State
		}
	}
	return nil
}

func regularState(p *corev1.Pod, container string) *corev1.ContainerState {
	for i := range p.Status.ContainerStatuses {
		if p.Status.ContainerStatuses[i].Name == container {
			return &p.Status.ContainerStatuses[i].State
		}
	}
	return nil
}

// waitContainer polls a pod until the selected container is Running, it
// terminates, or the deadline passes. A timeout is reported as ErrWaitTimeout so
// the caller can tell "not yet" from "never".
func waitContainer(ctx context.Context, cs kubernetes.Interface, namespace, pod, container string, timeout time.Duration, state containerStater) error {
	deadline := time.Now().Add(timeout)
	for {
		p, err := cs.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get pod %s/%s: %w", namespace, pod, err)
		}
		if st := state(p, container); st != nil {
			if st.Running != nil {
				return nil
			}
			if t := st.Terminated; t != nil {
				reason := t.Reason
				if reason == "" {
					reason = fmt.Sprintf("exit code %d", t.ExitCode)
				}
				return fmt.Errorf("debug container %q terminated: %s", container, reason)
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("%w: %q", ErrWaitTimeout, container)
		}

		// Sleep until the next poll, but wake immediately on cancellation so a
		// user who backs out of the action isn't stuck for the full interval.
		select {
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(waitPollInterval):
		}
	}
}

// WaitPodContainerRunning waits for a REGULAR container to reach Running. The
// node-debug pod's container is an ordinary one, so its status appears in
// Status.ContainerStatuses; waiting on the ephemeral list instead can never
// match and always burns the full deadline.
func WaitPodContainerRunning(ctx context.Context, cs kubernetes.Interface, namespace, pod, container string, timeout time.Duration) error {
	return waitContainer(ctx, cs, namespace, pod, container, timeout, regularState)
}

// WaitRunning blocks until the named ephemeral container on a pod reaches the
// Running state, or reports a friendly error if it terminates first or the wait
// times out. It polls the pod every ~500ms for up to ~30s and inspects the
// container's EphemeralContainerStatuses: Running means the UI can attach;
// Terminated means the image exited (surfaced with its reason so the operator
// knows why); a timeout means the container never scheduled or pulled in time.
// Context cancellation returns promptly with the context's error.
func WaitRunning(ctx context.Context, cs kubernetes.Interface, namespace, pod, container string) error {
	return waitContainer(ctx, cs, namespace, pod, container, waitTimeout, ephemeralState)
}

// NodeDebugPod builds the privileged host-namespace pod used to inspect a node,
// matching `kubectl debug node/<node>`. It is pinned to the target node and
// joins the host's PID, network, and IPC namespaces, with the host root
// filesystem bind-mounted read-write at /host, so from inside the container the
// operator sees the node's processes, sockets, and files. RestartPolicy Never
// keeps it a one-shot; the Exists toleration lets it schedule onto even tainted
// or cordoned nodes (the whole point is to debug nodes that are unhealthy); and
// the container is Privileged so it can actually read what it needs. The
// GenerateName lets the API server assign a unique name per invocation.
func NodeDebugPod(node, image string) *corev1.Pod {
	privileged := true
	deadline := nodeDebugDeadlineSeconds
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			GenerateName: "node-debugger-",
			Namespace:    "default",
		},
		Spec: corev1.PodSpec{
			NodeName:      node,
			HostPID:       true,
			HostNetwork:   true,
			HostIPC:       true,
			RestartPolicy: corev1.RestartPolicyNever,
			// Server-side backstop. Client-side cleanup covers the paths we control,
			// but Bubble Tea abandons in-flight commands on quit and the process can
			// be killed outright — in both cases nothing local runs. The deadline is
			// the only thing that stops a privileged host-root container then.
			ActiveDeadlineSeconds: &deadline,
			Tolerations: []corev1.Toleration{
				{Operator: corev1.TolerationOpExists},
			},
			Containers: []corev1.Container{{
				Name:    "debugger",
				Image:   image,
				Stdin:   true,
				TTY:     true,
				Command: keepAliveCommand,
				SecurityContext: &corev1.SecurityContext{
					Privileged: &privileged,
				},
				VolumeMounts: []corev1.VolumeMount{{
					Name:      "host",
					MountPath: "/host",
				}},
			}},
			Volumes: []corev1.Volume{{
				Name: "host",
				VolumeSource: corev1.VolumeSource{
					HostPath: &corev1.HostPathVolumeSource{Path: "/"},
				},
			}},
		},
	}
}

// CreateNodeDebug launches a node-debug pod and waits for it to start, returning
// the namespace and generated name so the UI can attach and, importantly, clean
// up afterwards. We return the ns/name even when the wait fails so the caller can
// still delete the pod it just created (leaking a privileged host pod would be
// worse than the wait error itself); the wait error is still returned so the UI
// knows the shell isn't ready.
func CreateNodeDebug(ctx context.Context, cs kubernetes.Interface, node, image string) (namespace, pod string, err error) {
	return createNodeDebug(ctx, cs, node, image, nodeWaitTimeout)
}

// createNodeDebug is CreateNodeDebug with an injectable wait deadline so the
// timeout and terminal-failure branches are testable without burning the real
// two minutes.
//
// The cleanup rule is the subtle part. On a TERMINAL failure (the container
// died, or the caller's context was cancelled) the pod is useless and we delete
// it. On a plain timeout we deliberately leave it: a slow image pull still
// resolves, and the operator can attach to it once it does — deleting there
// would turn "slow" into "destroyed". Either way the real namespace and name are
// returned so the caller retains a handle on what was created.
func createNodeDebug(ctx context.Context, cs kubernetes.Interface, node, image string, timeout time.Duration) (namespace, pod string, err error) {
	created, cerr := cs.CoreV1().Pods("default").Create(ctx, NodeDebugPod(node, image), metav1.CreateOptions{})
	if cerr != nil {
		return "", "", fmt.Errorf("create node debug pod for %q: %w", node, cerr)
	}
	ns, name := created.Namespace, created.Name

	werr := WaitPodContainerRunning(ctx, cs, ns, name, debugContainerBaseName, timeout)
	if werr == nil {
		return ns, name, nil
	}
	if !errors.Is(werr, ErrWaitTimeout) {
		if derr := DeletePod(ctx, cs, ns, name); derr != nil {
			return ns, name, fmt.Errorf("%w (cleanup also failed: %v)", werr, derr)
		}
	}
	return ns, name, werr
}

// DeletePod removes a debug pod. It detaches from the caller's context so a
// cancelled or timed-out parent still gets its cleanup through — the whole point
// is to run when things have already gone wrong. Callers use it for the
// after-the-shell-exits cleanup too.
func DeletePod(ctx context.Context, cs kubernetes.Interface, namespace, pod string) error {
	ctx, cancel := detachCtx(ctx)
	defer cancel()
	if err := cs.CoreV1().Pods(namespace).Delete(ctx, pod, metav1.DeleteOptions{}); err != nil {
		return fmt.Errorf("delete debug pod %s/%s: %w", namespace, pod, err)
	}
	return nil
}

// detachCtx returns a context that survives its parent's cancellation but is
// still bounded, so cleanup cannot hang forever.
func detachCtx(parent context.Context) (context.Context, context.CancelFunc) {
	return context.WithTimeout(context.WithoutCancel(parent), deleteTimeout)
}
