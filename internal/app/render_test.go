package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/style"
)

// Mouse capture takes drag-to-select away from the terminal, and nothing in the
// app handles mouse events, so the View must not ask for it. Bracketed paste
// stays on: that is what delivers a paste as one PasteMsg instead of keys, and a
// pasted newline as text instead of enter.
func TestViewLeavesMouseOffAndPasteOn(t *testing.T) {
	m := &Model{theme: style.Default()}
	v := m.View()
	if v.MouseMode != tea.MouseModeNone {
		t.Errorf("MouseMode = %v, want MouseModeNone until mouse events are handled", v.MouseMode)
	}
	if v.DisableBracketedPasteMode {
		t.Error("bracketed paste is disabled; a pasted newline would arrive as enter")
	}
	if !v.AltScreen {
		t.Error("the full-screen UI lost the alt screen")
	}
}
