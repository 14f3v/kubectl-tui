package execshell

import (
	"context"
	"errors"
	"os"
	"strings"
	"testing"
	"time"

	"k8s.io/kubectl/pkg/util/interrupt"
	"k8s.io/kubectl/pkg/util/term"
)

// TestDefaultShellFallsBackSafely guards the exit-127 regression: a failed `exec`
// terminates the shell before any `||`, so the default must test for bash with
// `command -v` rather than `exec bash || exec sh` (which dead-ends on bash-less
// images like Alpine/busybox).
func TestDefaultShellFallsBackSafely(t *testing.T) {
	script := defaultShell[len(defaultShell)-1]
	if strings.Contains(script, "|| exec") {
		t.Fatalf("default shell relies on a fatal `exec ... || exec` fallback: %q", script)
	}
	if !strings.Contains(script, "command -v bash") {
		t.Fatalf("default shell should probe bash with `command -v` before exec: %q", script)
	}
	if !strings.Contains(script, "exec sh") {
		t.Fatalf("default shell should fall back to `exec sh`: %q", script)
	}
	// The outer interpreter must be PATH-resolved (not an absolute path that may
	// not exist), so a shell anywhere on PATH is found.
	if defaultShell[0] != "sh" {
		t.Fatalf("default shell outer interpreter = %q, want PATH-resolved \"sh\"", defaultShell[0])
	}
}

// staticQueue yields one fixed size then blocks are irrelevant (Next is called
// once per test).
type staticQueue struct{ w, h uint16 }

func (q staticQueue) Next() *term.TerminalSize { return &term.TerminalSize{Width: q.w, Height: q.h} }

// panicQueue models a resize source that faults; remotecommand polls Next from a
// goroutine, so an unguarded panic here would crash the whole program.
type panicQueue struct{}

func (panicQueue) Next() *term.TerminalSize { panic("boom") }

// nilQueue models a stopped monitor.
type nilQueue struct{}

func (nilQueue) Next() *term.TerminalSize { return nil }

func TestSizeBridgePassThrough(t *testing.T) {
	got := sizeBridge{staticQueue{w: 120, h: 40}}.Next()
	if got == nil || got.Width != 120 || got.Height != 40 {
		t.Fatalf("got %+v, want 120x40", got)
	}
}

func TestSizeBridgeRecoversPanic(t *testing.T) {
	// Must not panic; a faulting inner queue stops resizing (nil).
	if got := (sizeBridge{panicQueue{}}).Next(); got != nil {
		t.Fatalf("got %+v, want nil after recovered panic", got)
	}
}

func TestSizeBridgeNilInner(t *testing.T) {
	if got := (sizeBridge{nil}).Next(); got != nil {
		t.Fatalf("got %+v, want nil for nil inner queue", got)
	}
}

func TestSizeBridgeStoppedInner(t *testing.T) {
	if got := (sizeBridge{nilQueue{}}).Next(); got != nil {
		t.Fatalf("got %+v, want nil when inner queue is stopped", got)
	}
}

// TestInterruptParentKeepsUnixExitOnSignal pins that nothing changes off
// Windows: with no Parent, a hangup or kill during a shell still restores the
// terminal and exits, as it always has.
func TestInterruptParentKeepsUnixExitOnSignal(t *testing.T) {
	for _, goos := range []string{"linux", "darwin", "freebsd"} {
		if h := interruptParent(goos, func() {}); h != nil {
			t.Fatalf("%s: got a Parent, which would change what a signal does there", goos)
		}
	}
}

// TestInterruptParentEndsTheSessionOnWindows drives the handler tty.Safe builds
// (kubectl util/term: Chain(Parent, restore).Run(fn)) through a Ctrl+Break. The
// session must end with the terminal restored and the process alive; a wrong
// Parent either never cancels the stream, so this times out, or reaches the
// os.Exit(1) default, which kills the test binary and fails the run.
func TestInterruptParentEndsTheSessionOnWindows(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	restored := false
	h := interrupt.Chain(interruptParent("windows", cancel), func() { restored = true })
	err := h.Run(func() error {
		go h.Signal(os.Interrupt)
		select { // the stream, until its context is cancelled
		case <-ctx.Done():
			return ctx.Err()
		case <-time.After(5 * time.Second):
			return errors.New("the signal never cancelled the stream")
		}
	})
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("Run = %v, want the stream's context.Canceled", err)
	}
	if !restored {
		t.Fatal("the terminal was not restored")
	}
	if got := sessionErr(ctx, err); got == nil || !strings.Contains(got.Error(), "interrupted") {
		t.Fatalf("sessionErr = %v, want the shell reported as interrupted", got)
	}

	// Whatever order a future kubectl calls them in, the Parent alone must not
	// exit either.
	var cancelled bool
	interruptParent("windows", func() { cancelled = true }).Signal(os.Interrupt)
	if !cancelled {
		t.Fatal("Parent.Signal did not cancel the stream")
	}
}

// TestInterruptParentNormalExitIsNotAnInterrupt is the other half. cancel is a
// notify func of the Parent, so on Windows Safe's handler also runs it when a
// session ends on its own (Run's deferred Close), after the stream has
// returned. Run must then report what the stream returned, not "interrupted":
// a shell left with `exit` would otherwise always toast as a Ctrl+Break, and a
// distroless image would lose its "no shell" hint.
func TestInterruptParentNormalExitIsNotAnInterrupt(t *testing.T) {
	exit127 := errors.New("command terminated with exit code 127")
	for name, tc := range map[string]struct {
		stream error
		want   string // substring of the reported error; "" for none
	}{
		"shell exited":    {nil, ""},
		"no shell":        {exit127, "no shell in this container"},
		"other exit code": {errors.New("command terminated with exit code 1"), "exit code 1"},
	} {
		ctx, cancel := context.WithCancel(context.Background())
		h := interrupt.Chain(interruptParent("windows", cancel), func() {})
		err := sessionErr(ctx, h.Run(func() error { return tc.stream }))
		if ctx.Err() == nil {
			t.Fatalf("%s: ctx was not cancelled on a normal exit; this test no longer covers that case", name)
		}
		switch {
		case tc.want == "" && err != nil:
			t.Fatalf("%s: Run reports %v, want nil", name, err)
		case tc.want != "" && (err == nil || !strings.Contains(err.Error(), tc.want)):
			t.Fatalf("%s: Run reports %v, want an error containing %q", name, err, tc.want)
		case err != nil && strings.Contains(err.Error(), "interrupted"):
			t.Fatalf("%s: Run reports %v for a session nobody interrupted", name, err)
		}
		cancel()
	}
}
