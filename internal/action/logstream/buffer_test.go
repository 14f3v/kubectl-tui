package logstream

import (
	"strconv"
	"sync"
	"testing"
)

func TestBufferDrainAll(t *testing.T) {
	b := newBuffer(10)
	for i := 0; i < 5; i++ {
		b.append("line" + strconv.Itoa(i))
	}
	lines, dropped := b.drain()
	if len(lines) != 5 || dropped != 0 {
		t.Fatalf("drain = %d lines, %d dropped; want 5/0", len(lines), dropped)
	}
	// Second drain is empty.
	lines, dropped = b.drain()
	if lines != nil || dropped != 0 {
		t.Fatalf("second drain not empty: %d/%d", len(lines), dropped)
	}
}

func TestBufferDropsOldestWhenFull(t *testing.T) {
	b := newBuffer(10)
	for i := 0; i < 25; i++ {
		b.append(strconv.Itoa(i))
	}
	lines, dropped := b.drain()
	if len(lines) != 10 {
		t.Fatalf("retained %d lines, want 10 (cap)", len(lines))
	}
	if dropped != 15 {
		t.Fatalf("dropped = %d, want 15", dropped)
	}
	// The retained lines must be the newest ones (15..24).
	if lines[0] != "15" || lines[9] != "24" {
		t.Fatalf("retained window = %s..%s, want 15..24", lines[0], lines[9])
	}
	// Dropped counter resets after drain.
	b.append("x")
	if _, d := b.drain(); d != 0 {
		t.Fatalf("dropped not reset after drain: %d", d)
	}
}

// TestBufferConcurrent ensures append and drain are safe under the race detector,
// mirroring the reader goroutine writing while the flusher drains.
func TestBufferConcurrent(t *testing.T) {
	b := newBuffer(DefaultCap)
	var wg sync.WaitGroup
	wg.Add(1)
	go func() {
		defer wg.Done()
		for i := 0; i < 10000; i++ {
			b.append(strconv.Itoa(i))
		}
	}()
	total := 0
	for i := 0; i < 200; i++ {
		lines, dropped := b.drain()
		total += len(lines) + dropped
	}
	wg.Wait()
	lines, dropped := b.drain()
	total += len(lines) + dropped
	if total != 10000 {
		t.Fatalf("accounted for %d lines, want 10000 (delivered + dropped)", total)
	}
}
