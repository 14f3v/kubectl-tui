package app

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/component"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/msg"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/view"
)

const headerHeight = 5

// View renders the full screen. Alt-screen, mouse, title, and the forced dark
// background all live on the returned View, per Bubble Tea v2.
func (m *Model) View() tea.View {
	v := tea.NewView(m.render())
	v.AltScreen = true
	v.MouseMode = tea.MouseModeCellMotion
	v.BackgroundColor = m.theme.Pal.Bg
	v.WindowTitle = m.windowTitle()
	return v
}

func (m *Model) windowTitle() string {
	if m.sess == nil {
		return "kubetui"
	}
	id := m.sess.Identity
	who := id.User
	if who == "" {
		who = "kube"
	}
	where := id.Cluster
	if where == "" {
		where = id.Context
	}
	return who + "@" + where + " — kubetui"
}

func (m *Model) render() string {
	if m.width <= 0 || m.height <= 0 {
		return ""
	}
	switch {
	case m.panicInfo != "":
		return m.renderPanic()
	case m.fatal != nil:
		return m.renderFatal()
	case m.booting || m.sess == nil:
		return m.renderCentered("connecting to cluster…", m.theme.Dim)
	}

	cmd := m.renderCommandBar()
	header := m.renderHeader()
	crumb := m.renderBreadcrumb()
	footer := m.renderFooter()

	contentH := m.height - 1 - headerHeight - 1 - 1
	if contentH < 1 {
		contentH = 1
	}
	content := m.renderContent(contentH)

	return strings.Join([]string{cmd, header, crumb, content, footer}, "\n")
}

// ---- command bar ----

func (m *Model) renderCommandBar() string {
	t := m.theme
	var left string
	switch m.mode {
	case modeCommand:
		left = t.PinkText.Render(":") + " " + t.Base.Render(m.inputBuf) + t.AccentText.Render("▊")
	case modeFilter:
		left = t.PinkText.Render("/") + " " + t.Base.Render(m.inputBuf) + t.AccentText.Render("▊")
	default:
		cmdText := ":"
		if p := m.active(); p != nil {
			cmdText = ":" + p.Title()
		}
		left = t.PinkText.Render(":") + " " + t.Base.Render(strings.TrimPrefix(cmdText, ":"))
	}

	clock := "—"
	if p := m.active(); p != nil {
		if s := p.Summary(); !s.SyncedAt.IsZero() {
			clock = s.SyncedAt.Format("15:04:05")
		}
	}
	right := t.Faint.Render("last sync ") + lipgloss.NewStyle().Foreground(t.Pal.OK).Render(clock)
	return spread(left, right, m.width)
}

// ---- header ----

func (m *Model) renderHeader() string {
	t := m.theme
	id := m.sess.Identity
	usable := m.width - 4
	if usable < 12 {
		usable = 12
	}
	wA := usable * 30 / 100
	wB := usable * 34 / 100
	wC := usable - wA - wB

	labelA := lipgloss.NewStyle().Foreground(t.Pal.Accent)
	labelB := lipgloss.NewStyle().Foreground(t.Pal.Cyan)
	val := t.Base
	dim := t.Dim

	colA := []string{
		kv("Context", orDash(id.Context), 9, labelA, val),
		kv("Cluster", orDash(id.Cluster), 9, labelA, val),
		kv("User", orDash(id.User), 9, labelA, val),
		kv("Kubetui", Version, 9, labelA, dim),
		kv("K8s Rev", orDash(id.K8sVersion), 9, labelA, dim),
	}

	nsLabel := id.Namespace
	if nsLabel == "" {
		nsLabel = "all"
	}
	sum := view.Summary{}
	kind := "—"
	if p := m.active(); p != nil {
		sum = p.Summary()
		kind = p.Title()
	}
	colB := []string{
		kv("Namespace", nsLabel, 10, labelB, val),
		kv("Resource", kind, 10, labelB, val),
		labelB.Render(padRight("Count", 10)) + component.Count(sum.Total, sum.OK, sum.Warn, sum.Err, t),
		labelB.Render(padRight("CPU", 10)) + t.Faint.Render("metrics n/a"),
		labelB.Render(padRight("MEM", 10)) + t.Faint.Render("metrics n/a"),
	}

	colC := m.renderKeyGrid()

	block := lipgloss.JoinHorizontal(lipgloss.Top,
		lipgloss.NewStyle().Width(wA).Render(strings.Join(colA, "\n")),
		"  ",
		lipgloss.NewStyle().Width(wB).Render(strings.Join(colB, "\n")),
		"  ",
		lipgloss.NewStyle().Width(wC).Render(strings.Join(colC, "\n")),
	)
	return fitBlock(block, m.width, headerHeight)
}

