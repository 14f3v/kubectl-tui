// Package logstream streams a pod's logs into the UI without ever blocking the
// reader on the renderer. A reader goroutine appends lines into a bounded buffer;
// a flusher drains the buffer on a fixed cadence and sends one coalesced batch per
// tick. If the producer outruns the buffer, the oldest lines are dropped and the
// count is surfaced, so the UI never silently loses data or stalls.
package logstream

import "sync"

// DefaultCap is the maximum number of undelivered lines held before the oldest
// are dropped. At the flush cadence this only trips under a genuine log flood.
const DefaultCap = 10000

// buffer is the bounded, mutex-guarded hand-off between the reader goroutine and
// the flusher. Append never blocks on the UI; drain hands the accumulated lines
// to the flusher and reports how many were dropped since the last drain.
type buffer struct {
	mu      sync.Mutex
	pending []string
	dropped int
	cap     int
}

func newBuffer(capacity int) *buffer {
	if capacity <= 0 {
		capacity = DefaultCap
	}
	return &buffer{cap: capacity}
}

// append adds a line, evicting the oldest and counting a drop if the buffer is
// full (the flusher has fallen behind the producer).
func (b *buffer) append(line string) {
	b.mu.Lock()
	defer b.mu.Unlock()
	b.pending = append(b.pending, line)
	if len(b.pending) > b.cap {
		over := len(b.pending) - b.cap
		b.pending = b.pending[over:]
		b.dropped += over
	}
}

// drain removes and returns all buffered lines plus the number dropped since the
// previous drain, resetting both.
func (b *buffer) drain() ([]string, int) {
	b.mu.Lock()
	defer b.mu.Unlock()
	if len(b.pending) == 0 && b.dropped == 0 {
		return nil, 0
	}
	lines := b.pending
	dropped := b.dropped
	b.pending = nil
	b.dropped = 0
	return lines, dropped
}
