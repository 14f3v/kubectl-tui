package view

import (
	"context"
	"fmt"
	"image/color"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/overview"
	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/tenant"
)

func init() {
	Register("overview", []string{"overview", "dash", "home"}, func(d Deps) Page {
		tp := tenant.NewCapsule(d.Session.Dyn, d.Session.CS, func(tea.Msg) {}, d.TierLabel)
		agg := overview.NewAggregator(d.Session.CS, d.Session.Metrics, tp, d.Session.Engine.Sink())
		return &overviewPage{theme: d.Theme, agg: agg, ctx: d.Session.Context()}
	})
}

type overviewTick struct{}
type overviewRefresh struct{}

// overviewPage renders the cluster dashboard. Data is aggregated on entry and
// every 10s (one-shot lists, like tenants). The rendered content is scrollable
// so it fits any terminal height.
type overviewPage struct {
	theme  style.Theme
	agg    *overview.Aggregator
	ctx    context.Context
	snap   overview.Snapshot
	loaded bool
	offset int
	height int
}

func (p *overviewPage) Init() tea.Cmd     { return nil }
func (p *overviewPage) Title() string     { return "overview" }
func (p *overviewPage) Kind() string      { return "" }
func (p *overviewPage) Namespace() string { return "" }
func (p *overviewPage) Filter() string    { return "" }
func (p *overviewPage) SetFilter(string)  {}
func (p *overviewPage) OnLeave()          {}

func (p *overviewPage) Summary() Summary {
	k := p.snap.KPIs
	return Summary{Total: k.PodsTotal, OK: k.PodsRunning, Warn: k.PodsPending, Err: k.PodsFailed}
}

func (p *overviewPage) OnEnter() tea.Cmd { return p.refreshCmd() }

func (p *overviewPage) refreshCmd() tea.Cmd {
	return func() tea.Msg {
		p.agg.Refresh(p.ctx) // emits an overview.Snapshot via the sink
		return overviewTick{}
	}
}

func (p *overviewPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "scroll")),
		key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "pods")),
		key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "tenants")),
		key.NewBinding(key.WithKeys("n"), key.WithHelp("n", "nodes")),
		key.NewBinding(key.WithKeys("e"), key.WithHelp("e", "events")),
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
	}
}

func (p *overviewPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case overview.Snapshot:
		p.snap = t
		p.loaded = t.Loaded
		return p, nil
	case overviewTick:
		return p, tea.Tick(10*time.Second, func(time.Time) tea.Msg { return overviewRefresh{} })
	case overviewRefresh:
		return p, p.refreshCmd()
	case tea.KeyPressMsg:
		switch t.String() {
		case "j", "down":
			p.offset++
		case "k", "up":
			if p.offset > 0 {
				p.offset--
			}
		case "g", "home":
			p.offset = 0
		case "p":
			return p, nav("pods")
		case "t":
			return p, nav("tenants")
		case "n":
			return p, nav("nodes")
		case "e":
			return p, nav("events")
		case "r":
			return p, p.refreshCmd()
		}
	}
	return p, nil
}

func nav(kind string) tea.Cmd {
	return func() tea.Msg { return msg.Navigate{Kind: kind} }
}

func (p *overviewPage) View(width, height int) string {
	p.height = height
	if !p.loaded {
		return "\n" + p.theme.Faint.Render("  gathering cluster overview…")
	}
	lines := strings.Split(p.render(width), "\n")
	maxOff := len(lines) - height
	if maxOff < 0 {
		maxOff = 0
	}
	if p.offset > maxOff {
		p.offset = maxOff
	}
	end := p.offset + height
	if end > len(lines) {
		end = len(lines)
	}
	return strings.Join(lines[p.offset:end], "\n")
}

// render builds the full (unclipped) dashboard content.
func (p *overviewPage) render(width int) string {
	t := p.theme
	var b strings.Builder

	b.WriteString(p.kpiRow(width) + "\n\n")

	half := (width - 3) / 2
	// Capacity + Pod phases side by side.
	cap := p.panel("CLUSTER CAPACITY", p.capacityBody(half), half)
	phases := p.panel("POD PHASES", p.phasesBody(half), half)
	b.WriteString(joinCols(cap, phases) + "\n")

	b.WriteString(p.panel("NODES", p.nodesBody(width), width) + "\n")

	work := p.panel("WORKLOADS", p.workloadsBody(half), half)
	top := p.panel("TOP CPU", p.topBody(half), half)
	b.WriteString(joinCols(work, top) + "\n")

	b.WriteString(p.panel("RECENT EVENTS", p.eventsBody(width), width))
	_ = t
	return b.String()
}

