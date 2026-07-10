// Package portforward is a pure-backend helper for forwarding local ports to a
// pod, mirroring `kubectl port-forward`. It targets a pod directly, but also
// resolves a Service or workload (Deployment/StatefulSet/...) down to a single
// ready pod so the UI can offer "port-forward to this Service" without the user
// hunting for a backing pod.
//
// Forward is long-lived: it opens SPDY streams to the pod's portforward
// subresource and blocks until the returned stop func is called. There is no
// auto-reconnect here — the caller owns the lifecycle. This package is
// deliberately free of any TUI dependency (no bubbletea/lipgloss); it is pure Go
// plus client-go so it can be unit-tested and driven from any front end.
package portforward

import (
	"context"
	"fmt"
	"io"
	"net/http"
	"strconv"
	"strings"

	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/labels"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"
)

// maxPort is the highest valid TCP port. Ports must be in 1..65535; port 0 is
// "any port" at the OS level but is not a meaningful forward target here, so we
// reject it along with anything above the ceiling.
const maxPort = 65535

// ParsePorts turns free-form user text into the "local:remote" mappings that
// portforward.New expects. It accepts a comma-separated list where each entry is
// either "LOCAL:REMOTE" or a bare "PORT" (bare means local == remote, matching
// `kubectl port-forward`'s shorthand). Each side is validated as an integer in
// 1..65535. The list must be non-empty. Errors are UI-ready messages because the
// string comes straight from a text input the operator typed.
func ParsePorts(s string) ([]string, error) {
	fields := strings.Split(s, ",")
	out := make([]string, 0, len(fields))
	for _, raw := range fields {
		entry := strings.TrimSpace(raw)
		if entry == "" {
			// A stray comma ("8080,,9090") or an all-blank input is a mistake we
			// surface rather than silently dropping.
			return nil, fmt.Errorf("empty port spec")
		}
		local, remote, hasColon := strings.Cut(entry, ":")
		local = strings.TrimSpace(local)
		if !hasColon {
			// Bare form: same port locally and remotely.
			if err := validatePort(local); err != nil {
				return nil, err
			}
			out = append(out, local+":"+local)
			continue
		}
		remote = strings.TrimSpace(remote)
		if err := validatePort(local); err != nil {
			return nil, err
		}
		if err := validatePort(remote); err != nil {
			return nil, err
		}
		out = append(out, local+":"+remote)
	}
	return out, nil
}

// validatePort checks that a single port token is a whole number in 1..65535.
func validatePort(p string) error {
	if p == "" {
		return fmt.Errorf("empty port spec")
	}
	n, err := strconv.Atoi(p)
	if err != nil {
		return fmt.Errorf("port %q must be a number", p)
	}
	if n < 1 || n > maxPort {
		return fmt.Errorf("port %d out of range (1-%d)", n, maxPort)
	}
	return nil
}

// PodForService resolves a Service to a single Running & Ready backing pod. It
// reads the Service, lists pods matching its spec.selector, and returns the name
// of the first ready one. A Service with no selector (e.g. an ExternalName or a
// manually-managed Endpoints Service) or with no ready pods is an error, because
// there is nothing to forward to.
func PodForService(ctx context.Context, cs kubernetes.Interface, ns, svc string) (string, error) {
	s, err := cs.CoreV1().Services(ns).Get(ctx, svc, metav1.GetOptions{})
	if err != nil {
		return "", err
	}
	if len(s.Spec.Selector) == 0 {
		return "", fmt.Errorf("service %q has no pod selector", svc)
	}
	return PodForSelector(ctx, cs, ns, s.Spec.Selector)
}

// PodForSelector returns the name of the first Running & Ready pod matching the
// given label selector in the namespace. It is the shared helper behind
// PodForService, and is equally usable for a workload once the caller has read
// its spec.selector.matchLabels (Deployment/StatefulSet/DaemonSet/ReplicaSet).
// An empty selector is rejected: forwarding to "whatever pod happens to match
// everything" is never what the operator means.
func PodForSelector(ctx context.Context, cs kubernetes.Interface, ns string, selector map[string]string) (string, error) {
	if len(selector) == 0 {
		return "", fmt.Errorf("empty selector")
	}
	sel := labels.Set(selector).AsSelector()
	pods, err := cs.CoreV1().Pods(ns).List(ctx, metav1.ListOptions{LabelSelector: sel.String()})
	if err != nil {
		return "", err
	}
	name, ok := firstReadyPod(pods.Items)
	if !ok {
		return "", fmt.Errorf("no ready pod found for selector %q", sel.String())
	}
	return name, nil
}

