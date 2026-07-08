package view

import (
	"testing"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"
)

func row(name, status string, class columns.StatusClass) columns.Row {
	return columns.Row{
		Name: name,
		UID:  "uid-" + name,
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
