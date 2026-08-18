package app

import (
	"strings"
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// plainPage is a page with no filter completion — the TextView-based pages (yaml,
// describe, logs) look like this, and they do not support alternation.
type plainPage struct{}

func (plainPage) Init() tea.Cmd                         { return nil }
func (p plainPage) Update(tea.Msg) (view.Page, tea.Cmd) { return p, nil }
func (plainPage) View(int, int) string                  { return "" }
func (plainPage) Keys() []key.Binding                   { return nil }
func (plainPage) Title() string                         { return "yaml" }
func (plainPage) Kind() string                          { return "" }
func (plainPage) Namespace() string                     { return "" }
func (plainPage) SetFilter(string)                      {}
func (plainPage) Filter() string                        { return "" }
func (plainPage) Summary() view.Summary                 { return view.Summary{} }
func (plainPage) OnEnter() tea.Cmd                      { return nil }
func (plainPage) OnLeave()                              {}

// tablePage additionally implements FilterCompleter, which is exactly the set of
// pages whose filter supports "col:a|b".
type tablePage struct{ plainPage }

func (tablePage) FilterMatches(string) (string, string, []string) { return "", "", nil }

func commandBar(p view.Page) string {
	m := &Model{theme: style.Default(), width: 200, mode: modeFilter}
	if p != nil {
		m.pages = []view.Page{p}
	}
	return m.renderCommandBar()
}

func TestFilterHintOnlyWhereAlternationWorks(t *testing.T) {
	if got := commandBar(tablePage{}); !strings.Contains(got, "a|b") {
		t.Errorf("table page filter hint omits alternation: %q", got)
	}
	if got := commandBar(plainPage{}); strings.Contains(got, "a|b") {
		t.Errorf("plain page advertises alternation it does not support: %q", got)
	}
	// Both still teach the rest of the grammar.
	for name, p := range map[string]view.Page{"table": tablePage{}, "plain": plainPage{}} {
		if got := commandBar(p); !strings.Contains(got, "terms AND") {
			t.Errorf("%s page lost the base grammar hint: %q", name, got)
		}
	}
	// No active page must not panic.
	_ = commandBar(nil)
}

func TestFilterHelpTextMatchesPageCapability(t *testing.T) {
	if got := filterHelpText(tablePage{}); !strings.Contains(got, "col:a|b") {
		t.Errorf("table page help omits alternation: %q", got)
	}
	if got := filterHelpText(plainPage{}); strings.Contains(got, "col:a|b") {
		t.Errorf("plain page help promises alternation it lacks: %q", got)
	}
	if got := filterHelpText(nil); got == "" {
		t.Error("nil page produced no help text")
	}
}
