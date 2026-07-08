// Package view holds the pages: one per resource kind plus the command line and
// registry. A Page is a self-contained Bubble Tea sub-model that renders its
// content area; the root model owns the surrounding chrome and routes messages
// to the active page. Pages are the only components that speak tea.Msg.
package view

import (
	"sort"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// Summary is the data the root chrome needs from the active page: the status
// tallies for the header count line, the data phase and error for staleness and
// forbidden banners, and the last sync time for the command bar clock.
type Summary struct {
	Total, OK, Warn, Err int
	Phase                engine.Phase
	Error                *engine.EngineErr
	SyncedAt             time.Time
}

// Deps are what every page needs to exist: the active Session, the resolved
// theme, the namespace scope ("" = all namespaces), and whether mutating actions
// are disabled. The root model supplies them when it builds a page.
type Deps struct {
	Session   *k8s.Session
	Theme     style.Theme
	Namespace string
	ReadOnly  bool
	TierLabel string // tenant label key for the TIER column
}

// Page is a routed sub-model. The root calls OnEnter when it becomes active and
// OnLeave when it is replaced; Update/View/Keys drive it in between.
type Page interface {
	// Init returns a command to run when the page is first created.
	Init() tea.Cmd
	// Update handles a message and returns the (possibly new) page and a command.
	Update(msg tea.Msg) (Page, tea.Cmd)
	// View renders the page's content area at the given size (chrome excluded).
	View(width, height int) string
	// Keys returns the context keybindings, shown in the header grid and footer.
	Keys() []key.Binding
	// Title is the page's display name, e.g. "pods".
	Title() string
	// Kind is the engine kind key this page watches, or "" if none.
	Kind() string
	// Namespace is the current namespace scope ("" = all namespaces).
	Namespace() string
	// SetFilter applies a live filter string from the root command line. An empty
	// string clears the filter. Pages without filtering may no-op.
	SetFilter(string)
	// Filter returns the active filter string, for the breadcrumb.
	Filter() string
	// Summary returns the counts, phase, and sync time the chrome renders.
	Summary() Summary
	// OnEnter starts any informers/timers the page needs.
	OnEnter() tea.Cmd
	// OnLeave stops screen-scoped informers/timers.
	OnLeave()
}

// Factory builds a page from its dependencies.
type Factory func(Deps) Page

var (
	factories   = map[string]Factory{}
	aliasToKind = map[string]string{}
)

// Register wires a kind and its command aliases to a factory. Called from page
// files' init() so adding a page is one new file.
func Register(kind string, aliases []string, f Factory) {
	factories[kind] = f
	aliasToKind[kind] = kind
	for _, a := range aliases {
		aliasToKind[a] = kind
	}
}

// ResolveKind maps a command alias (e.g. "po") to its canonical kind ("pods").
func ResolveKind(alias string) (string, bool) {
	k, ok := aliasToKind[alias]
	return k, ok
}

// NewPage builds a page for a kind, or ok=false if the kind is unregistered.
func NewPage(kind string, deps Deps) (Page, bool) {
	f, ok := factories[kind]
	if !ok {
		return nil, false
	}
	return f(deps), true
}

// Aliases returns every registered command alias, sorted, for tab-completion.
func Aliases() []string {
	out := make([]string, 0, len(aliasToKind))
	for a := range aliasToKind {
		out = append(out, a)
	}
	sort.Strings(out)
	return out
}
