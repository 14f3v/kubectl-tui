package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"
)

// TestInputAcceptsSpace guards the regression where a space keypress
// (String() == "space", not " ") was rejected by the character-insert path, so
// the command/filter line could not accept spaces — breaking multi-term filters
// and "kind namespace" commands. Insertion keys off k.Text, which is " " for
// space and "" for non-text keys.
func TestInputAcceptsSpace(t *testing.T) {
	m := &Model{mode: modeCommand, inputBuf: "pods"}

	// Space inserts a literal space.
	m.handleInputKey(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	if m.inputBuf != "pods " {
		t.Fatalf("space not inserted: inputBuf = %q, want %q", m.inputBuf, "pods ")
	}

	// A normal printable key still inserts.
	m.handleInputKey(tea.KeyPressMsg{Code: 'x', Text: "x"})
	if m.inputBuf != "pods x" {
		t.Fatalf("char insert broken: inputBuf = %q, want %q", m.inputBuf, "pods x")
	}

	// A non-text key (arrow) inserts nothing.
	m.handleInputKey(tea.KeyPressMsg{Code: tea.KeyLeft})
	if m.inputBuf != "pods x" {
		t.Fatalf("non-text key inserted text: inputBuf = %q", m.inputBuf)
	}
}

// TestPromptAcceptsSpace guards the same fix on the modal prompt input, which
// needs spaces for e.g. cp's "localPath remotePath".
func TestPromptAcceptsSpace(t *testing.T) {
	m := &Model{prompt: &promptState{buf: "a"}}
	m.handlePromptKey(tea.KeyPressMsg{Code: tea.KeySpace, Text: " "})
	m.handlePromptKey(tea.KeyPressMsg{Code: 'b', Text: "b"})
	if m.prompt == nil || m.prompt.buf != "a b" {
		t.Fatalf("prompt space insert broken: buf = %q, want %q", promptBuf(m), "a b")
	}
}

func promptBuf(m *Model) string {
	if m.prompt == nil {
		return "<nil>"
	}
	return m.prompt.buf
}

// TestSelectSuggest exercises filter suggestion navigation: down/Tab advance and
// wrap, up wraps backwards, the buffer is rebuilt from prefix + value, and an
// empty list is a no-op.
func TestSelectSuggest(t *testing.T) {
	m := &Model{suggest: []string{"alpha", "alpine", "bravo"}, suggestPrefix: "prod ", suggestSel: -1}

	// Down from no selection highlights the first and fills the buffer.
	m.selectSuggest(1)
	if m.suggestSel != 0 || m.inputBuf != "prod alpha" {
		t.Fatalf("first down: sel=%d buf=%q", m.suggestSel, m.inputBuf)
	}
	// Advancing steps through and wraps to the start.
	for _, want := range []struct {
		sel int
		buf string
	}{{1, "prod alpine"}, {2, "prod bravo"}, {0, "prod alpha"}} {
		m.selectSuggest(1)
		if m.suggestSel != want.sel || m.inputBuf != want.buf {
			t.Fatalf("advance: sel=%d buf=%q, want %d/%q", m.suggestSel, m.inputBuf, want.sel, want.buf)
		}
	}
	// Up from the first wraps to the last.
	m.selectSuggest(-1)
	if m.suggestSel != 2 || m.inputBuf != "prod bravo" {
		t.Fatalf("up wrap: sel=%d buf=%q", m.suggestSel, m.inputBuf)
	}

	// Up from no selection highlights the last.
	m2 := &Model{suggest: []string{"a", "b"}, suggestSel: -1}
	m2.selectSuggest(-1)
	if m2.suggestSel != 1 || m2.inputBuf != "b" {
		t.Fatalf("up from none: sel=%d buf=%q", m2.suggestSel, m2.inputBuf)
	}

	// No candidates: navigation is a no-op.
	m3 := &Model{suggest: nil, suggestSel: -1}
	m3.selectSuggest(1)
	if m3.suggestSel != -1 || m3.inputBuf != "" {
		t.Fatalf("empty list: sel=%d buf=%q", m3.suggestSel, m3.inputBuf)
	}
}
