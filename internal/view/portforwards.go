package view

import (
	"image/color"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/14f3v/kubectl-tui/internal/action/portfwd"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func init() {
	Register("portforwards", []string{"pf", "portforward", "portforwards"}, "active port-forwards", func(d Deps) Page {
		return &pfPage{sess: d.Session, theme: d.Theme}
	})
}

// pfPage lists the session's port-forwards and lets the user restart or delete
// them. It reads the manager's snapshot each render; PFChanged messages trigger
// the repaint.
type pfPage struct {
	sess   *k8s.Session
	theme  style.Theme
	cursor int
}

func (p *pfPage) Init() tea.Cmd     { return nil }
func (p *pfPage) Title() string     { return "port-forwards" }
func (p *pfPage) Kind() string      { return "" }
func (p *pfPage) Namespace() string { return "" }
func (p *pfPage) Filter() string    { return "" }
func (p *pfPage) SetFilter(string)  {}
func (p *pfPage) OnEnter() tea.Cmd  { return nil }
func (p *pfPage) OnLeave()          {}

func (p *pfPage) Summary() Summary {
	return Summary{Total: p.sess.Forwards.Count()}
}

func (p *pfPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "restart")),
		key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl-d", "delete")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *pfPage) Update(m tea.Msg) (Page, tea.Cmd) {
	k, ok := m.(tea.KeyPressMsg)
	if !ok {
		return p, nil // PFChanged and others just trigger a re-render
	}
	list := p.sess.Forwards.List()
	switch k.String() {
	case "j", "down":
		if p.cursor < len(list)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "ctrl+d":
		if p.cursor < len(list) {
			p.sess.Forwards.Remove(list[p.cursor].ID)
		}
	case "r":
		if p.cursor < len(list) {
			fw := list[p.cursor]
			p.sess.Forwards.Remove(fw.ID)
			p.sess.Forwards.Start(fw.Namespace, fw.Pod, fw.Ports)
		}
	}
	return p, nil
}

func (p *pfPage) View(width, height int) string {
	t := p.theme
	list := p.sess.Forwards.List()
	if p.cursor >= len(list) {
		p.cursor = len(list) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}

	var b strings.Builder
	header := t.ColHeader.Render("  POD") + "   " + t.ColHeader.Render("PORTS (local→remote)") + "   " + t.ColHeader.Render("STATE")
	b.WriteString(header + "\n")

	if len(list) == 0 {
		b.WriteString("\n" + t.Faint.Render("  no active port-forwards — press p on a pod to start one"))
		return b.String()
	}

	for i, fw := range list {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		ports := formatPorts(fw)
		state := lipgloss.NewStyle().Foreground(p.stateColor(fw.State)).Render(fw.State.String())
		if fw.State == portfwd.Broken && fw.Err != nil {
			state += t.Faint.Render(" (" + fw.Err.Error() + ")")
		}
		b.WriteString(marker + nameStyle.Render(fw.Namespace+"/"+fw.Pod) + "   " + t.Dim.Render(ports) + "   " + state + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  r restart · ctrl-d delete · these forwards close when you quit or switch context"))
	return b.String()
}

func (p *pfPage) stateColor(s portfwd.State) color.Color {
	switch s {
	case portfwd.Active:
		return p.theme.Pal.OK
	case portfwd.Starting:
		return p.theme.Pal.Warn
	case portfwd.Broken:
		return p.theme.Pal.Err
	default:
		return p.theme.Pal.TextFaint
	}
}

func formatPorts(fw portfwd.Info) string {
	if len(fw.Local) > 0 {
		parts := make([]string, 0, len(fw.Local))
		for _, lp := range fw.Local {
			parts = append(parts, strconv.Itoa(int(lp.Local))+"→"+strconv.Itoa(int(lp.Remote)))
		}
		return strings.Join(parts, ", ")
	}
	return strings.Join(fw.Ports, ", ")
}
