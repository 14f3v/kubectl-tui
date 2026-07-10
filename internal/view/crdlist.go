package view

import (
	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/dynbrowse"
	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/style"
)

type crdListMsg struct {
	crds []dynbrowse.CRDInfo
	err  error
}

// crdListPage lists the cluster's CustomResourceDefinitions; enter opens the
// selected CRD's instances via the Table-protocol browse page. Pushed by :crds.
type crdListPage struct {
	sess      *k8s.Session
	theme     style.Theme
	namespace string
	readOnly  bool

	table   *component.Table
	byName  map[string]dynbrowse.CRDInfo
	allRows []columns.Row
	filter  string
	loaded  bool
	errMsg  string
}

// crdListCols is the CRD index's fixed column layout; crdListTitles mirrors its
// headers for "col:" filter scoping.
var crdListCols = []columns.Column{
	{Title: "NAME", MinWidth: 30, Grow: 3, Align: columns.AlignLeft},
	{Title: "GROUP", MinWidth: 20, Grow: 1, Align: columns.AlignLeft},
	{Title: "KIND", MinWidth: 18, Align: columns.AlignLeft},
	{Title: "SCOPE", MinWidth: 11, Align: columns.AlignLeft},
	{Title: "AGE", MinWidth: 5, Align: columns.AlignRight},
}

var crdListTitles = colTitlesOf(crdListCols)

// NewCRDList builds the CRD index page.
func NewCRDList(sess *k8s.Session, theme style.Theme, namespace string, readOnly bool) *crdListPage {
	t := component.NewTable(theme)
	t.SetColumns(crdListCols)
	return &crdListPage{sess: sess, theme: theme, namespace: namespace, readOnly: readOnly, table: t, byName: map[string]dynbrowse.CRDInfo{}}
}

func (p *crdListPage) Init() tea.Cmd     { return nil }
func (p *crdListPage) Title() string     { return "custom resource definitions" }
func (p *crdListPage) Kind() string      { return "" }
func (p *crdListPage) Namespace() string { return p.namespace }
func (p *crdListPage) Filter() string    { return p.filter }
func (p *crdListPage) OnLeave()          {}
func (p *crdListPage) Summary() Summary  { return Summary{Total: p.table.RowCount()} }

func (p *crdListPage) SetFilter(f string) {
	p.filter = f
	p.table.SetRows(filterRows(p.allRows, p.filter, crdListTitles))
}

func (p *crdListPage) OnEnter() tea.Cmd {
	sess := p.sess
	return func() tea.Msg {
		crds, err := dynbrowse.ListCRDs(sess.Context(), sess.Dyn)
		return crdListMsg{crds: crds, err: err}
	}
}

func (p *crdListPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "open")),
		keySortNext, keySortDir,
		key.NewBinding(key.WithKeys("r"), key.WithHelp("r", "refresh")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *crdListPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case crdListMsg:
		p.loaded = true
		if t.err != nil {
			p.errMsg = t.err.Error()
			return p, nil
		}
		p.errMsg = ""
		p.byName = make(map[string]dynbrowse.CRDInfo, len(t.crds))
		rows := make([]columns.Row, 0, len(t.crds))
		for _, c := range t.crds {
			p.byName[c.Name] = c
			rows = append(rows, columns.Row{
				UID: c.Name, Name: c.Name, Health: columns.StatusNeutral,
				Cells: []columns.Cell{
					{Text: c.Name, Role: columns.RoleName},
					{Text: c.Group},
					{Text: c.Kind},
					{Text: c.Scope},
					{Text: shortAge(c.Created), Status: columns.StatusMuted},
				},
				SortKeys: []columns.SortKey{
					columns.StrKey(c.Name), columns.StrKey(c.Group), columns.StrKey(c.Kind),
					columns.StrKey(c.Scope), columns.NumKey(float64(c.Created.Unix())),
				},
			})
		}
		p.allRows = rows
		p.table.SetRows(filterRows(p.allRows, p.filter, crdListTitles))
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(t)
	}
	return p, nil
}

func (p *crdListPage) handleKey(k tea.KeyPressMsg) (Page, tea.Cmd) {
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
	case k.String() == "r":
		return p, p.OnEnter()
	case key.Matches(k, keyEnter):
		return p, p.openSelected()
	}
	return p, nil
}

func (p *crdListPage) openSelected() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	c, ok := p.byName[row.Name]
	if !ok {
		return nil
	}
	title := c.Kind + " (" + c.Group + ")"
	page := NewCRDBrowse(p.sess, p.theme, title, c.GVR(), c.Namespaced, p.namespace, p.readOnly)
	return func() tea.Msg { return PushMsg{Page: page} }
}

func (p *crdListPage) View(width, height int) string {
	if !p.loaded {
		return p.theme.Faint.Render("  discovering CRDs…")
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
