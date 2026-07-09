package execshell

import (
	"strings"
	"testing"

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
