package view

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/debug"
	"github.com/14f3v/kubectl-tui/internal/action/execshell"
	"github.com/14f3v/kubectl-tui/internal/action/nodeops"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

type nodeOpsItem struct{ label, desc string }

// nodeOpsPage is the menu pushed by pressing enter on a node: cordon, uncordon,
// or drain. Drain is confirm-gated (it evicts pods).
type nodeOpsPage struct {
	sess     *k8s.Session
	theme    style.Theme
	node     string
	readOnly bool
	items    []nodeOpsItem
	cursor   int
}

func newNodeOpsPage(sess *k8s.Session, theme style.Theme, node string, readOnly bool) *nodeOpsPage {
	return &nodeOpsPage{
		sess: sess, theme: theme, node: node, readOnly: readOnly,
		items: []nodeOpsItem{
			{"Cordon", "mark unschedulable (no new pods land here)"},
			{"Uncordon", "mark schedulable again"},
			{"Drain", "cordon, then evict pods (skips DaemonSet & mirror pods)"},
			{"Debug", "launch a privileged host shell on this node"},
		},
	}
}

func (p *nodeOpsPage) Init() tea.Cmd     { return nil }
func (p *nodeOpsPage) Title() string     { return p.node + " · node" }
func (p *nodeOpsPage) Kind() string      { return "" }
func (p *nodeOpsPage) Namespace() string { return "" }
func (p *nodeOpsPage) Filter() string    { return "" }
func (p *nodeOpsPage) SetFilter(string)  {}
func (p *nodeOpsPage) OnEnter() tea.Cmd  { return nil }
func (p *nodeOpsPage) OnLeave()          {}
func (p *nodeOpsPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *nodeOpsPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *nodeOpsPage) Update(m tea.Msg) (Page, tea.Cmd) {
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
		case "Cordon":
			return p, p.cordon(true)
		case "Uncordon":
			return p, p.cordon(false)
		case "Drain":
			return p, p.drain()
		case "Debug":
			return p, p.debug()
		}
	}
	return p, nil
}

// debug launches a privileged host-namespace pod on the node and execs into it —
// a node shell without SSH. Confirm-gated (it creates a privileged pod).
func (p *nodeOpsPage) debug() tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	sess, node := p.sess, p.node
	act := func() tea.Msg {
		ns, pod, err := debug.CreateNodeDebug(sess.Context(), sess.CS, node, "busybox")
		if err != nil {
			return msg.Toast{Text: "node debug: " + err.Error(), Level: msg.LevelError}
		}
		return ExecRequest{Label: "node-debug", Command: execshell.New(sess.RestCfg, sess.CS, ns, pod, "debugger", nil)}
	}
	return func() tea.Msg {
		return ConfirmRequest{
			Title:  "Node debug " + node,
			Prompt: "Launch a privileged debug pod on " + node + "? (host root is at /host)",
			Danger: true,
			Action: act,
		}
	}
}

func (p *nodeOpsPage) cordon(on bool) tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	sess, node := p.sess, p.node
	verb := "cordon"
	if !on {
		verb = "uncordon"
	}
	return func() tea.Msg {
		var err error
		if on {
			err = nodeops.Cordon(sess.Context(), sess.CS, node)
		} else {
			err = nodeops.Uncordon(sess.Context(), sess.CS, node)
		}
		if err != nil {
			return msg.Toast{Text: verb + " " + node + ": " + err.Error(), Level: msg.LevelError}
		}
		return msg.Toast{Text: node + " " + verb + "ed", Level: msg.LevelSuccess}
	}
}

func (p *nodeOpsPage) drain() tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	sess, node := p.sess, p.node
	act := func() tea.Msg {
		res, err := nodeops.Drain(sess.Context(), sess.CS, node)
		if err != nil {
			return msg.Toast{Text: "drain " + node + ": " + err.Error(), Level: msg.LevelError}
		}
		text := fmt.Sprintf("drained %s — %d evicted, %d skipped", node, len(res.Evicted), len(res.Skipped))
		level := msg.LevelSuccess
		if len(res.Errors) > 0 {
			text += fmt.Sprintf(", %d errors", len(res.Errors))
			level = msg.LevelWarn
		}
		return msg.Toast{Text: text, Level: level}
	}
	return func() tea.Msg {
		return ConfirmRequest{
			Title:  "Drain " + node,
			Prompt: "Cordon " + node + " and evict its pods? DaemonSet & mirror pods are skipped.",
			Danger: true,
			Action: act,
		}
	}
}

func (p *nodeOpsPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  NODE ACTION") + "\n")
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
	b.WriteString("\n" + t.Faint.Render("  enter run · esc back"))
	return b.String()
}
