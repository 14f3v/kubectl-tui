package app

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// stubPage records the keys routed to it so tests can assert what the root model
// forwarded and what it consumed.
type stubPage struct {
	phase   engine.Phase
	gotKeys []string
}

func (p *stubPage) Init() tea.Cmd { return nil }

func (p *stubPage) Update(m tea.Msg) (view.Page, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok {
		p.gotKeys = append(p.gotKeys, k.String())
	}
	return p, nil
}

func (p *stubPage) View(int, int) string { return "" }
func (p *stubPage) Keys() []key.Binding  { return nil }
func (p *stubPage) Title() string        { return "stub" }
func (p *stubPage) Kind() string         { return "pods" }
func (p *stubPage) Namespace() string    { return "" }
func (p *stubPage) SetFilter(string)     {}
func (p *stubPage) Filter() string       { return "" }
func (p *stubPage) OnEnter() tea.Cmd     { return nil }
func (p *stubPage) OnLeave()             {}

func (p *stubPage) Summary() view.Summary {
	s := view.Summary{Phase: p.phase}
	if p.phase == engine.PhaseTerminal {
		s.Error = &engine.EngineErr{Class: engine.ClassAuth, Detail: "creds expired"}
	}
	return s
}

func TestBannerSwallowsPageKeys(t *testing.T) {
	// While a terminal-error banner is up the page is not rendered at all, but the
	// root still forwarded every unmatched key to it — including the destructive
	// ones, acting on last-good rows the user cannot see.
	p := &stubPage{phase: engine.PhaseTerminal}
	m := &Model{pages: []view.Page{p}}

	m.handleKey(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl}) // delete
	m.handleKey(tea.KeyPressMsg{Code: 'k', Mod: tea.ModCtrl}) // force kill
	m.handleKey(tea.KeyPressMsg{Code: 'e', Text: "e"})        // edit

	if len(p.gotKeys) != 0 {
		t.Fatalf("keys reached the page behind an error banner: %v", p.gotKeys)
	}
}

func TestHealthyPageStillReceivesKeys(t *testing.T) {
	// The swallow must be scoped to the banner; a healthy page keeps its keymap.
	p := &stubPage{phase: engine.PhaseReady}
	m := &Model{pages: []view.Page{p}}

	m.handleKey(tea.KeyPressMsg{Code: 'e', Text: "e"})

	if len(p.gotKeys) != 1 || p.gotKeys[0] != "e" {
		t.Fatalf("healthy page received %v, want [e]", p.gotKeys)
	}
}

func TestRetryKeyIsConsumedByTheBanner(t *testing.T) {
	// The banner tells the user to press "r" to retry. That key was never wired:
	// it fell through to the page, where it opens the rollout menu.
	p := &stubPage{phase: engine.PhaseTerminal}
	m := &Model{pages: []view.Page{p}}

	for _, k := range []tea.KeyPressMsg{
		{Code: 'r', Text: "r"},
		{Code: 'R', Text: "R"},
	} {
		m.handleKey(k)
	}

	if len(p.gotKeys) != 0 {
		t.Fatalf("retry key routed to the page instead of retrying: %v", p.gotKeys)
	}
}

func TestBannerUpOnlyForTerminalPhase(t *testing.T) {
	for _, tc := range []struct {
		phase engine.Phase
		want  bool
	}{
		{engine.PhaseLoading, false},
		{engine.PhaseReady, false},
		{engine.PhaseStale, false}, // stale keeps rendering rows; it is not a banner
		{engine.PhaseTerminal, true},
	} {
		m := &Model{pages: []view.Page{&stubPage{phase: tc.phase}}}
		if got := m.bannerUp(); got != tc.want {
			t.Errorf("bannerUp() with phase %v = %v, want %v", tc.phase, got, tc.want)
		}
	}
}
