package component

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/style"
)

func testCols() []columns.Column {
	return []columns.Column{
		{Title: "NAME", MinWidth: 10, Grow: 3},
		{Title: "STATUS", MinWidth: 8, Grow: 1},
		{Title: "AGE", MinWidth: 4, Align: columns.AlignRight},
	}
}

func testRows(n int) []columns.Row {
	out := make([]columns.Row, n)
	for i := 0; i < n; i++ {
		name := string(rune('a'+i)) + "-pod"
		out[i] = columns.Row{
			UID: name, Name: name, Version: "1",
			Cells: []columns.Cell{
				{Text: name, Role: columns.RoleName},
				{Text: "Running", Role: columns.RoleStatus, Status: columns.StatusOK},
				{Text: "3h", Status: columns.StatusMuted},
			},
			SortKeys: []columns.SortKey{columns.StrKey(name), columns.StrKey("Running"), columns.NumKey(float64(i))},
		}
	}
	return out
}

func newTestTable() *Table {
	tbl := NewTable(style.Default())
	tbl.SetColumns(testCols())
	tbl.SetSize(80, 5)
	return tbl
}

func TestTableBodyLineCount(t *testing.T) {
	tbl := newTestTable()
	tbl.SetRows(testRows(3))
	body := tbl.Body()
	lines := strings.Split(body, "\n")
	// Body is padded to the visible height (5) even with only 3 rows.
	if len(lines) != 5 {
		t.Fatalf("body line count = %d, want 5", len(lines))
	}
}

func TestTableSelectionAndNav(t *testing.T) {
	tbl := newTestTable()
	tbl.SetRows(testRows(3))
	if r, ok := tbl.Selected(); !ok || r.Name != "a-pod" {
		t.Fatalf("initial selection = %v/%v, want a-pod", r.Name, ok)
	}
	tbl.MoveDown()
	tbl.MoveDown()
	if r, _ := tbl.Selected(); r.Name != "c-pod" {
		t.Fatalf("after 2x down = %v, want c-pod", r.Name)
	}
	// Clamp at the bottom.
	tbl.MoveDown()
	if r, _ := tbl.Selected(); r.Name != "c-pod" {
		t.Fatalf("clamp at bottom = %v, want c-pod", r.Name)
	}
}

func TestTableSelectionPreservedAcrossSetRows(t *testing.T) {
	tbl := newTestTable()
	tbl.SetRows(testRows(3))
	tbl.MoveDown() // select b-pod
	// A new snapshot with the same rows must keep b-pod selected.
	tbl.SetRows(testRows(3))
	if r, _ := tbl.Selected(); r.Name != "b-pod" {
		t.Fatalf("selection lost across SetRows: got %v, want b-pod", r.Name)
	}
}

func TestTableWidthsFit(t *testing.T) {
	tbl := newTestTable()
	tbl.SetRows(testRows(1))
	header := tbl.Header()
	// The header must not exceed the terminal width.
	if w := lipgloss.Width(header); w > 80 {
		t.Fatalf("header width = %d, want <= 80", w)
	}
}

func TestTableSort(t *testing.T) {
	tbl := newTestTable()
	tbl.SetRows(testRows(3))
	// Switching to a new column sorts it ascending.
	tbl.SetSort(1)
	if col, desc := tbl.SortColumn(); col != 1 || desc {
		t.Fatalf("after SetSort(1) = col %d desc %v, want 1/false", col, desc)
	}
	// Selecting the same column again toggles to descending.
	tbl.SetSort(1)
	if col, desc := tbl.SortColumn(); col != 1 || !desc {
		t.Fatalf("after toggle = col %d desc %v, want 1/true", col, desc)
	}
}
