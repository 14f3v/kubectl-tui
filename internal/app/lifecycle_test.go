package app

import (
	"testing"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/config"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// lifePage records every lifecycle call so the stack's contract can be asserted
// rather than inferred.
type lifePage struct {
	name            string
	entered, left   int
	inits, updates  int
	filter          string
	replaceOnUpdate view.Page // when set, Update installs this instead of itself
	lastKey         string
}

func (p *lifePage) Init() tea.Cmd { p.inits++; return nil }
func (p *lifePage) Update(m tea.Msg) (view.Page, tea.Cmd) {
	p.updates++
	if k, ok := m.(tea.KeyPressMsg); ok {
		p.lastKey = k.String()
	}
	if p.replaceOnUpdate != nil {
		return p.replaceOnUpdate, nil
	}
	return p, nil
}
func (p *lifePage) View(int, int) string  { return "" }
func (p *lifePage) Keys() []key.Binding   { return nil }
func (p *lifePage) Title() string         { return p.name }
func (p *lifePage) Kind() string          { return "pods" }
func (p *lifePage) Namespace() string     { return "" }
func (p *lifePage) SetFilter(f string)    { p.filter = f }
func (p *lifePage) Filter() string        { return p.filter }
func (p *lifePage) Summary() view.Summary { return view.Summary{} }
func (p *lifePage) OnEnter() tea.Cmd      { p.entered++; return nil }
func (p *lifePage) OnLeave()              { p.left++ }

func lifeModel(pages ...view.Page) *Model {
	return &Model{theme: style.Default(), pages: pages}
}

// A Session with just an Engine is enough to exercise navigation: navigate only
// requires it to be non-nil, and the pages it builds report an error through a
// toast rather than panicking when no factory is registered.
func navModel(t *testing.T, favs []string) *Model {
	t.Helper()
	sess := &k8s.Session{Engine: engine.NewEngine(t.Context(), func(tea.Msg) {})}
	return &Model{
		theme: style.Default(),
		sess:  sess,
		cfg:   Config{Config: config.Config{Favorites: favs}},
	}
}

func TestPushEntersOnlyTheNewPage(t *testing.T) {
	parent, child := &lifePage{name: "parent"}, &lifePage{name: "child"}
	m := lifeModel(parent)

	m.pushPage(child)

	if len(m.pages) != 2 || m.active() != child {
		t.Fatalf("stack = %d pages, active = %v", len(m.pages), m.active())
	}
	if child.inits != 1 || child.entered != 1 {
		t.Errorf("child init/enter = %d/%d, want 1/1", child.inits, child.entered)
	}
	// The parent stays on the stack and is NOT left — it is buried, not closed, so
	// its informers and streams keep running underneath.
	if parent.left != 0 || parent.entered != 0 {
		t.Errorf("parent was disturbed by a push: entered=%d left=%d", parent.entered, parent.left)
	}
	// A push also drops any open command line, so the drill-in is not hidden by it.
	m.mode, m.inputBuf = modeCommand, "pods"
	m.pushPage(&lifePage{name: "third"})
	if m.mode != modeNone || m.inputBuf != "" {
		t.Errorf("push left the command line open: mode=%v buf=%q", m.mode, m.inputBuf)
	}
}

func TestPopLeavesOnlyThePoppedPage(t *testing.T) {
	parent, child := &lifePage{name: "parent"}, &lifePage{name: "child"}
	m := lifeModel(parent, child)

	m.popPage()

	if len(m.pages) != 1 || m.active() != parent {
		t.Fatalf("stack = %d pages, active = %v", len(m.pages), m.active())
	}
	if child.left != 1 {
		t.Errorf("popped page OnLeave = %d, want 1", child.left)
	}
	if parent.left != 0 {
		t.Errorf("revealed page was left as well: %d", parent.left)
	}
}

func TestBasePageIsNeverPopped(t *testing.T) {
	base := &lifePage{name: "base"}
	m := lifeModel(base)

	m.popPage()
	m.popPage()

	if len(m.pages) != 1 || m.active() != base {
		t.Fatalf("base page was popped: %d pages left", len(m.pages))
	}
	if base.left != 0 {
		t.Errorf("base page was left %d times, want 0 — it is still on screen", base.left)
	}
}

func TestNavigateReplacesTheWholeStack(t *testing.T) {
	m := navModel(t, nil)
	a, b := &lifePage{name: "a"}, &lifePage{name: "b"}
	m.pages = []view.Page{a, b}

	m.navigate("pods", "kube-system")

	if len(m.pages) != 1 {
		t.Fatalf("navigate left %d pages, want 1", len(m.pages))
	}
	// Every page on the old stack must be left, not just the top — otherwise a
	// buried page keeps its informers running with nothing to render them.
	if a.left != 1 || b.left != 1 {
		t.Errorf("OnLeave counts a=%d b=%d, want 1/1", a.left, b.left)
	}
	if m.active().Namespace() != "kube-system" {
		t.Errorf("new page namespace = %q, want kube-system", m.active().Namespace())
	}
}

func TestNavigateToAnUnknownKindKeepsTheStack(t *testing.T) {
	m := navModel(t, nil)
	base := &lifePage{name: "base"}
	m.pages = []view.Page{base}

	_, cmd := m.navigate("not-a-kind", "")

	if len(m.pages) != 1 || m.active() != base {
		t.Error("a failed navigation disturbed the stack")
	}
	if base.left != 0 {
		t.Error("a failed navigation left the current page")
	}
	if cmd == nil {
		t.Fatal("no feedback for an unknown kind")
	}
	if toast, ok := cmd().(msg.Toast); !ok || toast.Level != msg.LevelError {
		t.Errorf("unknown kind produced %v, want an error toast", cmd())
	}
}

func TestRouteToPageOnlyReachesTheTop(t *testing.T) {
	buried, top := &lifePage{name: "buried"}, &lifePage{name: "top"}
	m := lifeModel(buried, top)

	m.routeToPage(tea.KeyPressMsg{Code: 'x', Text: "x"})

	if top.updates != 1 || top.lastKey != "x" {
		t.Errorf("top page updates=%d lastKey=%q, want 1/x", top.updates, top.lastKey)
	}
	if buried.updates != 0 {
		t.Errorf("a buried page received %d updates; only the top page is live", buried.updates)
	}
}

func TestRouteToPageInstallsTheReturnedPage(t *testing.T) {
	successor := &lifePage{name: "successor"}
	original := &lifePage{name: "original", replaceOnUpdate: successor}
	m := lifeModel(original)

	m.routeToPage(tea.KeyPressMsg{Code: 'x', Text: "x"})

	if m.active() != successor {
		t.Error("a page that returned a successor was not installed on the stack")
	}
	if len(m.pages) != 1 {
		t.Errorf("stack grew to %d; a replacement is not a push", len(m.pages))
	}
}

func TestJumpNamespace(t *testing.T) {
	m := navModel(t, []string{"demo", "prod"})
	m.pages = []view.Page{&lifePage{name: "base"}}

	// 0 is all-namespaces.
	m.jumpNamespace("0")
	if got := m.active().Namespace(); got != "" {
		t.Errorf(`after "0" namespace = %q, want "" (all)`, got)
	}
	// 1..n select configured favourites.
	m.jumpNamespace("2")
	if got := m.active().Namespace(); got != "prod" {
		t.Errorf(`after "2" namespace = %q, want prod`, got)
	}
	// A slot with no favourite explains itself instead of navigating nowhere.
	before := m.active()
	_, cmd := m.jumpNamespace("9")
	if m.active() != before {
		t.Error("an empty favourite slot navigated anyway")
	}
	if cmd == nil {
		t.Fatal("an empty favourite slot gave no feedback")
	}
	if _, ok := cmd().(msg.Toast); !ok {
		t.Errorf("empty slot produced %T, want a toast", cmd())
	}
}

// esc is a global: it pops a drill-in if there is one, and otherwise clears the
// active filter. It must never reach the page.
func TestEscPopsBeforeClearingTheFilter(t *testing.T) {
	parent := &lifePage{name: "parent", filter: "web"}
	child := &lifePage{name: "child"}
	m := lifeModel(parent, child)

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if len(m.pages) != 1 {
		t.Fatalf("esc did not pop the drill-in: %d pages", len(m.pages))
	}
	if parent.filter != "web" {
		t.Error("esc cleared the filter while a drill-in was open; it should only pop")
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if parent.filter != "" {
		t.Errorf("esc at the base did not clear the filter, got %q", parent.filter)
	}
	if child.updates != 0 || parent.updates != 0 {
		t.Error("esc was forwarded to a page; it is a global key")
	}
}
