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

// waitPollInterval and waitTimeout bound WaitRunning's polling. We poll rather
// than watch because the caller just created the container and only needs a
// short, bounded wait for it to come up; a full watch would be heavier than the
// interaction warrants.
const (
	waitPollInterval = 500 * time.Millisecond
	waitTimeout      = 30 * time.Second
)

// WaitRunning blocks until the named ephemeral container on a pod reaches the
// Running state, or reports a friendly error if it terminates first or the wait
// times out. It polls the pod every ~500ms for up to ~30s and inspects the
// container's EphemeralContainerStatuses: Running means the UI can attach;
// Terminated means the image exited (surfaced with its reason so the operator
// knows why); a timeout means the container never scheduled or pulled in time.
// Context cancellation returns promptly with the context's error.
func WaitRunning(ctx context.Context, cs kubernetes.Interface, namespace, pod, container string) error {
	deadline := time.Now().Add(waitTimeout)
	for {
		p, err := cs.CoreV1().Pods(namespace).Get(ctx, pod, metav1.GetOptions{})
		if err != nil {
			return fmt.Errorf("get pod %s/%s: %w", namespace, pod, err)
		}
		for _, st := range p.Status.EphemeralContainerStatuses {
			if st.Name != container {
				continue
			}
			if st.State.Running != nil {
				return nil
			}
			if t := st.State.Terminated; t != nil {
				reason := t.Reason
				if reason == "" {
					reason = fmt.Sprintf("exit code %d", t.ExitCode)
				}
				return fmt.Errorf("debug container %q terminated: %s", container, reason)
			}
		}

		if time.Now().After(deadline) {
			return fmt.Errorf("timed out waiting for debug container %q to start", container)
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
	created, err := cs.CoreV1().Pods("default").Create(ctx, NodeDebugPod(node, image), metav1.CreateOptions{})
	if err != nil {
		return "", "", fmt.Errorf("create node debug pod for %q: %w", node, err)
	}
	ns, name := created.Namespace, created.Name
	if err := WaitRunning(ctx, cs, ns, name, "debugger"); err != nil {
		return ns, name, err
	}
	return ns, name, nil
}
