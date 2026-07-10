package view

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/apidisco"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func init() {
	Register("api-resources", []string{"apiresources", "api-resources"}, "served API resources", func(d Deps) Page {
		return &apiResourcesPage{sess: d.Session, theme: d.Theme}
	})
	Register("api-versions", []string{"apiversions", "api-versions"}, "served API group/versions", func(d Deps) Page {
		return &apiVersionsPage{sess: d.Session, theme: d.Theme}
	})
}

// apiListKeys are the shared bindings for the two discovery list pages.
func apiListKeys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// clampCursor keeps c within [0, n-1] (or 0 when the list is empty).
func clampCursor(c, n int) int {
	if c >= n {
		c = n - 1
	}
	if c < 0 {
		c = 0
	}
	return c
}

// -- api-resources ----------------------------------------------------------

// apiResourcesPage lists every served resource (kubectl api-resources): name,
// short names, apiVersion, namespaced, kind. It fetches once on entry and on `r`.
type apiResourcesPage struct {
	sess    *k8s.Session
	theme   style.Theme
	rows    []apidisco.APIResource
	cursor  int
	loaded  bool
	errText string
}

type apiResMsg struct {
	rows []apidisco.APIResource
	err  string
}

func (p *apiResourcesPage) Init() tea.Cmd       { return p.refresh() }
func (p *apiResourcesPage) OnEnter() tea.Cmd    { return p.refresh() }
func (p *apiResourcesPage) Title() string       { return "api-resources" }
func (p *apiResourcesPage) Kind() string        { return "" }
func (p *apiResourcesPage) Namespace() string   { return "" }
func (p *apiResourcesPage) Filter() string      { return "" }
func (p *apiResourcesPage) SetFilter(string)    {}
func (p *apiResourcesPage) OnLeave()            {}
func (p *apiResourcesPage) Summary() Summary    { return Summary{Total: len(p.rows)} }
func (p *apiResourcesPage) Keys() []key.Binding { return apiListKeys() }

func (p *apiResourcesPage) refresh() tea.Cmd {
	sess := p.sess
	return func() tea.Msg {
		rows, err := apidisco.Resources(sess.Disco)
		if err != nil && len(rows) == 0 {
			return apiResMsg{err: err.Error()}
		}
		return apiResMsg{rows: rows}
	}
}

func (p *apiResourcesPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch msg := m.(type) {
	case apiResMsg:
		p.rows, p.errText, p.loaded = msg.rows, msg.err, true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "j", "down":
			if p.cursor < len(p.rows)-1 {
				p.cursor++
			}
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
			}
		case "r":
			return p, p.refresh()
		}
	}
	return p, nil
}

func (p *apiResourcesPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  "+pad("NAME", 34)+pad("SHORTNAMES", 14)+pad("APIVERSION", 26)+pad("NAMESPACED", 12)+"KIND") + "\n")
	if !p.loaded {
		return b.String() + "\n" + t.Faint.Render("  loading API resources…")
	}
	if p.errText != "" {
		return b.String() + "\n" + t.Faint.Render("  "+p.errText)
	}
	p.cursor = clampCursor(p.cursor, len(p.rows))
	for i, r := range p.rows {
		marker, nameStyle := "  ", t.Dim
		if i == p.cursor {
			marker, nameStyle = t.AccentText.Render("▶ "), t.Base
		}
		b.WriteString(marker +
			nameStyle.Render(pad(r.Name, 34)) +
			t.Dim.Render(pad(dashOr(r.ShortNames), 14)) +
			t.Dim.Render(pad(r.APIVersion, 26)) +
			t.Dim.Render(pad(strconv.FormatBool(r.Namespaced), 12)) +
			t.Dim.Render(r.Kind) + "\n")
	}
	return b.String() + "\n" + t.Faint.Render("  r refresh · esc back")
}

// -- api-versions -----------------------------------------------------------

// apiVersionsPage lists served group/versions (kubectl api-versions), marking
// each group's preferred version.
type apiVersionsPage struct {
	sess    *k8s.Session
	theme   style.Theme
	rows    []apidisco.APIGroupVersion
	cursor  int
	loaded  bool
	errText string
}

type apiVerMsg struct {
	rows []apidisco.APIGroupVersion
	err  string
}

func (p *apiVersionsPage) Init() tea.Cmd       { return p.refresh() }
func (p *apiVersionsPage) OnEnter() tea.Cmd    { return p.refresh() }
func (p *apiVersionsPage) Title() string       { return "api-versions" }
func (p *apiVersionsPage) Kind() string        { return "" }
func (p *apiVersionsPage) Namespace() string   { return "" }
func (p *apiVersionsPage) Filter() string      { return "" }
func (p *apiVersionsPage) SetFilter(string)    {}
func (p *apiVersionsPage) OnLeave()            {}
func (p *apiVersionsPage) Summary() Summary    { return Summary{Total: len(p.rows)} }
func (p *apiVersionsPage) Keys() []key.Binding { return apiListKeys() }

func (p *apiVersionsPage) refresh() tea.Cmd {
	sess := p.sess
	return func() tea.Msg {
		rows, err := apidisco.Versions(sess.Disco)
		if err != nil && len(rows) == 0 {
			return apiVerMsg{err: err.Error()}
		}
		return apiVerMsg{rows: rows}
	}
}

func (p *apiVersionsPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch msg := m.(type) {
	case apiVerMsg:
		p.rows, p.errText, p.loaded = msg.rows, msg.err, true
	case tea.KeyPressMsg:
		switch msg.String() {
		case "j", "down":
			if p.cursor < len(p.rows)-1 {
				p.cursor++
			}
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
			}
		case "r":
			return p, p.refresh()
		}
	}
	return p, nil
}

func (p *apiVersionsPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  "+pad("GROUP", 40)+pad("VERSION", 16)+"PREFERRED") + "\n")
	if !p.loaded {
		return b.String() + "\n" + t.Faint.Render("  loading API versions…")
	}
	if p.errText != "" {
		return b.String() + "\n" + t.Faint.Render("  "+p.errText)
	}
	p.cursor = clampCursor(p.cursor, len(p.rows))
	for i, r := range p.rows {
		marker, nameStyle := "  ", t.Dim
		if i == p.cursor {
			marker, nameStyle = t.AccentText.Render("▶ "), t.Base
		}
		preferred := ""
		if r.Preferred {
			preferred = "✓"
		}
		b.WriteString(marker +
			nameStyle.Render(pad(dashOr(r.Group), 40)) +
			t.Dim.Render(pad(r.Version, 16)) +
			t.Dim.Render(preferred) + "\n")
	}
	return b.String() + "\n" + t.Faint.Render("  r refresh · esc back")
}
