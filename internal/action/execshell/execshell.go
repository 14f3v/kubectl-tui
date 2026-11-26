// Package execshell provides an interactive exec shell into a pod as a Bubble Tea
// ExecCommand. It hands the terminal to the pod using the WebSocket→SPDY fallback
// executor, raw-terminal mode, and SIGWINCH-driven resize — the plumbing proven
// by hack/spike-tty. The TerminalGate serializes handoffs; this type performs one.
package execshell

import (
	"context"
	"errors"
	"fmt"
	"io"
	"net/url"
	"os"
	"path/filepath"
	"runtime"
	"runtime/debug"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
	"k8s.io/apimachinery/pkg/util/httpstream"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/kubernetes/scheme"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/remotecommand"
	"k8s.io/kubectl/pkg/util/interrupt"
	"k8s.io/kubectl/pkg/util/term"
)

// defaultShell prefers bash but reliably falls back to sh. It must NOT use
// "exec bash || exec sh": a failed exec terminates the shell before the ||, so on
// a bash-less image (Alpine/busybox) that yields exit 127 instead of falling
// back. Testing with `command -v` avoids that. The outer "sh" is resolved via the
// container's PATH so it works even when the shell is not at /bin/sh.
var defaultShell = []string{
	"sh", "-c",
	"export TERM=${TERM:-xterm-256color}; if command -v bash >/dev/null 2>&1; then exec bash; else exec sh; fi",
}

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

// Run performs the interactive exec, blocking until the shell exits. A pod exec
// hands the whole terminal to a remote process over the network; a panic in the
// resize plumbing, transport, or auth-plugin path must never take down the parent
// TUI (Bubble Tea would print a stack trace and exit). Any panic in this
// goroutine is recovered into an error and the stack is persisted for diagnosis.
func (c *Cmd) Run() (retErr error) {
	defer func() {
		if r := recover(); r != nil {
			retErr = fmt.Errorf("shell crashed: %v (details written to %s)", r, saveCrashStack(c, r))
		}
	}()

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

	// While the stream runs, ctx is cancelled only by a termination signal that
	// interruptParent turns into the end of the session (see sessionErr).
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()

	// TryDev lets Safe fall back to /dev/tty for raw mode when the passed input is
	// not itself a terminal fd. Only wrap a non-nil size queue: MonitorSize returns
	// nil when the output is not a terminal, and sizeBridge{nil}.Next() — polled
	// from a goroutine inside remotecommand — would otherwise panic.
	tty := term.TTY{In: in, Out: out, Raw: true, TryDev: true, Parent: interruptParent(runtime.GOOS, cancel)}
	var sizeQueue remotecommand.TerminalSizeQueue
	if tty.IsTerminalIn() {
		if q := tty.MonitorSize(tty.GetSize()); q != nil {
			sizeQueue = sizeBridge{q}
		}
	}

	// The TUI has already released the terminal (cooked mode), so it still shows the
	// old scrollback under a blank gap while the exec stream dials the cluster. Give
	// the shell a fresh screen: clear the visible area and home the cursor (like
	// `clear`, but WITHOUT the \x1b[3J that would also wipe the user's pre-kubetui
	// scrollback), then print a connecting line so the handoff reads as intentional.
	// It scrolls away under the shell's first output. (No "exited" line: Bubble Tea
	// re-enters the alt-screen the moment Run returns and would wipe it instantly.)
	target := c.pod
	if c.container != "" {
		target = c.pod + "/" + c.container
	}
	// On Windows the console Bubble Tea handed back may not interpret escapes at
	// all, so turn VT output on before the first one is written, and put the
	// console's own mode back when the shell is done.
	restoreOutput := enableVTOutput(out)
	defer restoreOutput()
	fmt.Fprintf(out, "\x1b[2J\x1b[H⏳ connecting to %s/%s…"+bannerNewline, c.namespace, target)

	err = tty.Safe(func() error {
		// Safe has made the console raw by now; on Windows that raw mode still
		// drops the non-character keys until VT input is added on top of it.
		enableVTInput(in)
		// Not in itself: the read client-go leaves parked when the stream returns
		// must be stopped before Bubble Tea takes the terminal back, and on
		// Windows a console read would turn Ctrl+Z into EOF (see stdin.go).
		// TTY.In stays in, so Safe still sees the console.
		stdin := newSessionStdin(in)
		defer stdin.stop()
		return exec.StreamWithContext(ctx, remotecommand.StreamOptions{
			Stdin:             stdin,
			Stdout:            out,
			Tty:               true,
			TerminalSizeQueue: sizeQueue,
		})
	})
	// A program in the pod that turned mouse reporting on (vim with mouse=a, htop,
	// mc) turns it off on a clean exit, but not when the session is cut short by
	// Ctrl+Break, a dropped connection or a killed pod. Bubble Tea only resets the
	// mouse modes it set itself, and kubetui sets none, so switch them off here,
	// while VT output is still on. Otherwise text selection stops working in the
	// TUI and the user's own shell gets mouse escapes after kubetui exits.
	fmt.Fprint(out, mouseReportingOff)
	return sessionErr(ctx, err)
}