// renderKeyGrid lays the active page's context keys into a 2-column grid.
func (m *Model) renderKeyGrid() []string {
	t := m.theme
	var binds []key.Binding
	if p := m.active(); p != nil {
		binds = p.Keys()
	}
	hints := make([]string, 0, len(binds))
	for _, b := range binds {
		h := b.Help()
		if h.Key == "" {
			continue
		}
		hints = append(hints, component.KeyHint(h.Key, h.Desc, t))
	}
	// Pair up into rows of two, max 5 rows.
	rows := make([]string, 0, headerHeight)
	for i := 0; i < len(hints) && len(rows) < headerHeight; i += 2 {
		left := lipgloss.NewStyle().Width(18).Render(hints[i])
		right := ""
		if i+1 < len(hints) {
			right = hints[i+1]
		}
		rows = append(rows, left+right)
	}
	for len(rows) < headerHeight {
		rows = append(rows, "")
	}
	return rows
}

// ---- breadcrumb ----

func (m *Model) renderBreadcrumb() string {
	t := m.theme
	p := m.active()
	if p == nil {
		return fitLine("", m.width)
	}
	sum := p.Summary()
	ns := p.Namespace()
	if ns == "" {
		ns = "all"
	}
	left := t.Faint.Render("<") +
		t.AccentText.Render(p.Title()) +
		t.Faint.Render("(") + t.CyanText.Render(ns) + t.Faint.Render(")") +
		t.Dim.Render("["+itoa(sum.Total)+"]") +
		t.Faint.Render(">")

	if sum.Phase == engine.PhaseStale {
		left += "  " + lipgloss.NewStyle().Foreground(t.Pal.Warn).Bold(true).Render("STALE")
	}

	var right string
	if f := p.Filter(); f != "" {
		right = t.Faint.Render("/ ") + t.Dim.Render(f)
	} else {
		right = t.Faint.Render("/ filter")
	}
	return spread(left, right, m.width)
}

// ---- footer ----

func (m *Model) renderFooter() string {
	if m.toast != nil {
		return m.renderToast()
	}
	t := m.theme
	chips := []string{
		component.Chip(":", "cmd", t),
		component.Chip("/", "filter", t),
		component.Chip("?", "help", t),
		component.Chip("↑↓", "nav", t),
		component.Chip("enter", "select", t),
		component.Chip("d", "describe", t),
		component.Chip("l", "logs", t),
		component.Chip("esc", "back", t),
		component.Chip("q", "quit", t),
	}
	return fitLine(strings.Join(chips, " "), m.width)
}

func (m *Model) renderToast() string {
	t := m.theme
	col := t.Pal.Info
	switch m.toast.Level {
	case msg.LevelSuccess:
		col = t.Pal.OK
	case msg.LevelWarn:
		col = t.Pal.Warn
	case msg.LevelError:
		col = t.Pal.Err
	}
	return fitLine(lipgloss.NewStyle().Foreground(col).Render("• "+m.toast.Text), m.width)
}

// ---- content ----

func (m *Model) renderContent(h int) string {
	if m.showHelp {
		return m.renderHelp(h)
	}
	p := m.active()
	if p == nil {
		return fitBlock("", m.width, h)
	}
	sum := p.Summary()
	if sum.Phase == engine.PhaseTerminal && sum.Error != nil {
		return m.renderBanner(sum.Error, h)
	}
	return fitBlock(p.View(m.width, h), m.width, h)
}

func (m *Model) renderBanner(e *engine.EngineErr, h int) string {
	t := m.theme
	title := "cannot watch this resource"
	switch e.Class {
	case engine.ClassForbidden:
		title = "forbidden — your account cannot watch this resource"
	case engine.ClassAuth:
		title = "credentials expired — re-authenticate, then press r to retry"
	case engine.ClassTLS:
		title = "TLS error connecting to the cluster"
	}
	body := lipgloss.NewStyle().Foreground(t.Pal.Err).Bold(true).Render(title) + "\n\n" +
		t.Faint.Render(e.Detail)
	return m.renderCentered(body, t.Base)
}

