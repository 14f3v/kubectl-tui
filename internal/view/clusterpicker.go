package view

import (
	"fmt"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
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
	banner     []BannerLine
	noStore    string
	cursor     int
	connecting *k8s.ClusterRef
	// errs holds why a connect failed, by ref. One whose row is listed shows
	// under it until that row connects again; one with no row (a -context
	// the kubeconfig does not have) shows above the table until any connect.
	errs map[k8s.ClusterRef]string
	now  func() time.Time
}

// NewClusterPicker builds an empty picker; rows arrive as a ClustersLoaded. At
// launch esc has nowhere to go back to, so the footer offers quit instead.
func NewClusterPicker(theme style.Theme, current *k8s.ClusterRef, atLaunch bool) *clusterPickerPage {
	return &clusterPickerPage{theme: theme, current: current, atLaunch: atLaunch, errs: map[k8s.ClusterRef]string{}, now: time.Now}
}

// SetConnecting marks the row whose connect is in flight; nil clears it. A new
// connect clears its own row's error and every error that has no row.
func (p *clusterPickerPage) SetConnecting(ref *k8s.ClusterRef) {
	p.connecting = ref
	if ref == nil {
		return
	}
	delete(p.errs, *ref)
	for r := range p.errs {
		if !p.listed(r) {
			delete(p.errs, r)
		}
	}
}

// listed reports whether ref has a row.
func (p *clusterPickerPage) listed(ref k8s.ClusterRef) bool {
	for _, r := range p.rows {
		if r.Ref == ref {
			return true
		}
	}
	return false
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

// Keys lists the picker's keys. At launch esc has nowhere to go back to (help
// already lists q), so it is left out.
func (p *clusterPickerPage) Keys() []key.Binding {
	keys := []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "connect")),
		key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "paste kubeconfig")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "load file")),
		key.NewBinding(key.WithKeys("ctrl+d"), key.WithHelp("ctrl-d", "remove")),
	}
	if !p.atLaunch {
		keys = append(keys, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")))
	}
	return keys
}

func (p *clusterPickerPage) isCurrent(r clusters.Row) bool {
	return p.current != nil && r.Ref == *p.current
}

func (p *clusterPickerPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case ClustersLoaded:
		p.rows, p.banner, p.noStore, p.loaded = t.Rows, t.Banner, t.NoStore, true
		want := p.current
		if t.Select != nil {
			want = t.Select
		}
		p.cursor = p.startRow(want, t.Current)
	case ClusterConnectFailed:
		p.errs[t.Ref] = t.Err
	case tea.KeyPressMsg:
		return p, p.onKey(t.String())
	}
	return p, nil
}

// startRow is the row the cursor starts on: want's, if listed; else the most
// recently used row; else, when nothing has been used yet, the default
// kubeconfig's current-context, which is where plain kubetui connected before
// it had a picker; else the first.
func (p *clusterPickerPage) startRow(want *k8s.ClusterRef, current string) int {
	recent := -1
	for i, r := range p.rows {
		if want != nil && r.Ref == *want {
			return i
		}
		if !r.LastUsed.IsZero() && (recent < 0 || r.LastUsed.After(p.rows[recent].LastUsed)) {
			recent = i
		}
	}
	if recent >= 0 {
		return recent
	}
	for i, r := range p.rows {
		if current != "" && r.Source == k8s.SourceKubeconfig && r.Ref.Context == current {
			return i
		}
	}
	return 0
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
			Title:   "Add kubeconfig file",
			Label:   "Path",
			OneLine: true,
			Action:  func(s string) tea.Msg { return AddKubeconfigFile{Path: unquotePath(s)} },
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
	label := clusters.Sanitize(r.Label)
	switch {
	case r.Missing:
		return msg.Toast{Text: label + ": file missing", Level: msg.LevelWarn}
	case r.NeedsReview:
		return ReviewCluster{Row: r}
	case current != nil && r.Ref == *current:
		return msg.Toast{Text: "already on " + label, Level: msg.LevelInfo}
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
				names = append(names, clusters.Sanitize(o.Ref.Context))
			}
		}
		prompt = fmt.Sprintf("Delete %s and its contexts: %s?", clusters.Sanitize(r.Key.File), strings.Join(names, ", "))
	case k8s.SourceFile:
		prompt = fmt.Sprintf("Forget %s? The file is not touched.", clusters.Sanitize(r.Ref.Path))
	default:
		return toast(clusters.Sanitize(r.Label)+" comes from your kubeconfig; kubetui doesn't edit it", msg.LevelInfo)
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

