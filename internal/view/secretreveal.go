package view

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// secretItem is one data key of a Secret. The decoded value is held in memory
// only for the lifetime of the page and shown solely when the user reveals it.
type secretItem struct {
	key      string
	value    []byte // already base64-decoded by the typed client
	revealed bool
}

// secretRevealPage lists a Secret's data keys with masked values. Pressing enter
// toggles cleartext reveal for the selected key. Values are never rendered until
// explicitly revealed and are never written to logs or the yaml view (redacted).
type secretRevealPage struct {
	sess      *k8s.Session
	theme     style.Theme
	namespace string
	name      string
	stype     string
	items     []secretItem
	cursor    int
}

// newSecretRevealPage builds the drill-in from a cached Secret object.
func newSecretRevealPage(sess *k8s.Session, theme style.Theme, secret *corev1.Secret) *secretRevealPage {
	keys := make([]string, 0, len(secret.Data))
	for k := range secret.Data {
		keys = append(keys, k)
	}
	sort.Strings(keys)
	items := make([]secretItem, 0, len(keys))
	for _, k := range keys {
		// Copy the value so the shared cache object is never referenced.
		v := append([]byte(nil), secret.Data[k]...)
		items = append(items, secretItem{key: k, value: v})
	}
	return &secretRevealPage{
		sess:      sess,
		theme:     theme,
		namespace: secret.Namespace,
		name:      secret.Name,
		stype:     string(secret.Type),
		items:     items,
	}
}

func (p *secretRevealPage) Init() tea.Cmd     { return nil }
func (p *secretRevealPage) Title() string     { return p.name + " · secret" }
func (p *secretRevealPage) Kind() string      { return "" }
func (p *secretRevealPage) Namespace() string { return p.namespace }
func (p *secretRevealPage) Filter() string    { return "" }
func (p *secretRevealPage) SetFilter(string)  {}
func (p *secretRevealPage) OnEnter() tea.Cmd  { return nil }
func (p *secretRevealPage) OnLeave()          {}
func (p *secretRevealPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *secretRevealPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter", "space"), key.WithHelp("enter", "reveal/hide")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *secretRevealPage) Update(m tea.Msg) (Page, tea.Cmd) {
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
	case "enter", "space":
		if p.cursor >= 0 && p.cursor < len(p.items) {
			p.items[p.cursor].revealed = !p.items[p.cursor].revealed
		}
	}
	return p, nil
}

func (p *secretRevealPage) View(width, height int) string {
	t := p.theme
	stype := p.stype
	if stype == "" {
		stype = "—"
	}
	var b strings.Builder
	b.WriteString(t.Dim.Render("  type: ") + t.Base.Render(stype) + "\n\n")
	b.WriteString(t.ColHeader.Render(pad("  KEY", 26)+pad("SIZE", 8)+"VALUE") + "\n")
	if len(p.items) == 0 {
		b.WriteString(t.Faint.Render("  (no data keys)") + "\n")
	}
	valWidth := width - 36
	if valWidth < 8 {
		valWidth = 8
	}
	for i, it := range p.items {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		var val string
		if it.revealed {
			val = t.Base.Render(trunc(strings.ReplaceAll(string(it.value), "\n", "⏎"), valWidth))
		} else {
			val = t.Faint.Render(strings.Repeat("•", min(len(it.value), 16)))
		}
		b.WriteString(marker +
			nameStyle.Render(pad(it.key, 24)) +
			t.Faint.Render(pad(strconv.Itoa(len(it.value)), 8)) +
			val + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  enter reveal/hide · esc back"))
	return b.String()
}

func min(a, b int) int {
	if a < b {
		return a
	}
	return b
}
