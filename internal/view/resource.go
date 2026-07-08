package view

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/action/inspect"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/component"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/msg"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/style"
)

// resourcePage is the generic table page shared by every core kind. It watches
// one engine kind, renders a windowed table, and applies a live filter. Kind-
// specific behavior (extra keys, drill-in) is layered by wrapping this page.
type resourcePage struct {
	kind  string
	title string

	sess  deps
	theme style.Theme
	table *component.Table

	viewID    uint64
	namespace string

	remote  engine.Remote[columns.Row]
	allRows []columns.Row
	filter  string
}

// deps mirrors Deps but lets resourcePage store the session without re-importing.
type deps = Deps

// Option customizes a resource page at construction.
type Option func(*resourcePage)

// WithInitialSort opens the page sorted by a column and direction (e.g. events
// newest-first) instead of the default NAME-ascending.
func WithInitialSort(col int, desc bool) Option {
	return func(p *resourcePage) { p.table.SetSortState(col, desc) }
}

// newResourcePage builds a generic page for a kind.
func newResourcePage(kind, title string, d Deps, opts ...Option) *resourcePage {
	tbl := component.NewTable(d.Theme)
	if proj := columns.For(kind); proj != nil {
		tbl.SetColumns(proj.Columns())
	}
	p := &resourcePage{
		kind:      kind,
		title:     title,
		sess:      d,
		theme:     d.Theme,
		table:     tbl,
		namespace: d.Namespace,
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *resourcePage) Init() tea.Cmd { return nil }

func (p *resourcePage) Title() string     { return p.title }
func (p *resourcePage) Kind() string      { return p.kind }
func (p *resourcePage) Namespace() string { return p.namespace }
func (p *resourcePage) Filter() string    { return p.filter }

func (p *resourcePage) OnEnter() tea.Cmd {
	vs, err := p.sess.Session.Engine.Ensure(p.kind, p.namespace)
	if err != nil {
		return func() tea.Msg { return msg.Toast{Text: err.Error(), Level: msg.LevelError} }
	}
	p.viewID = p.sess.Session.Engine.NextViewID()
	vs.SetViewID(p.viewID)
	// Paint the current cache immediately; further snapshots arrive via the sink.
	p.apply(vs.Snapshot())
	return nil
}

func (p *resourcePage) OnLeave() {
	p.sess.Session.Engine.StopIfScreenScoped(p.kind)
}

func (p *resourcePage) SetFilter(f string) {
	p.filter = f
	p.reapplyFilter()
}

func (p *resourcePage) Summary() Summary {
	total, ok, warn, errc := statusCounts(p.allRows)
	return Summary{
		Total:    total,
		OK:       ok,
		Warn:     warn,
		Err:      errc,
		Phase:    p.remote.Phase,
		Error:    p.remote.Err,
		SyncedAt: p.remote.SyncedAt,
	}
}

func (p *resourcePage) Keys() []key.Binding {
	return []key.Binding{
		keyEnter, keyDescribe, keyLogs, keyShell, keyYAML,
		keyEdit, keyDelete, keyKill, keyPortFwd,
	}
}

func (p *resourcePage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case engine.SnapshotMsg:
		if t.Kind == p.kind && t.ViewID == p.viewID {
			p.apply(t.Snap)
		}
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(t)
	}
	return p, nil
}

func (p *resourcePage) handleKey(k tea.KeyPressMsg) (Page, tea.Cmd) {
	switch {
	case key.Matches(k, keyUp):
		p.table.MoveUp()
	case key.Matches(k, keyDown):
		p.table.MoveDown()
	case key.Matches(k, keyPageUp):
		p.table.PageUp()
	case key.Matches(k, keyPageDown):
		p.table.PageDown()
	case key.Matches(k, keyHome):
		p.table.Home()
	case key.Matches(k, keyEnd):
		p.table.End()
	case key.Matches(k, keyYAML):
		return p, p.yamlAction()
	case key.Matches(k, keyDescribe):
		return p, p.describeAction()
	case key.Matches(k, keyLogs):
		return p, p.logsAction()
	case isPendingAction(k):
		// Handlers wired in later phases; acknowledge so the binding is discoverable.
		if _, ok := p.table.Selected(); ok {
			return p, func() tea.Msg {
				return msg.Toast{Text: k.String() + ": coming soon", Level: msg.LevelInfo}
			}
		}
	}
	return p, nil
}

