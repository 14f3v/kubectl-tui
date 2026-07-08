package view

import (
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func row(name, status string, class columns.StatusClass) columns.Row {
	return columns.Row{
		Name:   name,
		UID:    "uid-" + name,
		Health: class,
		Cells: []columns.Cell{
			{Text: name, Role: columns.RoleName},
			{Text: status, Role: columns.RoleStatus, Status: class},
		},
	}
}

func TestFilterRows(t *testing.T) {
	rows := []columns.Row{
		row("checkout-api", "Running", columns.StatusOK),
		row("payments-worker", "Running", columns.StatusOK),
		row("notifications", "CrashLoopBackOff", columns.StatusError),
	}

	if got := filterRows(rows, ""); len(got) != 3 {
		t.Fatalf("empty filter: got %d, want 3", len(got))
	}
	if got := filterRows(rows, "pay"); len(got) != 1 || got[0].Name != "payments-worker" {
		t.Fatalf("substring filter: got %v", got)
	}
	// Match on a cell value, not just the name.
	if got := filterRows(rows, "crashloop"); len(got) != 1 || got[0].Name != "notifications" {
		t.Fatalf("cell-value filter: got %v", got)
	}
	// Inverse filter.
	if got := filterRows(rows, "!running"); len(got) != 1 || got[0].Name != "notifications" {
		t.Fatalf("inverse filter: got %v", got)
	}
}

// newBarePage constructs a resourcePage without a Session, for rendering tests
// (View does not touch the Session — only OnEnter does).
func newBarePage(kind string) *resourcePage {
	th := style.Default()
	tbl := component.NewTable(th)
	if proj := columns.For(kind); proj != nil {
		tbl.SetColumns(proj.Columns())
	}
	return &resourcePage{kind: kind, title: kind, theme: th, table: tbl}
}

func TestResourcePageView(t *testing.T) {
	p := newBarePage("pods")
	p.apply(engine.Remote[columns.Row]{
		Phase: engine.PhaseReady,
		Rows: []columns.Row{
			row("checkout-api", "Running", columns.StatusOK),
			row("notifications", "CrashLoopBackOff", columns.StatusError),
		},
	})
	out := p.View(120, 12)
	lines := strings.Split(out, "\n")
	if len(lines) != 12 {
		t.Fatalf("view line count = %d, want 12", len(lines))
	}
	if !strings.Contains(out, "checkout-api") || !strings.Contains(out, "NAME") {
		t.Fatalf("view missing expected content:\n%s", out)
	}
	// Summary must reflect the rows.
	s := p.Summary()
	if s.Total != 2 || s.OK != 1 || s.Err != 1 {
		t.Fatalf("summary = %+v, want total 2 ok 1 err 1", s)
	}
}

func TestStatusCounts(t *testing.T) {
	rows := []columns.Row{
		row("a", "Running", columns.StatusOK),
		row("b", "Running", columns.StatusOK),
		row("c", "Pending", columns.StatusWarn),
		row("d", "CrashLoopBackOff", columns.StatusError),
	}
	total, ok, warn, errc := statusCounts(rows)
	if total != 4 || ok != 2 || warn != 1 || errc != 1 {
		t.Fatalf("counts = %d/%d/%d/%d, want 4/2/1/1", total, ok, warn, errc)
	}
}
