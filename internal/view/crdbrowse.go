package view

import (
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/client-go/dynamic"

	"github.com/14f3v/kubectl-tui/internal/action/dynbrowse"
	"github.com/14f3v/kubectl-tui/internal/action/inspect"
	"github.com/14f3v/kubectl-tui/internal/action/write"
	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// crdRefresh is how often a CRD instance table re-fetches. The Table protocol is
// a List, not a watch, so we poll; the interval is a balance between freshness
// and API load.
const crdRefresh = 3 * time.Second

// crdTokenSeq gives each browse page a unique token so a popped page's in-flight
// ticks/fetches (which route to whatever page is now active) are ignored.
var crdTokenSeq int

type crdTickMsg struct{ token int }

type crdTableMsg struct {
	token int
	cols  []columns.Column
	rows  []columns.Row
	err   error
}

// crdBrowsePage browses any resource kind (typically a CRD) via the server-side
// Table protocol: it periodically fetches a metav1.Table, renders it with the
// shared table component (so sort and regex filter work), and offers yaml/delete.
type crdBrowsePage struct {
	sess       *k8s.Session
	theme      style.Theme
	title      string
	gvr        schema.GroupVersionResource
	namespaced bool
	namespace  string
	readOnly   bool
	token      int

	table     *component.Table
	allRows   []columns.Row
	colTitles []string // server column headers, for "col:" filter scoping
	filter    string
	loaded    bool
	errMsg    string
	lastAt    time.Time
}

// NewCRDBrowse builds a Table-protocol browse page for a resource kind.
func NewCRDBrowse(sess *k8s.Session, theme style.Theme, title string, gvr schema.GroupVersionResource, namespaced bool, namespace string, readOnly bool) *crdBrowsePage {
	crdTokenSeq++
	return &crdBrowsePage{
		sess: sess, theme: theme, title: title, gvr: gvr,
		namespaced: namespaced, namespace: namespace, readOnly: readOnly,
		token: crdTokenSeq, table: component.NewTable(theme),
	}
}

func (p *crdBrowsePage) Init() tea.Cmd     { return nil }
func (p *crdBrowsePage) Title() string     { return p.title }
func (p *crdBrowsePage) Kind() string      { return "" }
func (p *crdBrowsePage) Namespace() string { return p.namespace }
func (p *crdBrowsePage) Filter() string    { return p.filter }
func (p *crdBrowsePage) OnLeave()          {}
func (p *crdBrowsePage) Summary() Summary {
	return Summary{Total: p.table.RowCount(), SyncedAt: p.lastAt}
}

func (p *crdBrowsePage) SetFilter(f string) {
	p.filter = f
	p.reapply()
}

// OnEnter kicks off the first fetch and the refresh tick.
func (p *crdBrowsePage) OnEnter() tea.Cmd {
	return tea.Batch(p.fetchCmd(), p.tickCmd())
}

func (p *crdBrowsePage) tickCmd() tea.Cmd {
	token := p.token
	return tea.Tick(crdRefresh, func(time.Time) tea.Msg { return crdTickMsg{token: token} })
}

func (p *crdBrowsePage) fetchCmd() tea.Cmd {
	sess, gvr, nsd, ns, token := p.sess, p.gvr, p.namespaced, p.namespace, p.token
	return func() tea.Msg {
		t, err := dynbrowse.FetchTable(sess.Context(), sess.Disco.RESTClient(), gvr, nsd, ns)
		if err != nil {
			return crdTableMsg{token: token, err: err}
		}
		cols, rows := dynbrowse.ToColumns(t)
		return crdTableMsg{token: token, cols: cols, rows: rows}
	}
}

func (p *crdBrowsePage) Keys() []key.Binding {
	return []key.Binding{
		keyYAML, keyDescribe, keyDelete, keySortNext, keySortDir,
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *crdBrowsePage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case crdTableMsg:
		if t.token != p.token {
			return p, nil
		}
		p.loaded = true
		p.lastAt = time.Now()
		if t.err != nil {
			p.errMsg = t.err.Error()
			return p, nil
		}
		p.errMsg = ""
		p.table.SetColumns(t.cols)
		p.colTitles = colTitlesOf(t.cols)
		p.allRows = t.rows
		p.reapply()
		return p, nil
	case crdTickMsg:
		if t.token != p.token {
			return p, nil // stale tick from a page that was left; let its chain die
		}
		return p, tea.Batch(p.fetchCmd(), p.tickCmd())
	case tea.KeyPressMsg:
		return p.handleKey(t)
	}
	return p, nil
}

func (p *crdBrowsePage) handleKey(k tea.KeyPressMsg) (Page, tea.Cmd) {
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
	case key.Matches(k, keySortNext):
		cycleTableSort(p.table)
	case key.Matches(k, keySortDir):
		toggleTableSortDir(p.table)
	case key.Matches(k, keyYAML):
		return p, p.yamlAction()
	case key.Matches(k, keyDescribe):
		// kubectl has no describer for arbitrary CRDs; yaml carries the full object.
		return p, toast("describe is unavailable for custom resources — press y for yaml", msg.LevelInfo)
	case key.Matches(k, keyDelete):
		return p, p.deleteAction()
	case k.String() == "r":
		return p, p.fetchCmd()
	}
	return p, nil
}

