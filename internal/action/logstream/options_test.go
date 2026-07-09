package logstream

import (
	"os"
	"path/filepath"
	"testing"
)

// TestPodLogOptions verifies that Options render into the PodLogOptions we expect:
// the zero value reproduces the historical defaults (follow, 500-line tail,
// timestamps on, not previous); Previous flips off Follow and drops SinceSeconds;
// an explicit TailLines is carried through; and SinceSeconds survives for a live
// stream.
func TestPodLogOptions(t *testing.T) {
	// Default Options{} must reproduce the pre-options behavior exactly.
	def := Options{}.podLogOptions("c1")
	if def.Container != "c1" {
		t.Fatalf("default Container = %q, want %q", def.Container, "c1")
	}
	if !def.Timestamps {
		t.Fatalf("default Timestamps = false, want true")
	}
	if !def.Follow {
		t.Fatalf("default Follow = false, want true")
	}
	if def.Previous {
		t.Fatalf("default Previous = true, want false")
	}
	if def.TailLines == nil {
		t.Fatalf("default TailLines is nil, want non-nil pointer to 500")
	}
	if *def.TailLines != 500 {
		t.Fatalf("default *TailLines = %d, want 500", *def.TailLines)
	}
	if def.SinceSeconds != nil {
		t.Fatalf("default SinceSeconds = %v, want nil", *def.SinceSeconds)
	}

	// Previous logs cannot be followed; SinceSeconds is dropped for them.
	since := int64(120)
	prev := Options{Previous: true, SinceSeconds: &since}.podLogOptions("c1")
	if prev.Follow {
		t.Fatalf("previous Follow = true, want false (a terminated container cannot be followed)")
	}
	if !prev.Previous {
		t.Fatalf("previous Previous = false, want true")
	}
	if prev.SinceSeconds != nil {
		t.Fatalf("previous SinceSeconds = %v, want nil (ignored for previous logs)", *prev.SinceSeconds)
	}

	// An explicit tail is carried through verbatim.
	tail := Options{TailLines: 10}.podLogOptions("c1")
	if tail.TailLines == nil || *tail.TailLines != 10 {
		t.Fatalf("TailLines=10 -> *TailLines = %v, want 10", tail.TailLines)
	}

	// SinceSeconds is preserved for a live (non-previous) stream.
	live := Options{SinceSeconds: &since}.podLogOptions("c1")
	if live.SinceSeconds == nil || *live.SinceSeconds != 120 {
		t.Fatalf("SinceSeconds -> %v, want 120 carried through", live.SinceSeconds)
	}
	if !live.Follow {
		t.Fatalf("live Follow = false, want true")
	}
}

// TestSaveLines writes lines to a temp file and reads them back, asserting the
// exact on-disk form (join with \n plus a trailing newline).
func TestSaveLines(t *testing.T) {
	path := filepath.Join(t.TempDir(), "logs.txt")
	lines := []string{"line one", "line two", "line three"}

	if err := SaveLines(path, lines); err != nil {
		t.Fatalf("SaveLines: unexpected error: %v", err)
	}

	got, err := os.ReadFile(path)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	want := "line one\nline two\nline three\n"
	if string(got) != want {
		t.Fatalf("SaveLines wrote %q, want %q", string(got), want)
	}

	// Perm bits are part of the contract (0o644).
	info, err := os.Stat(path)
	if err != nil {
		t.Fatalf("Stat: %v", err)
	}
	if perm := info.Mode().Perm(); perm != 0o644 {
		t.Fatalf("file perm = %o, want 644", perm)
	}
}
