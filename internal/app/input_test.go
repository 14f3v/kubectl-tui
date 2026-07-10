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
