package app

import (
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// pastePage records the pastes routed to it, offers filter suggestions so the
// live-filter side effects can be observed, and can stand behind an error banner.
type pastePage struct {
	lifePage
	pastes []string
	banner bool
}

func (p *pastePage) Update(m tea.Msg) (view.Page, tea.Cmd) {
	if pm, ok := m.(tea.PasteMsg); ok {
		p.pastes = append(p.pastes, pm.Content)
	}
	return p, nil
}

func (p *pastePage) Summary() view.Summary {
	if !p.banner {
		return view.Summary{}
	}
	return view.Summary{Phase: engine.PhaseTerminal, Error: &engine.EngineErr{Class: engine.ClassAuth, Detail: "creds expired"}}
}

func (p *pastePage) FilterMatches(buf string) (string, string, []string) {
	return "", buf, []string{buf + "-0"}
}

func TestSingleLine(t *testing.T) {
	cases := []struct{ name, in, want string }{
		{"plain text", "web-7d9f", "web-7d9f"},
		{"empty", "", ""},
		{"crlf is one space", "a\r\nb", "a b"},
		{"lone cr", "a\rb", "a b"},
		{"lone lf", "a\nb", "a b"},
		{"copied line with its newline", `C:\Users\me\out.log` + "\r\n", `C:\Users\me\out.log `},
		{"tab", "a\tb", "a b"},
		{"escape sequence defused", "\x1b[31mred", "[31mred"},
		{"other controls dropped", "a\x00b\x7fc", "abc"},
		{"quotes and backslashes kept", `"C:\Program Files\x.log"`, `"C:\Program Files\x.log"`},
		{"non-ascii kept", "日本語 ü", "日本語 ü"},
	}
	for _, c := range cases {
		if got := singleLine(c.in); got != c.want {
			t.Errorf("%s: singleLine(%q) = %q, want %q", c.name, c.in, got, c.want)
		}
	}
}

// A paste into an open prompt lands in its buffer like typing would, and its
// newline is flattened rather than taken as enter. The prompt also wins over a
// command line left open underneath it, the same precedence keys get.
func TestPasteFillsThePrompt(t *testing.T) {
	submitted := false
	m := &Model{
		mode: modeFilter,
		prompt: &promptState{
			buf:    "/tmp/",
			errMsg: "no such directory",
			action: func(string) tea.Msg { submitted = true; return nil },
		},
	}

	_, cmd := m.Update(tea.PasteMsg{Content: "pod.log\r\n"})

	if m.prompt == nil || cmd != nil || submitted {
		t.Fatal("a pasted newline submitted the prompt")
	}
	if got := m.prompt.buf; got != "/tmp/pod.log " {
		t.Errorf("prompt buf = %q, want %q", got, "/tmp/pod.log ")
	}
	if m.prompt.errMsg != "" {
		t.Errorf("an edit left the stale validation error %q", m.prompt.errMsg)
	}
	if m.inputBuf != "" {
		t.Errorf("the paste also reached the command line: %q", m.inputBuf)
	}
}

// A paste replaces the value a prompt opened with, as long as nobody has edited
// it: pasting Explorer's "Copy as path" into the log-save prompt must give that
// path, not the default with it stuck on the end. Once the user has typed, a
// paste appends like any other input.
func TestPasteReplacesAnUntouchedPrefill(t *testing.T) {
	m := &Model{}
	m.Update(view.PromptRequest{Title: "Save logs", Label: "Path", Initial: `C:\Temp\web-0.log`})

	m.Update(tea.PasteMsg{Content: `"C:\Users\me\web.log"`})
	if got, want := m.prompt.buf, `"C:\Users\me\web.log"`; got != want {
		t.Fatalf("paste over the prefill: buf = %q, want %q", got, want)
	}
	m.Update(tea.PasteMsg{Content: "x"})
	if got, want := m.prompt.buf, `"C:\Users\me\web.log"x`; got != want {
		t.Errorf("second paste: buf = %q, want %q (appended)", got, want)
	}

	m.Update(view.PromptRequest{Title: "Scale web", Label: "Replicas", Initial: "3"})
	m.Update(tea.KeyPressMsg{Code: tea.KeyBackspace})
	m.Update(tea.PasteMsg{Content: "12"})
	if got, want := m.prompt.buf, "12"; got != want {
		t.Errorf("paste after an edit: buf = %q, want %q", got, want)
	}

	m.Update(view.PromptRequest{Title: "Scale web", Label: "Replicas", Initial: "3"})
	m.Update(tea.KeyPressMsg{Code: '0', Text: "0"})
	m.Update(tea.PasteMsg{Content: "5"})
	if got, want := m.prompt.buf, "305"; got != want {
		t.Errorf("paste after typing: buf = %q, want %q", got, want)
	}
}

// A paste into the "/" filter applies it live and refreshes the suggestions, the
// same side effects as typing each character.
func TestPasteFillsTheFilterLive(t *testing.T) {
	p := &pastePage{lifePage: lifePage{name: "pods"}}
	m := lifeModel(p)
	m.mode, m.inputBuf = modeFilter, "ns:demo "

	m.Update(tea.PasteMsg{Content: "web"})

	if m.inputBuf != "ns:demo web" {
		t.Errorf("filter buf = %q, want %q", m.inputBuf, "ns:demo web")
	}
	if p.filter != "ns:demo web" {
		t.Errorf("page filter = %q, want it applied live", p.filter)
	}
	if len(m.suggest) != 1 || m.suggest[0] != "ns:demo web-0" {
		t.Errorf("suggestions = %v, want them recomputed for the pasted term", m.suggest)
	}
	if len(p.pastes) != 0 {
		t.Error("a paste into the filter was also forwarded to the page")
	}
}

// A multi-line paste into the ":" command bar is flattened onto one line and
// never runs the command; the palette selection resets as it does for typing.
func TestPasteNeverSubmitsTheCommandLine(t *testing.T) {
	m := lifeModel(&pastePage{lifePage: lifePage{name: "pods"}})
	m.mode, m.inputBuf, m.cmdSel = modeCommand, "dep", 2

	m.Update(tea.PasteMsg{Content: "loy\nkube-system"})

	if m.mode != modeCommand {
		t.Fatal("a pasted newline submitted the command line")
	}
	if m.inputBuf != "deploy kube-system" {
		t.Errorf("command buf = %q, want %q", m.inputBuf, "deploy kube-system")
	}
	if m.cmdSel != 0 {
		t.Errorf("palette selection = %d, want it reset to 0", m.cmdSel)
	}
}

// With no text input open, a paste is routed to the page untouched, but only
// where a key would be: never into a confirm dialog, and never to a page that is
// covered by help, an error banner or the panic screen.
func TestPasteIsGatedLikeAKey(t *testing.T) {
	cases := []struct {
		name    string
		setup   func(*Model, *pastePage)
		reaches bool
	}{
		{"no input open", func(*Model, *pastePage) {}, true},
		{"confirm dialog", func(m *Model, _ *pastePage) {
			m.confirm = &confirmState{title: "delete", action: func() tea.Msg { return nil }}
		}, false},
		{"help overlay", func(m *Model, _ *pastePage) { m.showHelp = true }, false},
		{"error banner", func(_ *Model, p *pastePage) { p.banner = true }, false},
		{"panic screen", func(m *Model, _ *pastePage) { m.panicInfo = "boom" }, false},
	}
	for _, c := range cases {
		p := &pastePage{lifePage: lifePage{name: "pods"}}
		m := lifeModel(p)
		c.setup(m, p)
		confirmOpen := m.confirm != nil

		_, cmd := m.Update(tea.PasteMsg{Content: "y\nmore"})

		if c.reaches {
			if len(p.pastes) != 1 || p.pastes[0] != "y\nmore" {
				t.Errorf("%s: page got %q, want the paste as sent", c.name, p.pastes)
			}
			continue
		}
		if len(p.pastes) != 0 {
			t.Errorf("%s: the paste reached the page: %q", c.name, p.pastes)
		}
		if cmd != nil {
			t.Errorf("%s: the paste produced a command", c.name)
		}
		if confirmOpen && m.confirm == nil {
			t.Errorf("%s: a pasted %q answered the dialog", c.name, "y")
		}
	}
}