// ---- KPI tiles ----

func (p *overviewPage) kpiRow(width int) string {
	t := p.theme
	k := p.snap.KPIs
	tw := (width - 3) / 4

	tenantsVal, tenantsSub := "n/a", t.Faint.Render("capsule not installed")
	if k.TenantsAvailable {
		tenantsVal = itoaK(k.Tenants)
		tenantsSub = subDot(k.TenantsOverQuota, "over quota", t.Pal.Err, t)
	}
	tiles := []string{
		kpiTile("NODES READY", fmt.Sprintf("%d/%d", k.NodesReady, k.NodesTotal),
			subDot(k.NodesCordoned, "cordoned", t.Pal.Warn, t), tw, t),
		kpiTile("PODS RUNNING", fmt.Sprintf("%d/%d", k.PodsRunning, k.PodsTotal),
			t.Dim.Render(fmt.Sprintf("%d pending · %d failed", k.PodsPending, k.PodsFailed)), tw, t),
		kpiTile("TENANTS", tenantsVal, tenantsSub, tw, t),
		kpiTile("ALERTS", alertVal(k.Alerts, t),
			t.Dim.Render(fmt.Sprintf("%d warning · %d critical", k.AlertsWarning, k.AlertsCritical)), tw, t),
	}
	return lipgloss.JoinHorizontal(lipgloss.Top, spaced(tiles, 1)...)
}

func kpiTile(title, value, sub string, w int, t style.Theme) string {
	body := t.Faint.Render(title) + "\n" +
		lipgloss.NewStyle().Foreground(t.Pal.Text).Bold(true).Render(value) + "\n" +
		sub
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Pal.Border).
		Padding(0, 1).
		Width(w).
		Render(body)
}

func alertVal(n int, t style.Theme) string {
	c := t.Pal.OK
	if n > 0 {
		c = t.Pal.Err
	}
	return lipgloss.NewStyle().Foreground(c).Bold(true).Render(itoaK(n))
}

func subDot(n int, label string, c color.Color, t style.Theme) string {
	if n == 0 {
		return t.Faint.Render("none " + label)
	}
	return lipgloss.NewStyle().Foreground(c).Render("● ") + t.Dim.Render(fmt.Sprintf("%d %s", n, label))
}

// ---- panels ----

func (p *overviewPage) panel(title, body string, w int) string {
	t := p.theme
	head := lipgloss.NewStyle().Foreground(t.Pal.Accent).Render("▍") + " " +
		t.ColHeader.Render(title)
	inner := head + "\n" + body
	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Pal.Border).
		Padding(0, 1).
		Width(w).
		Render(inner)
}

func (p *overviewPage) capacityBody(w int) string {
	t := p.theme
	c := p.snap.Capacity
	barW := w - 8
	if barW < 6 {
		barW = 6
	}
	row := func(label string, pct float64, text string) string {
		return t.Dim.Render(pad(label, 7)) + bar(pct, barW, t) + "\n" + t.Faint.Render("  "+text)
	}
	return row("CPU", c.CPUPct, c.CPUText) + "\n" +
		row("MEM", c.MemPct, c.MemText) + "\n" +
		row("PODS", c.PodsPct, c.PodsText)
}

