package view

import (
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/rest"

	"github.com/14f3v/kubectl-tui/internal/action/portfwd"
	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// testSession assembles a Session from fake clients. Every field the view layer
// touches is either already an interface (CS/Dyn/Disco/Metrics) or has a public
// constructor (Engine/Forwards), so no test-only constructor is needed — the only
// thing that ever blocked this was Context() returning nil.
func testSession(t *testing.T) *k8s.Session {
	t.Helper()
	cs := fake.NewClientset()
	sink := func(tea.Msg) {}
	cfg := &rest.Config{Host: "https://example.invalid"}
	return &k8s.Session{
		RestCfg:  cfg,
		CS:       cs,
		Disco:    cs.Discovery(),
		Engine:   engine.NewEngine(t.Context(), sink),
		Forwards: portfwd.NewManager(cfg, cs, sink),
		Identity: k8s.Identity{Context: "test", Cluster: "test"},
	}
}

// actionPage builds a resource page backed by fakes, with one selectable row.
func actionPage(t *testing.T, kind string, readOnly bool) *resourcePage {
	t.Helper()
	p := newResourcePage(kind, kind, Deps{
		Session:  testSession(t),
		Theme:    style.Default(),
		ReadOnly: readOnly,
	})
	p.table = component.NewTable(style.Default())
	if proj := columns.For(kind); proj != nil {
		p.table.SetColumns(proj.Columns())
	}
	p.allRows = []columns.Row{{
		UID: "u1", Namespace: "demo", Name: "web", Version: "1",
		Cells: []columns.Cell{{Text: "web"}}, SortKeys: []columns.SortKey{columns.StrKey("web")},
	}}
	p.table.SetSize(120, 10)
	p.table.SetRows(p.allRows)
	return p
}

// runToast executes a command and returns its toast text, or "" if it produced
// something else (a push, a confirm, a prompt).
func runToast(cmd tea.Cmd) string {
	if cmd == nil {
		return ""
	}
	if m, ok := cmd().(msg.Toast); ok {
		return m.Text
	}
	return ""
}

// Every mutating action must refuse in read-only mode. This is the check that was
// missing when exec, file copy and port-forward shipped ungated: the actions
// existed, the flag existed, and nothing ever asserted the two met.
func TestEveryMutatingActionRefusesInReadOnlyMode(t *testing.T) {
	actions := []struct {
		name string
		kind string
		run  func(p *resourcePage) tea.Cmd
	}{
		{"delete", "pods", func(p *resourcePage) tea.Cmd { return p.deleteAction(false) }},
		{"kill", "pods", func(p *resourcePage) tea.Cmd { return p.deleteAction(true) }},
		{"edit", "pods", func(p *resourcePage) tea.Cmd { return p.editAction() }},
		{"shell", "pods", func(p *resourcePage) tea.Cmd { return p.shellAction() }},
		{"copy", "pods", func(p *resourcePage) tea.Cmd { return p.copyAction() }},
		{"debug", "pods", func(p *resourcePage) tea.Cmd { return p.debugAction() }},
		{"port-forward", "pods", func(p *resourcePage) tea.Cmd { return p.portForwardAction() }},
		{"label", "pods", func(p *resourcePage) tea.Cmd { return p.labelAction() }},
		{"set", "pods", func(p *resourcePage) tea.Cmd { return p.setAction() }},
		{"scale", "deployments", func(p *resourcePage) tea.Cmd { return p.scaleAction() }},
		{"csr-approve", "certificatesigningrequests", func(p *resourcePage) tea.Cmd { return p.csrDecision(true) }},
		{"csr-deny", "certificatesigningrequests", func(p *resourcePage) tea.Cmd { return p.csrDecision(false) }},
	}
	for _, a := range actions {
		got := runToast(a.run(actionPage(t, a.kind, true)))
		if !strings.Contains(got, "read-only") {
			t.Errorf("%s in read-only mode: got %q, want a read-only refusal", a.name, got)
		}
	}
}

// The same actions must NOT refuse when read-only is off — otherwise the test
// above could pass with everything permanently disabled.
func TestMutatingActionsAreAllowedWhenNotReadOnly(t *testing.T) {
	for _, a := range []struct {
		name string
		kind string
		run  func(p *resourcePage) tea.Cmd
	}{
		{"delete", "pods", func(p *resourcePage) tea.Cmd { return p.deleteAction(false) }},
		{"shell", "pods", func(p *resourcePage) tea.Cmd { return p.shellAction() }},
		{"copy", "pods", func(p *resourcePage) tea.Cmd { return p.copyAction() }},
		{"label", "pods", func(p *resourcePage) tea.Cmd { return p.labelAction() }},
		{"scale", "deployments", func(p *resourcePage) tea.Cmd { return p.scaleAction() }},
	} {
		if got := runToast(a.run(actionPage(t, a.kind, false))); strings.Contains(got, "read-only") {
			t.Errorf("%s refused with read-only off: %q", a.name, got)
		}
	}
}

// Read-only must not touch the read paths — those are the whole point of the mode.
func TestReadActionsWorkInReadOnlyMode(t *testing.T) {
	for _, a := range []struct {
		name string
		run  func(p *resourcePage) tea.Cmd
	}{
		{"yaml", func(p *resourcePage) tea.Cmd { return p.yamlAction() }},
		{"describe", func(p *resourcePage) tea.Cmd { return p.describeAction() }},
		{"logs", func(p *resourcePage) tea.Cmd { return p.logsAction() }},
	} {
		if got := runToast(a.run(actionPage(t, "pods", true))); strings.Contains(got, "read-only") {
			t.Errorf("%s was refused in read-only mode, but it only reads: %q", a.name, got)
		}
	}
}

// The rollout key opens a menu rather than mutating, so it is not gated itself —
// Status and History are reads. The gate belongs on the menu's items, and the
// page must be told which mode it is in.
func TestRolloutMenuOpensButCarriesReadOnly(t *testing.T) {
	cmd := actionPage(t, "deployments", true).rolloutAction()
	if cmd == nil {
		t.Fatal("rolloutAction returned nil")
	}
	push, ok := cmd().(PushMsg)
	if !ok {
		t.Fatalf("rolloutAction did not push a page, got %T", cmd())
	}
	rp, ok := push.Page.(*rolloutPage)
	if !ok {
		t.Fatalf("pushed %T, want *rolloutPage", push.Page)
	}
	if !rp.readOnly {
		t.Error("the rollout menu did not inherit read-only, so its items would mutate")
	}
}

// Kind gates: an action offered only for some kinds must say so, not act.
func TestActionsRejectTheWrongKind(t *testing.T) {
	if got := runToast(actionPage(t, "configmaps", false).shellAction()); !strings.Contains(got, "pod") {
		t.Errorf("shell on configmaps: %q, want a select-a-pod message", got)
	}
	if got := runToast(actionPage(t, "configmaps", false).copyAction()); !strings.Contains(got, "pod") {
		t.Errorf("copy on configmaps: %q, want a select-a-pod message", got)
	}
	// approve/deny on a non-CSR kind is a silent no-op today; pin that it at least
	// does not act on the wrong object.
	if cmd := actionPage(t, "pods", false).csrDecision(true); cmd != nil {
		if txt := runToast(cmd); txt != "" && !strings.Contains(strings.ToLower(txt), "csr") {
			t.Errorf("approve on pods produced %q", txt)
		}
	}
}

// Bulk delete must target the marked rows when there are marks, and the cursor row
// otherwise — the selection logic behind ctrl+d, which had no test.
func TestDeleteTargetsMarkedRowsOtherwiseTheCursorRow(t *testing.T) {
	p := actionPage(t, "pods", false)
	p.allRows = append(p.allRows, columns.Row{
		UID: "u2", Namespace: "demo", Name: "api", Version: "1",
		Cells: []columns.Cell{{Text: "api"}}, SortKeys: []columns.SortKey{columns.StrKey("api")},
	})
	p.table.SetRows(p.allRows)

	// No marks: the confirm names the row under the cursor.
	cmd := p.deleteAction(false)
	if cmd == nil {
		t.Fatal("deleteAction returned nil")
	}
	req, ok := cmd().(ConfirmRequest)
	if !ok {
		t.Fatalf("deleteAction did not ask for confirmation, got %T", cmd())
	}
	sel, _ := p.table.Selected()
	if !strings.Contains(req.Prompt+req.Title, sel.Name) {
		t.Errorf("unmarked delete prompt %q/%q does not name the cursor row %q", req.Title, req.Prompt, sel.Name)
	}

	// With marks, the prompt must reflect the marked set, not the cursor.
	// SetRows restores the cursor by UID, which leaves it on the last row here, so
	// start from the top before walking down to mark two distinct rows.
	p.table.Home()
	p.table.ToggleMark()
	p.table.MoveDown()
	p.table.ToggleMark()
	if n := p.table.MarkedCount(); n != 2 {
		t.Fatalf("marked %d rows, want 2", n)
	}
	req2, ok := p.deleteAction(false)().(ConfirmRequest)
	if !ok {
		t.Fatal("marked delete did not ask for confirmation")
	}
	if !strings.Contains(req2.Title+req2.Prompt, "2") {
		t.Errorf("marked delete prompt %q/%q does not mention the 2 marked rows", req2.Title, req2.Prompt)
	}
	if !req2.Danger {
		t.Error("a bulk delete confirm is not marked dangerous")
	}
}
