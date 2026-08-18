package app

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/view"
)

// leafPage is an ordinary page with no self-refresh timer.
type leafPage struct{ entered, left int }

func (leafPage) Init() tea.Cmd                          { return nil }
func (p *leafPage) Update(tea.Msg) (view.Page, tea.Cmd) { return p, nil }
func (leafPage) View(int, int) string                   { return "" }
func (leafPage) Keys() []key.Binding                    { return nil }
func (leafPage) Title() string                          { return "leaf" }
func (leafPage) Kind() string                           { return "" }
func (leafPage) Namespace() string                      { return "" }
func (leafPage) SetFilter(string)                       {}
func (leafPage) Filter() string                         { return "" }
func (leafPage) Summary() view.Summary                  { return view.Summary{} }
func (p *leafPage) OnEnter() tea.Cmd                    { p.entered++; return nil }
func (p *leafPage) OnLeave()                            { p.left++ }

// tickingPage drives its own refresh timer, so it needs re-arming when a drill-in
// on top of it is popped.
type tickingPage struct {
	leafPage
	resumed int
}

func (p *tickingPage) OnResume() tea.Cmd {
	p.resumed++
	return func() tea.Msg { return nil }
}

func TestPopResumesASelfRefreshingPage(t *testing.T) {
	parent := &tickingPage{}
	m := &Model{pages: []view.Page{parent}}

	// Drill in, then back out — the shape of pressing y for YAML and then esc.
	child := &leafPage{}
	m.pushPage(child)
	if parent.resumed != 0 {
		t.Fatalf("parent resumed while a child was pushed")
	}
	_, cmd := m.popPage()

	if parent.resumed != 1 {
		t.Errorf("parent OnResume called %d times, want 1 — its refresh tick stays dead otherwise", parent.resumed)
	}
	if cmd == nil {
		t.Error("popPage returned no command, so the re-armed tick is never scheduled")
	}
	if child.left != 1 {
		t.Errorf("child OnLeave called %d times, want 1", child.left)
	}
	// The parent must not be re-entered: OnEnter has side effects some pages
	// cannot repeat (logsPage would start a second stream).
	if parent.entered != 0 {
		t.Errorf("parent OnEnter called %d times on pop, want 0", parent.entered)
	}
}

func TestPopLeavesOrdinaryPagesAlone(t *testing.T) {
	parent := &leafPage{}
	m := &Model{pages: []view.Page{parent}}
	m.pushPage(&leafPage{})
	if _, cmd := m.popPage(); cmd != nil {
		t.Errorf("popPage issued a command for a page with no refresh timer")
	}
	if parent.entered != 0 {
		t.Errorf("parent OnEnter called %d times, want 0", parent.entered)
	}
}
