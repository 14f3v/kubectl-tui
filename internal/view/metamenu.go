package view

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/metaedit"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

type metaItem struct{ label, desc string }

// metaMenuPage is the `L` menu for adding/removing labels and annotations on any
// resource, pushed from a resource row. Each item takes a single-line "key=value"
// (set) or "key-" (remove); the change is a JSON merge patch.
type metaMenuPage struct {
	sess      *k8s.Session
	theme     style.Theme
	kind      string
	namespace string
	name      string
	items     []metaItem
	cursor    int
}

func newMetaMenuPage(sess *k8s.Session, theme style.Theme, kind, namespace, name string) *metaMenuPage {
	return &metaMenuPage{
		sess: sess, theme: theme, kind: kind, namespace: namespace, name: name,
		items: []metaItem{
			{"Label", "key=value to set, key- to remove"},
			{"Annotate", "key=value to set, key- to remove"},
		},
	}
}

func (p *metaMenuPage) Init() tea.Cmd     { return nil }
func (p *metaMenuPage) Title() string     { return p.name + " · metadata" }
func (p *metaMenuPage) Kind() string      { return "" }
func (p *metaMenuPage) Namespace() string { return p.namespace }
func (p *metaMenuPage) Filter() string    { return "" }
func (p *metaMenuPage) SetFilter(string)  {}
func (p *metaMenuPage) OnEnter() tea.Cmd  { return nil }
func (p *metaMenuPage) OnLeave()          {}
func (p *metaMenuPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *metaMenuPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "edit")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *metaMenuPage) Update(m tea.Msg) (Page, tea.Cmd) {
	k, ok := m.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "j", "down":
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter", " ":
		switch p.items[p.cursor].label {
		case "Label":
			return p, p.prompt("Label", false)
		case "Annotate":
			return p, p.prompt("Annotate", true)
		}
	}
	return p, nil
}

// prompt collects "key=value" (set) or "key-" (remove) and applies the patch off
// the UI thread. annotation selects labels vs annotations.
func (p *metaMenuPage) prompt(title string, annotation bool) tea.Cmd {
	name := p.name
	return func() tea.Msg {
		return PromptRequest{
			Title: title + " · " + name,
			Label: "key=value (or key- to remove)",
			Action: func(v string) tea.Msg {
				key, value, remove, err := parseMetaEdit(strings.TrimSpace(v))
				if err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				var body []byte
				if annotation {
					body = metaedit.AnnotationPatch(key, value, remove)
				} else {
					body = metaedit.LabelPatch(key, value, remove)
				}
				info, ok := k8s.ResourceFor(p.kind)
				if !ok {
					return msg.Toast{Text: "not supported for " + p.kind, Level: msg.LevelError}
				}
				if err := metaedit.Apply(p.sess.Context(), p.sess.Dyn, info.GVR, info.Namespaced, p.namespace, p.name, body); err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				return msg.Toast{Text: name + " patched", Level: msg.LevelSuccess}
			},
		}
	}
}

// parseMetaEdit reads "key=value" (set), or "key-" / "key" (remove).
func parseMetaEdit(v string) (key, value string, remove bool, err error) {
	if v == "" {
		return "", "", false, fmt.Errorf("expected key=value or key-")
	}
	if k, val, ok := strings.Cut(v, "="); ok {
		if strings.TrimSpace(k) == "" {
			return "", "", false, fmt.Errorf("empty key")
		}
		return strings.TrimSpace(k), val, false, nil
	}
	return strings.TrimSuffix(v, "-"), "", true, nil
}

func (p *metaMenuPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  METADATA") + "\n")
	for i, it := range p.items {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		b.WriteString(marker +
			nameStyle.Render(pad(it.label, 12)) +
			t.Faint.Render(trunc(it.desc, width-18)) + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  enter edit · esc back"))
	return b.String()
}
