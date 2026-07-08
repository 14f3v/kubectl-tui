// Package engine is the data plane. One ViewStore per resource kind wraps a
// client-go informer, coalesces its watch events, and emits a single
// pre-projected snapshot per burst through a sink (in production, tea.Program.Send).
//
// The engine imports Bubble Tea only for the tea.Msg type used by the sink; it
// performs no rendering and is fully testable headless with a fake clientset.
package engine

import (
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"
)

// Phase is the lifecycle state of a view's data.
type Phase int

const (
	// PhaseLoading is the initial list; no data has arrived yet.
	PhaseLoading Phase = iota
	// PhaseReady means the watch is healthy and rows are current.
	PhaseReady
	// PhaseStale means a retryable failure occurred; the last-good rows are
	// still rendered while the reflector reconnects.
	PhaseStale
	// PhaseTerminal means a non-retryable failure (auth/forbidden/TLS); the
	// informer has stopped and the error is surfaced.
	PhaseTerminal
)

// String renders a phase for logs and tests.
func (p Phase) String() string {
	switch p {
	case PhaseLoading:
		return "loading"
	case PhaseReady:
		return "ready"
	case PhaseStale:
		return "stale"
	case PhaseTerminal:
		return "terminal"
	}
	return "unknown"
}

// Remote is the async-state envelope for a view. The core invariant, borrowed
// from the sibling app: Rows is never cleared on failure — a stale or terminal
// phase keeps the last successful rows so the UI never blanks on a network blip.
type Remote[T any] struct {
	Phase    Phase
	Rows     []T
	Err      *EngineErr
	SyncedAt time.Time
}

// Sink receives engine snapshots. In production it is tea.Program.Send; in tests
// it is a channel writer or slice appender.
type Sink = func(tea.Msg)

// SnapshotMsg is the engine's sole output: one coalesced, pre-projected snapshot
// for one kind. ViewID lets the root model drop snapshots for a view the user
// has already navigated away from.
type SnapshotMsg struct {
	Kind   string
	ViewID uint64
	Snap   Remote[columns.Row]
}
