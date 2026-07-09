package view

import (
	"testing"

	"github.com/14f3v/kubectl-tui/internal/style"
)

func TestTextViewSubstringFilter(t *testing.T) {
	v := NewTextView("d", "spec:\n  replicas: 3\n  Replicas note\nstatus: ok", style.Default())
	v.height = 10
	v.SetFilter("replicas")

	// Both the lowercase and the capitalized line match (case-insensitive).
	if len(v.matched) != 2 {
		t.Fatalf("matched lines = %v, want 2", v.matched)
	}
	// The match span covers exactly the needle.
	got := v.matchRanges("  replicas: 3")
	if len(got) != 1 || got[0] != [2]int{2, 10} {
		t.Fatalf("ranges = %v, want [[2 10]]", got)
	}
	if len(v.matchRanges("  Replicas note")) != 1 {
		t.Fatal("case-insensitive substring did not match a capitalized occurrence")
	}
	// Multiple occurrences on one line.
	v.SetFilter("a")
	if got := v.matchRanges("aXaXa"); len(got) != 3 {
		t.Fatalf("multi-occurrence ranges = %v, want 3", got)
	}
	// No match.
	v.SetFilter("zzz")
	if len(v.matched) != 0 || v.matchRanges("nothing here") != nil {
		t.Fatalf("expected no matches; matched=%v", v.matched)
	}
	// Clearing the filter resets state.
	v.SetFilter("")
	if v.needle != "" || v.re != nil || len(v.matched) != 0 {
		t.Fatal("empty filter should clear match state")
	}
}

func TestTextViewRegexFilter(t *testing.T) {
	v := NewTextView("d", "replicas: 3\nreplicaSet: x\nother: y", style.Default())
	v.height = 10
	v.SetFilter("~replica[a-z]*")

	if v.re == nil {
		t.Fatal("~ prefix should compile a regex")
	}
	if len(v.matched) != 2 {
		t.Fatalf("regex matched lines = %v, want 2", v.matched)
	}
	// Case-insensitive: "replicaSet" matches "replica[a-z]*".
	if got := v.matchRanges("replicaSet: x"); len(got) != 1 || got[0] != [2]int{0, 10} {
		t.Fatalf("regex ranges = %v, want [[0 10]]", got)
	}
	// An unparseable regex matches nothing rather than erroring.
	v.SetFilter("~[unclosed")
	if v.re != nil || len(v.matched) != 0 {
		t.Fatalf("invalid regex should match nothing; matched=%v", v.matched)
	}
}

func TestTextViewHighlightWraps(t *testing.T) {
	v := NewTextView("d", "replicas: 3", style.Default())
	v.height = 10
	v.SetFilter("replicas")
	// The highlighted line must still contain the text and be longer than the raw
	// line (ANSI styling was inserted around the match).
	out := v.highlight("replicas: 3", true)
	if len(out) <= len("replicas: 3") {
		t.Fatalf("highlight did not wrap the match: %q", out)
	}
	// A line with no match is returned unchanged.
	if out := v.highlight("status: ok", false); out != "status: ok" {
		t.Fatalf("non-matching line changed: %q", out)
	}
}
