package app

import (
	"strings"
	"testing"
	"unicode/utf8"

	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/14f3v/kubectl-tui/internal/style"
)

// parseCommand drives every ":" entry and had no test at all.
func TestParseCommand(t *testing.T) {
	cases := []struct {
		in   string
		want command
	}{
		{"", command{verb: "noop"}},
		{"   ", command{verb: "noop"}},
		{"pods", command{verb: "nav", kind: "pods"}},
		{"pods kube-system", command{verb: "nav", kind: "pods", namespace: "kube-system"}},
		{"PODS", command{verb: "nav", kind: "pods"}}, // the head is lowercased
		{"pods  kube-system  extra", command{verb: "nav", kind: "pods", namespace: "kube-system"}},
		{"q", command{verb: "quit"}},
		{"quit", command{verb: "quit"}},
		{"q!", command{verb: "quit"}},
		{"exit", command{verb: "quit"}},
		{"ctx", command{verb: "ctx"}},
		{"context Staging-EU", command{verb: "ctx", arg: "Staging-EU"}}, // case preserved
		{"apply", command{verb: "apply"}},
		{"create", command{verb: "apply"}},
		{"crds", command{verb: "crds"}},
		{"crd", command{verb: "crds"}},
		// Field paths are camelCase, so explain must not lowercase its argument.
		{"explain pod.spec.securityContext", command{verb: "explain", arg: "pod.spec.securityContext"}},
		{"exp Pod", command{verb: "explain", arg: "Pod"}},
		{"explain", command{verb: "explain"}},
		// A dotted head is a fully-qualified resource, not a nav alias.
		{"certificates.cert-manager.io", command{verb: "crdopen", arg: "certificates.cert-manager.io"}},
	}
	for _, c := range cases {
		if got := parseCommand(c.in); got != c.want {
			t.Errorf("parseCommand(%q) = %+v, want %+v", c.in, got, c.want)
		}
	}
}

func TestPadRightIsDisplayWidthAware(t *testing.T) {
	// padRight sits in the same layouts as fitLine, which is x/ansi-based. Measuring
	// with len() makes them disagree the moment a name is not pure ASCII, and
	// docs/ARCHITECTURE.md states the rule outright: width math never uses len().
	for _, s := range []string{"abc", "日本語", "café"} {
		got := padRight(s, 10)
		if w := lipgloss.Width(got); w != 10 {
			t.Errorf("padRight(%q, 10) width = %d, want 10", s, w)
		}
	}
	if got := padRight("abcdefghij", 4); got != "abcdefghij" {
		t.Errorf("padRight over-width = %q, want unchanged", got)
	}
}

func TestLayoutHelpers(t *testing.T) {
	for _, w := range []int{1, 5, 20, 80} {
		if got := lipgloss.Width(fitLine("a very long line indeed that overflows", w)); got > w {
			t.Errorf("fitLine width %d > %d", got, w)
		}
		out := spread("left", "right", w)
		if got := lipgloss.Width(out); got > w {
			t.Errorf("spread(%d) width = %d, want <= %d", w, got, w)
		}
		if !utf8.ValidString(out) {
			t.Errorf("spread(%d) emitted invalid UTF-8", w)
		}
	}
	// fitBlock forces exactly the requested number of lines, which is the contract
	// renderContent relies on to keep the footer pinned.
	for _, h := range []int{1, 3, 10} {
		got := strings.Count(fitBlock("one\ntwo", 20, h), "\n") + 1
		if got != h {
			t.Errorf("fitBlock(h=%d) produced %d lines", h, got)
		}
	}
}

// The chrome must survive any terminal size before a Session exists — this is the
// path every launch goes through, and a panic here shows the panic screen instead
// of the UI.
func TestRenderSurvivesEverySizeWhileBooting(t *testing.T) {
	for _, m := range []*Model{
		{theme: style.Default(), booting: true},
		{theme: style.Default(), fatal: errFake{}},
		{theme: style.Default(), panicInfo: "boom"},
	} {
		for _, s := range [][2]int{{0, 0}, {1, 1}, {5, 2}, {40, 10}, {200, 60}} {
			m.width, m.height = s[0], s[1]
			func() {
				defer func() {
					if r := recover(); r != nil {
						t.Errorf("render at %dx%d panicked: %v", s[0], s[1], r)
					}
				}()
				if out := m.render(); !utf8.ValidString(out) {
					t.Errorf("render at %dx%d emitted invalid UTF-8", s[0], s[1])
				}
			}()
		}
	}
}

type errFake struct{}

func (errFake) Error() string { return "no kubeconfig" }

// Modal precedence: a prompt or confirm dialog must swallow input, and the panic
// screen must respond to nothing but quit and dismiss.
func TestModalKeyPrecedence(t *testing.T) {
	key := func(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Text: string(r)} }

	m := &Model{theme: style.Default(), prompt: &promptState{buf: ""}}
	m.handleKey(key('x'))
	if m.prompt == nil || m.prompt.buf != "x" {
		t.Error("a prompt did not capture input")
	}

	// A confirm gates destructive actions, so only an explicit yes may run it and
	// any other key must cancel — a stray keypress must never delete anything.
	ran := false
	m = &Model{theme: style.Default(), confirm: &confirmState{action: func() tea.Msg { ran = true; return nil }}}
	m.handleKey(key('x'))
	if m.confirm != nil {
		t.Error("an unrelated key left the confirm dialog up")
	}
	if ran {
		t.Error("an unrelated key CONFIRMED a destructive action")
	}
	for _, yes := range []rune{'y', 'Y'} {
		ran = false
		m = &Model{theme: style.Default(), confirm: &confirmState{action: func() tea.Msg { ran = true; return nil }}}
		_, cmd := m.handleKey(key(yes))
		if cmd == nil {
			t.Fatalf("%q did not confirm", yes)
		}
		cmd()
		if !ran {
			t.Errorf("%q did not run the action", yes)
		}
	}

	m = &Model{theme: style.Default(), panicInfo: "boom"}
	m.handleKey(key('x'))
	if m.panicInfo == "" {
		t.Error("the panic screen was dismissed by an unrelated key")
	}
	m.handleKey(key('r'))
	if m.panicInfo != "" {
		t.Error("r did not dismiss the panic screen")
	}
}