func (p *crdBrowsePage) reapply() {
	p.table.SetRows(filterRows(p.allRows, p.filter, p.colTitles))
}

// yamlAction fetches the selected object via the dynamic client and shows its YAML.
func (p *crdBrowsePage) yamlAction() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	sess, gvr, nsd, ns, name, theme := p.sess, p.gvr, p.namespaced, row.Namespace, row.Name, p.theme
	return func() tea.Msg {
		var ri dynamic.ResourceInterface = sess.Dyn.Resource(gvr)
		if nsd {
			ri = sess.Dyn.Resource(gvr).Namespace(ns)
		}
		obj, err := ri.Get(sess.Context(), name, metav1.GetOptions{})
		if err != nil {
			return msg.Toast{Text: "yaml: " + err.Error(), Level: msg.LevelError}
		}
		y, err := inspect.YAML(obj)
		if err != nil {
			return msg.Toast{Text: "yaml: " + err.Error(), Level: msg.LevelError}
		}
		return PushMsg{Page: NewTextView(name+" · yaml", y, theme)}
	}
}

// deleteAction confirms and deletes the selected custom resource.
func (p *crdBrowsePage) deleteAction() tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	sess, gvr, nsd, ns, name := p.sess, p.gvr, p.namespaced, row.Namespace, row.Name
	act := func() tea.Msg {
		if err := write.Delete(sess.Context(), sess.Dyn, gvr, nsd, ns, name, false); err != nil {
			return msg.Toast{Text: "delete " + name + ": " + err.Error(), Level: msg.LevelError}
		}
		return msg.Toast{Text: name + " deleted", Level: msg.LevelSuccess}
	}
	return func() tea.Msg {
		return ConfirmRequest{Title: "Delete " + name, Prompt: "Delete " + name + "?", Danger: true, Action: act}
	}
}

func (p *crdBrowsePage) View(width, height int) string {
	if !p.loaded {
		return p.theme.Faint.Render("  loading…")
	}
	if p.errMsg != "" {
		return p.theme.Faint.Render("  " + trunc(p.errMsg, width-4))
	}
	if height < 2 {
		height = 2
	}
	p.table.SetSize(width, height-1)
	return p.table.Header() + "\n" + p.table.Body()
}

// cycleTableSort / toggleTableSortDir mirror the resource table's sort keys for
// any page that drives a component.Table directly.
func cycleTableSort(t *component.Table) {
	n := t.ColumnCount()
	if n == 0 {
		return
	}
	col, _ := t.SortColumn()
	t.SetSortState((col+1)%n, false)
}

func toggleTableSortDir(t *component.Table) {
	col, _ := t.SortColumn()
	t.SetSort(col)
}
