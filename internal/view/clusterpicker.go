package view

import (
	"fmt"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// clusterPickerPage lists every cluster kubetui knows: pasted kubeconfigs,
// loaded files, the default kubeconfig and the in-cluster account. It shows at
// launch and opens from ":ctx". It only emits requests; the root model owns the
// connect, the paste box and every write.
type clusterPickerPage struct {
	theme      style.Theme
	current    *k8s.ClusterRef // the connected cluster, nil before the first connect
	atLaunch   bool
	rows       []clusters.Row
	loaded     bool
	loadErr    error
	noStore    string
	cursor     int
	connecting *k8s.ClusterRef
	errs       map[k8s.ClusterRef]string
	now        func() time.Time
}

// NewClusterPicker builds an empty picker; rows arrive as a ClustersLoaded. At
// launch esc has nowhere to go back to, so the footer offers quit instead.
func NewClusterPicker(theme style.Theme, current *k8s.ClusterRef, atLaunch bool) *clusterPickerPage {
	return &clusterPickerPage{theme: theme, current: current, atLaunch: atLaunch, errs: map[k8s.ClusterRef]string{}, now: time.Now}
}

// SetConnecting marks the row whose connect is in flight; nil clears it.
func (p *clusterPickerPage) SetConnecting(ref *k8s.ClusterRef) {
	p.connecting = ref
	if ref != nil {
		delete(p.errs, *ref)
	}
}

func (p *clusterPickerPage) Init() tea.Cmd     { return nil }
func (p *clusterPickerPage) Title() string     { return "clusters" }
func (p *clusterPickerPage) Kind() string      { return "" }
func (p *clusterPickerPage) Namespace() string { return "" }
func (p *clusterPickerPage) Filter() string    { return "" }
func (p *clusterPickerPage) SetFilter(string)  {}
func (p *clusterPickerPage) OnEnter() tea.Cmd  { return nil }
func (p *clusterPickerPage) OnLeave()          {}
func (p *clusterPickerPage) Summary() Summary  { return Summary{Total: len(p.rows)} }

func (p *clusterPickerPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "connect")),
		key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "paste kubeconfig")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "load file")),
		key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl-d", "remove")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *clusterPickerPage) isCurrent(r clusters.Row) bool {
	return p.current != nil && r.Ref == *p.current
}

func (p *clusterPickerPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case ClustersLoaded:
		p.rows, p.loadErr, p.noStore, p.loaded = t.Rows, t.Err, t.NoStore, true
		want := p.current
		if t.Select != nil {
			want = t.Select
		}
		p.cursor = 0
		for i, r := range p.rows {
			if want != nil && r.Ref == *want {
				p.cursor = i
				break
			}
		}
	case ClusterConnectFailed:
		p.errs[t.Ref] = t.Err
	case tea.KeyPressMsg:
		return p, p.onKey(t.String())
	}
	return p, nil
}

func (p *clusterPickerPage) onKey(k string) tea.Cmd {
	switch k {
	case "j", "down":
		if p.cursor < len(p.rows)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter", "space":
		return p.connect()
	case "p":
		if p.noStore != "" {
			return toast(p.noStore, msg.LevelWarn)
		}
		return func() tea.Msg { return OpenPasteBox{} }
	case "f":
		if p.noStore != "" {
			return toast(p.noStore, msg.LevelWarn)
		}
		req := PromptRequest{
			Title:  "Add kubeconfig file",
			Label:  "Path",
			Action: func(s string) tea.Msg { return AddKubeconfigFile{Path: unquotePath(s)} },
		}
		return func() tea.Msg { return req }
	case "ctrl+d":
		return p.remove()
	}
	return nil
}

func (p *clusterPickerPage) selected() (clusters.Row, bool) {
	if p.cursor < 0 || p.cursor >= len(p.rows) {
		return clusters.Row{}, false
	}
	return p.rows[p.cursor], true
}

func (p *clusterPickerPage) connect() tea.Cmd {
	r, ok := p.selected()
	if !ok {
		return nil
	}
	out := PickMsg(r, p.current)
	return func() tea.Msg { return out }
}

// PickMsg is what choosing r does, both for enter in the picker and for the one
// match of ":ctx <name>", so the two cannot drift. A missing file or the
// connected cluster (current; nil when there is none) only says so with a
// msg.Toast, an unreviewed paste asks for a ReviewCluster before anything in it
// runs, and any other row asks for a SwitchContextRequest. It does no IO.
func PickMsg(r clusters.Row, current *k8s.ClusterRef) tea.Msg {
	switch {
	case r.Missing:
		return msg.Toast{Text: r.Label + ": file missing", Level: msg.LevelWarn}
	case r.NeedsReview:
		return ReviewCluster{Row: r}
	case current != nil && r.Ref == *current:
		return msg.Toast{Text: "already on " + r.Label, Level: msg.LevelInfo}
	}
	return SwitchContextRequest{Ref: r.Ref}
}