// firstReadyPod returns the name of the first Running & Ready pod in the slice.
// The second return is false when none qualify. It is a pure helper so the
// selection rule can be unit-tested without a cluster.
func firstReadyPod(pods []corev1.Pod) (string, bool) {
	for i := range pods {
		if podReady(&pods[i]) {
			return pods[i].Name, true
		}
	}
	return "", false
}

// podReady reports whether a pod is Running and has its PodReady condition set to
// True. Both checks matter: a pod can be in phase Running yet not yet Ready (a
// failing readiness probe), and forwarding to such a pod would connect to a
// process that is not accepting traffic.
func podReady(pod *corev1.Pod) bool {
	if pod.Status.Phase != corev1.PodRunning {
		return false
	}
	for _, c := range pod.Status.Conditions {
		if c.Type == corev1.PodReady {
			return c.Status == corev1.ConditionTrue
		}
	}
	return false
}

// Forward opens the given "local:remote" port mappings to a pod and starts
// forwarding in a background goroutine. It returns immediately with:
//
//   - stop: an idempotent func that tears the forward down (closes the stop
//     channel); safe to call from any goroutine and more than once.
//   - ready: a channel that is closed once the streams are established and the
//     local listeners are accepting connections. The caller can select on it to
//     learn when the forward is live.
//   - err: non-nil only for synchronous setup failures (bad config, bad ports).
//     A failure that happens after ForwardPorts starts is written to out and
//     ends the goroutine; the ready channel will simply never close.
//
// out receives the forwarder's stdout and stderr (bind addresses, per-connection
// errors). Pass io.Discard to silence it. Forward is long-lived and must be
// driven off the UI thread; the UI keeps stop and shows the forward as active
// until it invokes stop.
func Forward(ctx context.Context, cfg *rest.Config, cs kubernetes.Interface, ns, pod string, ports []string, out io.Writer) (stop func(), ready <-chan struct{}, err error) {
	if len(ports) == 0 {
		return nil, nil, fmt.Errorf("no ports to forward")
	}
	if out == nil {
		out = io.Discard
	}

	// Build an SPDY round tripper from the rest config and dial the pod's
	// portforward subresource — this is exactly how `kubectl port-forward`
	// upgrades the connection, so it needs no tooling beyond the kubeconfig.
	rt, upgrader, err := spdy.RoundTripperFor(cfg)
	if err != nil {
		return nil, nil, err
	}
	u := cs.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(ns).Name(pod).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: rt}, "POST", u)

	stopCh := make(chan struct{})
	readyCh := make(chan struct{})
	pf, err := portforward.New(dialer, ports, stopCh, readyCh, out, out)
	if err != nil {
		return nil, nil, err
	}

	// closeOnce guards the stop channel so stop() is idempotent. We cannot use
	// sync.Once directly in the returned closure without capturing it, so a small
	// buffered channel acts as a one-shot latch.
	closed := make(chan struct{}, 1)
	stop = func() {
		select {
		case closed <- struct{}{}:
			close(stopCh)
		default:
			// Already stopped.
		}
	}

	// If the caller's context is cancelled, tear the forward down too so a
	// per-request deadline or a session shutdown propagates cleanly.
	go func() {
		select {
		case <-ctx.Done():
			stop()
		case <-stopCh:
		}
	}()

	// ForwardPorts blocks until stopCh closes or an error occurs; run it in the
	// background and hand the caller back the ready channel to observe liveness.
	go func() {
		if ferr := pf.ForwardPorts(); ferr != nil {
			fmt.Fprintf(out, "port-forward error: %v\n", ferr)
		}
	}()

	return stop, readyCh, nil
}
