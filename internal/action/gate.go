// Package action holds the TerminalGate plus the child-process action
// subpackages (execshell, editor, portfwd). The gate is the single arbiter for
// terminal ownership: only one child process may hold the TTY at a time, and the
// engine's snapshot emission is paused while it does so.
package action

import "sync"

// GateState is the terminal-ownership state.
type GateState int

const (
	// TUIOwned: the TUI owns the terminal (the normal state).
	TUIOwned GateState = iota
	// ChildOwned: a child process (shell, editor) owns the terminal.
	ChildOwned
)

// TerminalGate serializes TTY handoffs. Acquire succeeds only from TUIOwned;
// concurrent handoff attempts are rejected so two shells can never fight over the
// terminal.
type TerminalGate struct {
	mu    sync.Mutex
	state GateState
}

// NewTerminalGate returns a gate in the TUIOwned state.
func NewTerminalGate() *TerminalGate { return &TerminalGate{} }

// Acquire transitions to ChildOwned and returns true, or returns false if a child
// already owns the terminal.
func (g *TerminalGate) Acquire() bool {
	g.mu.Lock()
	defer g.mu.Unlock()
	if g.state != TUIOwned {
		return false
	}
	g.state = ChildOwned
	return true
}

// Release returns ownership to the TUI. It is safe to call when already TUIOwned.
func (g *TerminalGate) Release() {
	g.mu.Lock()
	defer g.mu.Unlock()
	g.state = TUIOwned
}

// State returns the current ownership state.
func (g *TerminalGate) State() GateState {
	g.mu.Lock()
	defer g.mu.Unlock()
	return g.state
}