func (m *Model) renderHelp(h int) string {
	t := m.theme
	var b strings.Builder
	b.WriteString(t.AccentText.Bold(true).Render("kubetui — keys"))
	b.WriteString("\n\n")
	globals := [][2]string{
		{":", "command line (:pods, :ns, :q)"},
		{"/", "filter rows (prefix ! to invert)"},
		{"j/k ↑/↓", "move cursor"},
		{"g/G", "top / bottom"},
		{"enter", "drill in"},
		{"?", "toggle this help"},
		{"esc", "back / clear filter"},
		{"q", "quit"},
	}
	for _, g := range globals {
		b.WriteString(t.PinkText.Render(padRight(g[0], 12)) + t.Dim.Render(g[1]) + "\n")
	}
	if p := m.active(); p != nil {
		b.WriteString("\n" + t.AccentText.Render("actions") + "\n")
		for _, bind := range p.Keys() {
			hp := bind.Help()
			b.WriteString(t.PinkText.Render(padRight(hp.Key, 12)) + t.Dim.Render(hp.Desc) + "\n")
		}
	}
	b.WriteString("\n" + t.Faint.Render("press any key to close"))
	return fitBlock(b.String(), m.width, h)
}

// ---- fatal / panic / centered ----

func (m *Model) renderFatal() string {
	t := m.theme
	body := lipgloss.NewStyle().Foreground(t.Pal.Err).Bold(true).Render("could not connect to the cluster") +
		"\n\n" + t.Dim.Render(m.fatal.Error()) +
		"\n\n" + t.Faint.Render("check your kubeconfig / context, then restart · q to quit")
	return m.renderCentered(body, t.Base)
}

func (m *Model) renderPanic() string {
	t := m.theme
	body := lipgloss.NewStyle().Foreground(t.Pal.Err).Bold(true).Render("internal error (recovered)") +
		"\n\n" + t.Dim.Render(m.panicInfo) +
		"\n\n" + t.Faint.Render("press r to resume · q to quit")
	return m.renderCentered(body, t.Base)
}

func (m *Model) renderCentered(body string, _ lipgloss.Style) string {
	return lipgloss.Place(m.width, m.height, lipgloss.Center, lipgloss.Center, body)
}

// ---- layout helpers ----

// spread places left and right on one line separated by filler, truncating to
// width if the two would overlap.
func spread(left, right string, width int) string {
	lw := lipgloss.Width(left)
	rw := lipgloss.Width(right)
	if lw+rw+1 > width {
		// Not enough room; keep the left, drop the right.
		return fitLine(left, width)
	}
	gap := width - lw - rw
	return left + strings.Repeat(" ", gap) + right
}

// fitLine truncates or right-pads a single line to exactly width columns.
func fitLine(s string, width int) string {
	if width <= 0 {
		return ""
	}
	if idx := strings.IndexByte(s, '\n'); idx >= 0 {
		s = s[:idx]
	}
	w := lipgloss.Width(s)
	if w > width {
		return ansi.Truncate(s, width, "…")
	}
	return s + strings.Repeat(" ", width-w)
}

// fitBlock forces s to exactly height lines, each fit to width.
func fitBlock(s string, width, height int) string {
	lines := strings.Split(s, "\n")
	out := make([]string, height)
	for i := 0; i < height; i++ {
		if i < len(lines) {
			out[i] = fitLine(lines[i], width)
		} else {
			out[i] = strings.Repeat(" ", width)
		}
	}
	return strings.Join(out, "\n")
}

// kv renders "label value" with the label padded and styled.
func kv(label, value string, labelW int, labelStyle, valueStyle lipgloss.Style) string {
	return labelStyle.Render(padRight(label, labelW)) + valueStyle.Render(value)
}

func padRight(s string, w int) string {
	if len(s) >= w {
		return s
	}
	return s + strings.Repeat(" ", w-len(s))
}

func orDash(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

// itoa is a tiny int formatter used by the breadcrumb count.
func itoa(n int) string { return strconv.Itoa(n) }