// View draws the rows. Labels, servers and errors come from kubeconfigs and are
// drawn through clusters.Sanitize: a CR in a context name would otherwise move
// the cursor back over its own row, and a line feed would add a fake one.
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

	// The banner stands above the table and does not scroll: it says why
	// something is missing from it.
	var banner []string
	note := func(st lipgloss.Style, s string) {
		for _, l := range strings.Split(ansi.Wrap(strings.Join(strings.Fields(clusters.Sanitize(s)), " "), max(width-4, 10), ""), "\n") {
			banner = append(banner, fit("  "+st.Render(l)))
		}
	}
	warn := lipgloss.NewStyle().Foreground(t.Pal.Warn)
	for _, b := range p.banner {
		st := t.Faint
		if b.Warn {
			st = warn
		}
		note(st, b.Text)
	}
	var unlisted []k8s.ClusterRef
	for ref := range p.errs {
		if !p.listed(ref) {
			unlisted = append(unlisted, ref)
		}
	}
	sort.Slice(unlisted, func(i, j int) bool { return RefName(unlisted[i]) < RefName(unlisted[j]) })
	for _, ref := range unlisted {
		note(warn, "✗ connect "+RefName(ref)+": "+p.errs[ref])
	}
	if len(banner) > 0 {
		banner = append(banner, "")
	}

	var lines []string
	lines = append(lines, fit(t.ColHeader.Render(row("  ", "NAME", "SOURCE", "SERVER", "LAST USED"))))
	cursorLine := 1
	switch {
	case !p.loaded:
		lines = append(lines, fit(t.Faint.Render("  loading…")))
	case len(p.rows) == 0 && p.noStore != "":
		lines = append(lines, fit(t.Faint.Render("  (no clusters in your kubeconfig)")))
	case len(p.rows) == 0:
		lines = append(lines, fit(t.Faint.Render("  (no clusters; press p to paste a kubeconfig)")))
	}
	for i, r := range p.rows {
		marker := "  "
		if p.isCurrent(r) {
			marker = t.AccentText.Render("● ")
		}
		server, last := clusters.Sanitize(r.Server), lastUsedText(r.LastUsed, p.now())
		name, st := clusters.Sanitize(r.Label), t.Dim
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
			lines = append(lines, fit(t.Faint.Render("    ✗ "+strings.Join(strings.Fields(clusters.Sanitize(e)), " "))))
		}
	}

	quit := "q quit"
	if !p.atLaunch {
		quit = "esc back"
	}
	hint := fit(t.Faint.Render("  enter connect · p paste kubeconfig · f load file · ctrl+d remove · " + quit))

	// The banner gives way to the table, which always keeps a line.
	banner = banner[:min(len(banner), max(height-3, 0))]
	body := height - 2 - len(banner) // blank line and footer
	if body < 1 {
		return hint
	}
	if len(lines) > body {
		off := max(0, min(cursorLine-body+2, len(lines)-body))
		lines = lines[off : off+body]
	}
	return strings.Join(append(banner, lines...), "\n") + "\n\n" + hint
}

// RefName is how a connect error names the cluster it was for: its context,
// else its file, else its source. An in-cluster ref names no context, and a
// file with no current-context to pin names none either.
func RefName(ref k8s.ClusterRef) string {
	switch {
	case ref.Context != "":
		return clusters.Sanitize(ref.Context)
	case ref.Path != "":
		return clusters.Sanitize(filepath.Base(ref.Path))
	}
	return string(ref.Source)
}