func isPendingAction(k tea.KeyPressMsg) bool {
	return key.Matches(k, keyEnter, keyShell,
		keyEdit, keyDelete, keyKill, keyPortFwd)
}

// logsAction follows the selected pod's logs in a new page. Logs are pod-only.
func (p *resourcePage) logsAction() tea.Cmd {
	if p.kind != "pods" {
		return toast("logs: select a pod", msg.LevelInfo)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	page := NewLogsPage(p.sess.Session, p.theme, row.Namespace, row.Name, "")
	return func() tea.Msg { return PushMsg{Page: page} }
}

// yamlAction reads the selected object from the informer cache and pushes a YAML
// text page. It runs synchronously (the object is already cached).
func (p *resourcePage) yamlAction() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	vs := p.sess.Session.Engine.Get(p.kind)
	if vs == nil {
		return toast("no live data for "+p.kind, msg.LevelError)
	}
	obj, ok := vs.Get(row.Namespace, row.Name)
	if !ok {
		return toast(row.Name+" is no longer in the cache", msg.LevelWarn)
	}
	yamlStr, err := inspect.YAML(obj)
	if err != nil {
		return toast("yaml: "+err.Error(), msg.LevelError)
	}
	tv := NewTextView(row.Name+" · yaml", yamlStr, p.theme)
	return func() tea.Msg { return PushMsg{Page: tv} }
}

// describeAction runs kubectl-style describe as a command (it makes its own API
// calls) and pushes the result as a text page.
func (p *resourcePage) describeAction() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	cfg := p.sess.Session.RestCfg
	kind, ns, name, theme := p.kind, row.Namespace, row.Name, p.theme
	return func() tea.Msg {
		out, err := inspect.Describe(cfg, kind, ns, name)
		if err != nil {
			return msg.Toast{Text: "describe: " + err.Error(), Level: msg.LevelError}
		}
		return PushMsg{Page: NewTextView(name+" · describe", out, theme)}
	}
}

func toast(text string, level msg.Level) tea.Cmd {
	return func() tea.Msg { return msg.Toast{Text: text, Level: level} }
}

func (p *resourcePage) View(width, height int) string {
	if height < 2 {
		height = 2
	}
	p.table.SetSize(width, height-1) // one line for the column header
	return p.table.Header() + "\n" + p.table.Body()
}

// apply stores a new snapshot and refreshes the filtered view.
func (p *resourcePage) apply(snap engine.Remote[columns.Row]) {
	p.remote = snap
	p.allRows = snap.Rows
	p.reapplyFilter()
}

func (p *resourcePage) reapplyFilter() {
	rows := filterRows(p.allRows, p.filter)
	p.table.SetRows(rows)
}

// filterRows applies the live filter: case-insensitive substring over the row's
// name and cell text, with a leading "!" inverting the match.
func filterRows(rows []columns.Row, filter string) []columns.Row {
	if filter == "" {
		return rows
	}
	invert := false
	term := filter
	if strings.HasPrefix(term, "!") {
		invert = true
		term = term[1:]
	}
	term = strings.ToLower(term)
	if term == "" {
		return rows
	}
	out := make([]columns.Row, 0, len(rows))
	for _, r := range rows {
		if rowMatches(r, term) != invert {
			out = append(out, r)
		}
	}
	return out
}

func rowMatches(r columns.Row, term string) bool {
	if strings.Contains(strings.ToLower(r.Name), term) {
		return true
	}
	for _, c := range r.Cells {
		if strings.Contains(strings.ToLower(c.Text), term) {
			return true
		}
	}
	return false
}

// statusCounts tallies rows by their health class, for the header count line.
func statusCounts(rows []columns.Row) (total, ok, warn, errc int) {
	total = len(rows)
	for _, r := range rows {
		switch r.Health {
		case columns.StatusOK:
			ok++
		case columns.StatusWarn:
			warn++
		case columns.StatusError:
			errc++
		}
	}
	return total, ok, warn, errc
}
