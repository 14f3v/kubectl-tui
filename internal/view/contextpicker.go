package view

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// contextPickerPage lists the kubeconfig contexts so the user can switch without
// typing the exact name. Pushed by ":ctx" with no argument. Selecting a context
// emits a SwitchContextRequest; selecting the current one is a no-op.
type contextPickerPage struct {
	theme    style.Theme
	contexts []string
	current  string
	cursor   int
}

// NewContextPicker builds the picker from the session's known contexts, starting
// the cursor on the current context.
func NewContextPicker(sess *k8s.Session, theme style.Theme) *contextPickerPage {
	names, current := sess.Contexts()
	cursor := 0
	for i, n := range names {
		if n == current {
			cursor = i
			break
		}
	}
	return &contextPickerPage{theme: theme, contexts: names, current: current, cursor: cursor}
}

func (p *contextPickerPage) Init() tea.Cmd     { return nil }
func (p *contextPickerPage) Title() string     { return "contexts" }
func (p *contextPickerPage) Kind() string      { return "" }
func (p *contextPickerPage) Namespace() string { return "" }
func (p *contextPickerPage) Filter() string    { return "" }
func (p *contextPickerPage) SetFilter(string)  {}
func (p *contextPickerPage) OnEnter() tea.Cmd  { return nil }
func (p *contextPickerPage) OnLeave()          {}
func (p *contextPickerPage) Summary() Summary  { return Summary{Total: len(p.contexts)} }

func (p *contextPickerPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "switch")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *contextPickerPage) Update(m tea.Msg) (Page, tea.Cmd) {
	k, ok := m.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "j", "down":
		if p.cursor < len(p.contexts)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter", "space":
		if p.cursor < 0 || p.cursor >= len(p.contexts) {
			return p, nil
		}
		name := p.contexts[p.cursor]
		if name == p.current {
			return p, toast("already on context "+name, msg.LevelInfo)
		}
		return p, func() tea.Msg { return SwitchContextRequest{Name: name} }
	}
	return p, nil
}

func (p *contextPickerPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  CONTEXT") + "\n")
	if len(p.contexts) == 0 {
		b.WriteString(t.Faint.Render("  (no contexts in kubeconfig)") + "\n")
	}
	for i, name := range p.contexts {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		line := marker + nameStyle.Render(name)
		if name == p.current {
			line += t.Faint.Render("  (current)")
		}
		b.WriteString(line + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  enter switch · esc back"))
	return b.String()
}
