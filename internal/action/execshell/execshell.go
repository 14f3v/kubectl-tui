// Package execshell provides an interactive exec shell into a pod as a Bubble Tea
// ExecCommand. It hands the terminal to the pod using the WebSocket→SPDY fallback
// executor, raw-terminal mode, and SIGWINCH-driven resize — the plumbing proven
// by hack/spike-tty. The TerminalGate serializes handoffs; this type performs one.
package execshell

import (
	"context"
	"io"
	"net/url"
	"os"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/util/term"
)

// defaultShell tries bash with a sane TERM, falling back to sh.
var defaultShell = []string{"/bin/sh", "-c", "TERM=xterm-256color exec /bin/bash 2>/dev/null || exec /bin/sh"}

// Cmd is a tea.ExecCommand that streams an interactive shell. Bubble Tea releases
// the terminal, wires Set{Stdin,Stdout,Stderr}, calls Run, and restores the
// terminal afterward.
type Cmd struct {
	cfg       *rest.Config
	cs        kubernetes.Interface
	namespace string
	pod       string
	container string
	command   []string

	in  io.Reader
	out io.Writer
	err io.Writer
}

// New builds an exec-shell command. An empty command uses the default shell; an
// empty container targets the pod's first container.
func New(cfg *rest.Config, cs kubernetes.Interface, namespace, pod, container string, command []string) *Cmd {
	if len(command) == 0 {
		command = defaultShell
	}
	return &Cmd{cfg: cfg, cs: cs, namespace: namespace, pod: pod, container: container, command: command}
}

func (c *Cmd) SetStdin(r io.Reader)  { c.in = r }
func (c *Cmd) SetStdout(w io.Writer) { c.out = w }
func (c *Cmd) SetStderr(w io.Writer) { c.err = w }

// Run performs the interactive exec, blocking until the shell exits.
func (c *Cmd) Run() error {
	req := c.cs.CoreV1().RESTClient().Post().
		Resource("pods").
		Namespace(c.namespace).
		Name(c.pod).
		SubResource("exec").
		VersionedParams(&corev1.PodExecOptions{
			Container: c.container,
			Command:   c.command,
			Stdin:     true,
			Stdout:    true,
			Stderr:    false, // merged into stdout under a TTY
			TTY:       true,
		}, scheme.ParameterCodec)

	exec, err := newFallbackExecutor(c.cfg, req.URL())
	if err != nil {
		return err
	}

	in := c.in
	if in == nil {
		in = os.Stdin
	}
	out := c.out
	if out == nil {
		out = os.Stdout
	}

	tty := term.TTY{In: in, Out: out, Raw: true}
	var sizeQueue remotecommand.TerminalSizeQueue
	if tty.IsTerminalIn() {
		sizeQueue = sizeBridge{tty.MonitorSize(tty.GetSize())}
	}

	return tty.Safe(func() error {
		return exec.StreamWithContext(context.Background(), remotecommand.StreamOptions{
			Stdin:             in,
			Stdout:            out,
			Tty:               true,
			TerminalSizeQueue: sizeQueue,
		})
	})
}

// newFallbackExecutor builds a WebSocket-primary, SPDY-secondary executor,
// mirroring kubectl's exec.
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

// sizeBridge adapts term.TerminalSizeQueue to remotecommand.TerminalSizeQueue
// (identical-but-distinct TerminalSize types).
type sizeBridge struct{ q term.TerminalSizeQueue }

func (b sizeBridge) Next() *remotecommand.TerminalSize {
	ts := b.q.Next()
	if ts == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: ts.Width, Height: ts.Height}
}