// mouseReportingOff resets X10/normal (1000), button-event (1002) and any-event
// (1003) tracking and SGR encoding (1006).
const mouseReportingOff = "\x1b[?1000l\x1b[?1002l\x1b[?1003l\x1b[?1006l"

// sessionErr turns what the stream returned into the error Run reports.
func sessionErr(ctx context.Context, err error) error {
	if err == nil {
		return nil
	}
	// The stream hands back ctx's own error only when interruptParent cancelled
	// it under a live session. ctx.Err() alone cannot tell: on Windows Safe's
	// handler also runs cancel as every session ends, once the stream has
	// returned (see interruptParent). Otherwise the toast reads "session ended:
	// context canceled".
	if ctx.Err() != nil && errors.Is(err, context.Canceled) {
		return errors.New("shell interrupted (Ctrl+Break)")
	}
	// Exit 127 means the container runtime could not find a shell at all — the
	// image has no sh (distroless/scratch/static binary). Make that actionable.
	if strings.Contains(err.Error(), "exit code 127") {
		return fmt.Errorf("no shell in this container — it may be a minimal/distroless image (%w)", err)
	}
	return err
}

// saveCrashStack appends the recovered panic and its stack to a temp log file and
// returns the path (or a short note if it could not be written) so the operator
// can share it. Kept out of the terminal, which the failing exec may have left in
// an odd state.
func saveCrashStack(c *Cmd, r any) string {
	path := filepath.Join(os.TempDir(), "kubetui-shell-crash.log")
	f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0o600)
	if err != nil {
		return "stack unavailable: " + err.Error()
	}
	defer f.Close()
	fmt.Fprintf(f, "\n=== %s exec %s/%s container=%q ===\npanic: %v\n%s\n",
		time.Now().UTC().Format(time.RFC3339), c.namespace, c.pod, c.container, r, debug.Stack())
	return path
}

// interruptParent decides what a termination signal does while the pod owns the
// terminal. With no Parent, tty.Safe restores the terminal and calls os.Exit(1)
// (kubectl util/interrupt). On unix that is the answer to a hangup or a kill,
// and raw mode keeps ^C from being a signal at all, so it stays that way there.
//
// On Windows the signal is usually a keystroke. Ctrl+Break arrives as a console
// control event even in raw mode, and Go reports it as SIGINT, as it reports the
// console closing as SIGTERM (runtime os_windows.go ctrlHandler). Exiting there
// took kubetui and every port-forward down with the shell, and skipped the
// deletion of a node-debug pod, which holds the host root. So on Windows the
// signal cancels the stream instead: Run returns, Bubble Tea takes the console
// back and the caller's cleanup runs. A closing console still ends the process
// shortly after (Go parks the control handler to give signal handlers that
// time), but the cleanup now gets that time rather than none.
//
// cancel has to be one of the Parent's notify funcs. Safe builds
// Chain(parent, restore), whose notify list ends with parent.Close and whose
// final is parent.Signal; Close spends the parent's once, so when
// parent.Signal runs it does nothing. cancel is the final as well, so no
// ordering can ever fall through to the os.Exit default. Being a notify func
// also means cancel runs when a session ends normally (Run's deferred Close),
// after the stream has returned, so a cancelled ctx is not by itself a sign
// that the user interrupted the shell (see sessionErr).
func interruptParent(goos string, cancel context.CancelFunc) *interrupt.Handler {
	if goos != "windows" {
		return nil
	}
	return interrupt.New(func(os.Signal) { cancel() }, cancel)
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

// Next is polled from a goroutine inside remotecommand, outside Run's recover, so
// it guards itself: a nil inner queue or a panic in it stops resizing (returns
// nil) rather than crashing the whole program.
func (b sizeBridge) Next() (ts *remotecommand.TerminalSize) {
	defer func() {
		if recover() != nil {
			ts = nil
		}
	}()
	if b.q == nil {
		return nil
	}
	s := b.q.Next()
	if s == nil {
		return nil
	}
	return &remotecommand.TerminalSize{Width: s.Width, Height: s.Height}
}
