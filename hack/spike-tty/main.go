// Command spike-tty de-risks the interactive exec shell: the WebSocket→SPDY
// fallback executor, raw-terminal handoff, and SIGWINCH-driven resize. It shells
// into a pod and hands the terminal over, exactly as the real exec action will,
// so we can prove the handoff and restore work across terminals (iTerm2, tmux,
// Terminal.app) and against a Teleport-proxied cluster before building the
// TerminalGate on top.
//
//	go run ./hack/spike-tty -context <ctx> -n <ns> -pod <pod> [-c <container>] [-- /bin/sh]
package main

import (
	"context"
	"flag"
	"fmt"
	"net/url"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/clientcmd"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/util/term"

	_ "k8s.io/client-go/plugin/pkg/client/auth"
)

func main() {
	ctxName := flag.String("context", "", "kubeconfig context (default: current-context)")
	ns := flag.String("n", "default", "namespace")
	pod := flag.String("pod", "", "pod name (required)")
	container := flag.String("c", "", "container (default: first)")
	flag.Parse()

	if *pod == "" {
		fmt.Fprintln(os.Stderr, "spike-tty: -pod is required")
		os.Exit(2)
	}
	cmd := flag.Args()
	if len(cmd) == 0 {
		cmd = []string{"/bin/sh", "-c", "TERM=xterm-256color /bin/bash || /bin/sh"}
	}

	restCfg, cs := mustClients(*ctxName)

	req := cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(*ns).
		Name(*pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: *container,
			Command:   cmd,
			Stdin:     true,
			Stdout:    true,
			Stderr:    false, // merged into stdout under a TTY
			TTY:       true,
		}, scheme.ParameterCodec)

	exec, err := newFallbackExecutor(restCfg, req.URL())
	if err != nil {
		fmt.Fprintln(os.Stderr, "build executor:", err)
		os.Exit(1)
	}

	tty := term.TTY{In: os.Stdin, Out: os.Stdout, Raw: true}
	if !tty.IsTerminalIn() {
		fmt.Fprintln(os.Stderr, "spike-tty: stdin is not a terminal; run this in a real terminal")
		os.Exit(1)
	}
	sizeQueue := tty.MonitorSize(tty.GetSize())

	fmt.Printf("→ handing terminal to %s/%s (exit the shell to return)\n", *ns, *pod)
	fn := func() error {
		return exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdin:             os.Stdin,
			Stdout:            os.Stdout,
			Tty:               true,
			TerminalSizeQueue: sizeBridge{sizeQueue},
		})
	}
	if err := tty.Safe(fn); err != nil {
		fmt.Fprintln(os.Stderr, "\nexec stream:", err)
		os.Exit(1)
	}
	fmt.Println("\n✓ terminal restored — tty spike passed")
}

// sizeBridge adapts term.TerminalSizeQueue to remotecommand.TerminalSizeQueue;
// the two packages define identical-but-distinct TerminalSize types.
type sizeBridge struct{ q term.TerminalSizeQueue }

func (b sizeBridge) Next() *remotecommand.TerminalSize {
	ts := b.q.Next()
	if ts == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: ts.Width, Height: ts.Height}
}

// newFallbackExecutor builds a WebSocket-primary, SPDY-secondary executor,
// mirroring kubectl.
func newFallbackExecutor(cfg *rest.Config, u *url.URL) (remotecommand.Executor, error) {
	ws, err := remotecommand.NewWebSocketExecutor(cfg, "GET", u.String())
	if err != nil {
		return nil, err
	}
	spdy, err := remotecommand.NewSPDYExecutor(cfg, "POST", u)
	if err != nil {
		return nil, err
	}
	return remotecommand.NewFallbackExecutor(ws, spdy, func(err error) bool {
		return httpstream.IsUpgradeFailure(err) || httpstream.IsHTTPSProxyError(err)
	})
}

func mustClients(ctxName string) (*rest.Config, kubernetes.Interface) {
	rules := clientcmd.NewDefaultClientConfigLoadingRules()
	overrides := &clientcmd.ConfigOverrides{}
	if ctxName != "" {
		overrides.CurrentContext = ctxName
	}
	restCfg, err := clientcmd.NewNonInteractiveDeferredLoadingClientConfig(rules, overrides).ClientConfig()
	if err != nil {
		fmt.Fprintln(os.Stderr, "kubeconfig:", err)
		os.Exit(1)
	}
	cs, err := kubernetes.NewForConfig(restCfg)
	if err != nil {
		fmt.Fprintln(os.Stderr, "clientset:", err)
		os.Exit(1)
	}
	return restCfg, cs
}
