package view

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/metrics"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func init() {
	Register("nodetop", []string{"topnodes", "top-nodes"}, "node CPU/memory usage", func(d Deps) Page {
		return newNodeTopPage(d)
	})
}

// nodeTopPage shows per-node CPU/MEM usage, the TUI equivalent of
// `kubectl top node`. It collects once on entry and then refreshes on a timer;
// if metrics-server is absent it degrades gracefully to an explanatory line.
type nodeTopPage struct {
	sess      *k8s.Session
	theme     style.Theme
	rows      []metrics.NodeUsage
	cursor    int
	available bool
	reason    string
	loaded    bool
}

func newNodeTopPage(d Deps) *nodeTopPage {
	return &nodeTopPage{sess: d.Session, theme: d.Theme}
}

// nodeTopMsg carries the result of a collection pass.
type nodeTopMsg struct {
	available bool
	reason    string
	rows      []metrics.NodeUsage
}

// nodeTopTickMsg fires the periodic refresh.
type nodeTopTickMsg struct{}

func (p *nodeTopPage) Init() tea.Cmd     { return p.refresh() }
func (p *nodeTopPage) OnEnter() tea.Cmd  { return p.refresh() }
func (p *nodeTopPage) Title() string     { return "node top" }
func (p *nodeTopPage) Kind() string      { return "" }
func (p *nodeTopPage) Namespace() string { return "" }
func (p *nodeTopPage) Filter() string    { return "" }
func (p *nodeTopPage) SetFilter(string)  {}
func (p *nodeTopPage) OnLeave()          {}

func (p *nodeTopPage) Summary() Summary {
	return Summary{Total: len(p.rows)}
}

func (p *nodeTopPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// refresh collects node metrics off the UI thread. It never panics: a nil
// metrics client, an absent metrics-server, or a collection error all map to an
// unavailable result with a short reason.
func (p *nodeTopPage) refresh() tea.Cmd {
	sess := p.sess
	return func() tea.Msg {
		if sess.Metrics == nil {
			return nodeTopMsg{available: false, reason: "metrics client unavailable"}
		}
		if ok, reason := metrics.Probe(sess.Disco); !ok {
			return nodeTopMsg{available: false, reason: reason}
		}
		rows, err := metrics.CollectNodes(sess.Context(), sess.CS, sess.Metrics)
		if err != nil {
			return nodeTopMsg{available: false, reason: err.Error()}
		}
		return nodeTopMsg{available: true, rows: rows}
	}
}

func (p *nodeTopPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch msg := m.(type) {
	case nodeTopMsg:
		p.available = msg.available
		p.reason = msg.reason
		p.rows = msg.rows
		p.loaded = true
		return p, tea.Tick(15*time.Second, func(time.Time) tea.Msg { return nodeTopTickMsg{} })
	case nodeTopTickMsg:
		return p, p.refresh()
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

func (p *nodeTopPage) View(width, height int) string {
	t := p.theme

	var b strings.Builder
	header := t.ColHeader.Render("  " + pad("NODE", 22) + pad("CPU", 10) + pad("CPU%", 8) + pad("MEM", 12) + "MEM%")
	b.WriteString(header + "\n")

	if !p.loaded {
		b.WriteString("\n" + t.Faint.Render("  collecting node metrics…"))
		return b.String()
	}
	if !p.available {
		b.WriteString("\n" + t.Faint.Render("  node metrics unavailable — "+p.reason+" (needs metrics-server, like kubectl top)"))
		return b.String()
	}

	if p.cursor >= len(p.rows) {
		p.cursor = len(p.rows) - 1
	}
	if p.cursor < 0 {
		p.cursor = 0
	}

	for i, r := range p.rows {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		line := nameStyle.Render(pad(r.Name, 22)) +
			t.Dim.Render(pad(metrics.FormatCPU(r.CPUMillis), 10)) +
			t.Dim.Render(pad(fmt.Sprintf("%.0f%%", r.CPUPct), 8)) +
			t.Dim.Render(pad(metrics.FormatMem(r.MemBytes), 12)) +
			t.Dim.Render(fmt.Sprintf("%.0f%%", r.MemPct))
		b.WriteString(marker + line + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  r refresh · esc back"))
	return b.String()
}