func (p *clusterPickerPage) remove() tea.Cmd {
	r, ok := p.selected()
	if !ok {
		return nil
	}
	var prompt string
	switch r.Source {
	case k8s.SourcePasted:
		var names []string
		for _, o := range p.rows {
			if o.Source == k8s.SourcePasted && o.Key.File == r.Key.File {
				names = append(names, o.Ref.Context)
			}
		}
		prompt = fmt.Sprintf("Delete %s and its contexts: %s?", r.Key.File, strings.Join(names, ", "))
	case k8s.SourceFile:
		prompt = fmt.Sprintf("Forget %s? The file is not touched.", r.Ref.Path)
	default:
		return toast(r.Label+" comes from your kubeconfig; kubetui doesn't edit it", msg.LevelInfo)
	}
	req := ConfirmRequest{
		Title:  "remove cluster",
		Prompt: prompt,
		Danger: true,
		Action: func() tea.Msg { return ForgetCluster{Row: r} },
	}
	return func() tea.Msg { return req }
}

// lastUsedText renders when a cluster was last connected to.
func lastUsedText(at, now time.Time) string {
	if at.IsZero() {
		return "—"
	}
	d := now.Sub(at)
	switch {
	case d < time.Minute:
		return "just now"
	case d < time.Hour:
		return fmt.Sprintf("%dm ago", int(d/time.Minute))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh ago", int(d/time.Hour))
	case d < 48*time.Hour:
		return "yesterday"
	}
	return fmt.Sprintf("%d days ago", int(d/(24*time.Hour)))
}

// cell fits s to exactly w cells, cutting with an ellipsis and padding by width.
func cell(s string, w int) string {
	if w <= 0 {
		return ""
	}
	s = ansi.Truncate(s, w, "…")
	if pad := w - ansi.StringWidth(s); pad > 0 {
		s += strings.Repeat(" ", pad)
	}
	return s
}

const (
	pickerSourceW   = 10
	pickerLastUsedW = 12
	pickerMinWide   = 70 // below this the SERVER column is dropped
)

func (p *clusterPickerPage) View(width, height int) string {
	t := p.theme
	if width < 1 || height < 1 {
		return ""
	}
	avail := width - 2 // marker column
	showServer := avail >= pickerMinWide
	nameW, serverW := avail-pickerSourceW-pickerLastUsedW-4, 0
	if showServer {
		nameW = (avail - pickerSourceW - pickerLastUsedW - 6) * 2 / 5
		serverW = avail - pickerSourceW - pickerLastUsedW - 6 - nameW
	}
	row := func(marker, name, source, server, last string) string {
		parts := []string{cell(name, nameW), cell(source, pickerSourceW)}
		if showServer {
			parts = append(parts, cell(server, serverW))
		}
		parts = append(parts, cell(last, pickerLastUsedW))
		return marker + strings.Join(parts, "  ")
	}
	fit := func(s string) string { return ansi.Truncate(s, width, "") }

	var lines []string
	lines = append(lines, fit(t.ColHeader.Render(row("  ", "NAME", "SOURCE", "SERVER", "LAST USED"))))
	cursorLine := 1
	switch {
	case p.loadErr != nil:
		lines = append(lines, fit(t.Faint.Render("  could not list clusters: "+p.loadErr.Error())))
	case !p.loaded:
		lines = append(lines, fit(t.Faint.Render("  loading…")))
	case len(p.rows) == 0:
		lines = append(lines, fit(t.Faint.Render("  (no clusters; press p to paste a kubeconfig)")))
	}
	for i, r := range p.rows {
		marker := "  "
		if p.isCurrent(r) {
			marker = t.AccentText.Render("● ")
		}
		server, last := r.Server, lastUsedText(r.LastUsed, p.now())
		name, st := r.Label, t.Dim
		switch {
		case r.Missing:
			name += " (file missing)"
			st = t.Faint
		case r.NeedsReview:
			server = "needs review"
		}
		if p.connecting != nil && r.Ref == *p.connecting {
			last = "connecting…"
		}
		if i == p.cursor {
			if marker == "  " {
				marker = t.AccentText.Render("▶ ")
			}
			if !r.Missing {
				st = t.Base
			}
			cursorLine = len(lines)
		}
		lines = append(lines, fit(marker+st.Render(row("", name, string(r.Source), server, last))))
		if e := p.errs[r.Ref]; e != "" {
			lines = append(lines, fit(t.Faint.Render("    ✗ "+strings.Join(strings.Fields(e), " "))))
		}
	}

	quit := "q quit"
	if !p.atLaunch {
		quit = "esc back"
	}
	hint := fit(t.Faint.Render("  enter connect · p paste kubeconfig · f load file · ctrl+d remove · " + quit))

	body := height - 2 // blank line and footer
	if body < 1 {
		return hint
	}
	if len(lines) > body {
		off := max(0, min(cursorLine-body+2, len(lines)-body))
		lines = lines[off : off+body]
	}
	return strings.Join(lines, "\n") + "\n\n" + hint
}
