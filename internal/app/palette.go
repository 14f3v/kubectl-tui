package app

import (
	"sort"
	"strings"

	"github.com/14f3v/kubectl-tui/internal/view"
)

// appCommands are the `:` verbs the view registry doesn't know about (they are
// handled directly by runCommand rather than being pages).
var appCommands = []view.Command{
	{Name: "ctx", Desc: "switch kube-context (:ctx opens a picker)"},
	{Name: "crds", Aliases: []string{"crd"}, Desc: "browse CustomResourceDefinitions and custom resources"},
	{Name: "apply", Aliases: []string{"create"}, Desc: "apply YAML from $EDITOR (any kind, incl. CRDs)"},
	{Name: "q", Aliases: []string{"quit", "exit"}, Desc: "quit kubetui"},
}

// commandCatalog is every command the palette can offer: the registered pages
// plus the app-only verbs, sorted by name.
func commandCatalog() []view.Command {
	cat := append(view.Commands(), appCommands...)
	sort.Slice(cat, func(i, j int) bool { return cat[i].Name < cat[j].Name })
	return cat
}

// matchCommands filters the catalog by token (the buffer up to the first space,
// lowercased). Matches are commands whose name or any alias contains the token,
// with prefix matches ordered before mid-string matches. An empty token returns
// the whole catalog.
func matchCommands(catalog []view.Command, token string) []view.Command {
	token = strings.ToLower(strings.TrimSpace(token))
	if token == "" {
		return catalog
	}
	var prefix, contains []view.Command
	for _, c := range catalog {
		switch matchRank(c, token) {
		case 1:
			prefix = append(prefix, c)
		case 2:
			contains = append(contains, c)
		}
	}
	return append(prefix, contains...)
}

// matchRank returns 1 for a prefix match, 2 for a substring match, 0 for none.
func matchRank(c view.Command, token string) int {
	best := 0
	consider := func(s string) {
		s = strings.ToLower(s)
		if strings.HasPrefix(s, token) {
			best = 1
		} else if best == 0 && strings.Contains(s, token) {
			best = 2
		}
	}
	consider(c.Name)
	for _, a := range c.Aliases {
		if best == 1 {
			break
		}
		consider(a)
	}
	return best
}

// commandMatches returns the catalog filtered by the current command-line buffer.
func (m *Model) commandMatches() []view.Command {
	token := m.inputBuf
	if i := strings.IndexByte(token, ' '); i >= 0 {
		token = token[:i]
	}
	return matchCommands(commandCatalog(), token)
}

// selectedCommand returns the palette command Enter would run for the current
// buffer and selection. ok is false when the buffer is an argument form (contains
// a space) or nothing matches — in those cases the caller parses the raw buffer.
// It reads m.inputBuf, so it must be called BEFORE the buffer is cleared.
func (m *Model) selectedCommand() (name string, ok bool) {
	if strings.Contains(m.inputBuf, " ") {
		return "", false
	}
	matches := m.commandMatches()
	if m.cmdSel < 0 || m.cmdSel >= len(matches) {
		return "", false
	}
	return matches[m.cmdSel].Name, true
}

// shortAlias returns the shortest alias distinct from the name, for a compact
// hint in the palette (e.g. "portforwards (pf)").
func shortAlias(c view.Command) string {
	best := ""
	for _, a := range c.Aliases {
		if a == c.Name {
			continue
		}
		if best == "" || len(a) < len(best) {
			best = a
		}
	}
	return best
}
