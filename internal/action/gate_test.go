package action

import (
	"sync"
	"testing"
)

func TestGateSingleOwner(t *testing.T) {
	g := NewTerminalGate()
	if g.State() != TUIOwned {
		t.Fatal("initial state should be TUIOwned")
	}
	if !g.Acquire() {
		t.Fatal("first acquire should succeed")
	}
	if g.State() != ChildOwned {
		t.Fatal("state should be ChildOwned after acquire")
	}
	if g.Acquire() {
		t.Fatal("second acquire must be rejected while child owns the terminal")
	}
	g.Release()
	if g.State() != TUIOwned {
		t.Fatal("state should be TUIOwned after release")
	}
	if !g.Acquire() {
		t.Fatal("acquire should succeed again after release")
	}
}

func TestGateConcurrentAcquireExactlyOne(t *testing.T) {
	g := NewTerminalGate()
	const n = 50
	var wg sync.WaitGroup
	var mu sync.Mutex
	wins := 0
	wg.Add(n)
	for i := 0; i < n; i++ {
		go func() {
			defer wg.Done()
			if g.Acquire() {
				mu.Lock()
				wins++
				mu.Unlock()
			}
		}()
	}
	wg.Wait()
	if wins != 1 {
		t.Fatalf("exactly one acquire should win, got %d", wins)
	}
}