func (p *overviewPage) phasesBody(w int) string {
	t := p.theme
	var b strings.Builder
	for _, ph := range p.snap.Phases {
		b.WriteString(
			lipgloss.NewStyle().Foreground(t.StatusColor(ph.Class)).Render("■ ") +
				t.Dim.Render(pad(ph.Label, 11)) +
				t.Base.Render(pad(itoaK(ph.N), 6)) +
				t.Faint.Render(fmt.Sprintf("%3.0f%%", ph.Pct)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *overviewPage) nodesBody(w int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render(pad("NAME", 20)+pad("STATUS", 12)+pad("CPU", 8)+pad("MEM", 8)+"PODS") + "\n")
	for _, n := range p.snap.Nodes {
		cpu, mem := "—", "—"
		if n.HasMetrics {
			cpu = fmt.Sprintf("%.0f%%", n.CPUPct)
			mem = fmt.Sprintf("%.0f%%", n.MemPct)
		}
		status := lipgloss.NewStyle().Foreground(t.StatusColor(n.StatusClass)).Render("● " + n.Status)
		b.WriteString(t.Dim.Render(pad(trunc(n.Name, 19), 20)) +
			pad(status, 12) +
			t.Faint.Render(pad(cpu, 8)) +
			t.Faint.Render(pad(mem, 8)) +
			t.Faint.Render(n.Pods) + "\n")
	}
	if len(p.snap.Nodes) == 0 {
		b.WriteString(t.Faint.Render("  no nodes visible"))
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *overviewPage) workloadsBody(w int) string {
	t := p.theme
	barW := w - 24
	if barW < 5 {
		barW = 5
	}
	var b strings.Builder
	for _, wl := range p.snap.Workloads {
		frac := 0.0
		if wl.Total > 0 {
			frac = float64(wl.Ready) / float64(wl.Total)
		}
		b.WriteString(t.Dim.Render(pad(wl.Name, 13)) +
			barFrac(frac, barW, t.StatusColor(wl.Class), t) + " " +
			t.Faint.Render(fmt.Sprintf("%d/%d", wl.Ready, wl.Total)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *overviewPage) topBody(w int) string {
	t := p.theme
	if len(p.snap.TopCPU) == 0 {
		return t.Faint.Render("metrics not available")
	}
	barW := w - 24
	if barW < 5 {
		barW = 5
	}
	max := p.snap.TopCPU[0].Millis
	if max <= 0 {
		max = 1
	}
	var b strings.Builder
	for _, c := range p.snap.TopCPU {
		frac := float64(c.Millis) / float64(max)
		b.WriteString(t.Dim.Render(pad(trunc(c.Name, 12), 13)) +
			barFrac(frac, barW, t.Pal.Accent, t) + " " +
			t.Faint.Render(fmt.Sprintf("%dm", c.Millis)) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

func (p *overviewPage) eventsBody(w int) string {
	t := p.theme
	if len(p.snap.Events) == 0 {
		return t.Faint.Render("no recent events")
	}
	var b strings.Builder
	for _, e := range p.snap.Events {
		reason := lipgloss.NewStyle().Foreground(t.StatusColor(e.Class)).Bold(true).Render(pad(e.Reason, 18))
		msgW := w - 30
		if msgW < 10 {
			msgW = 10
		}
		b.WriteString(reason + t.Faint.Render(pad(trunc(e.Message, msgW), msgW)) + " " + t.Faint.Render(e.Age) + "\n")
	}
	return strings.TrimRight(b.String(), "\n")
}

// ---- bar + layout helpers ----

func bar(pct float64, width int, t style.Theme) string {
	return barFrac(pct/100, width, thresholdColor(pct/100, t), t)
}

func barFrac(frac float64, width int, col color.Color, t style.Theme) string {
	if frac < 0 {
		frac = 0
	}
	if frac > 1 {
		frac = 1
	}
	n := int(frac*float64(width) + 0.5)
	if n > width {
		n = width
	}
	return lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("█", n)) +
		t.Faint.Render(strings.Repeat("░", width-n))
}

func thresholdColor(frac float64, t style.Theme) color.Color {
	switch {
	case frac >= 0.9:
		return t.Pal.Err
	case frac >= 0.7:
		return t.Pal.Warn
	default:
		return t.Pal.Accent
	}
}

// joinCols joins two panels side by side with a gap.
func joinCols(a, b string) string {
	return lipgloss.JoinHorizontal(lipgloss.Top, a, " ", b)
}

// spaced interleaves single-space gaps between blocks for JoinHorizontal.
func spaced(blocks []string, gap int) []string {
	if len(blocks) == 0 {
		return blocks
	}
	sep := strings.Repeat(" ", gap)
	out := make([]string, 0, len(blocks)*2-1)
	for i, blk := range blocks {
		if i > 0 {
			out = append(out, sep)
		}
		out = append(out, blk)
	}
	return out
}

func itoaK(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	neg := n < 0
	if neg {
		n = -n
	}
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		b[i] = '-'
	}
	return string(b[i:])
}
