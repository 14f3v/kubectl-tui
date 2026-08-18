package component

import (
	"strings"
	"testing"

	"charm.land/lipgloss/v2"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/style"
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

func TestTableMultiSelect(t *testing.T) {
	tbl := newTestTable()
	tbl.SetRows(testRows(3)) // a-pod, b-pod, c-pod

	if tbl.MarkedCount() != 0 || tbl.MarkedRows() != nil {
		t.Fatalf("fresh table should have no marks")
	}
	tbl.ToggleMark() // mark a-pod
	tbl.MoveDown()   // cursor to b-pod
	tbl.MoveDown()   // cursor to c-pod
	tbl.ToggleMark() // mark c-pod
	if tbl.MarkedCount() != 2 {
		t.Fatalf("MarkedCount = %d, want 2", tbl.MarkedCount())
	}
	got := tbl.MarkedRows()
	if len(got) != 2 || got[0].Name != "a-pod" || got[1].Name != "c-pod" {
		t.Fatalf("MarkedRows = %v, want [a-pod c-pod] in display order", got)
	}

	// Toggling a marked row clears just that one.
	tbl.ToggleMark() // c-pod is under the cursor -> unmark
	if tbl.MarkedCount() != 1 {
		t.Fatalf("after unmark, MarkedCount = %d, want 1", tbl.MarkedCount())
	}

	// A mark on a since-removed object is pruned by MarkedRows.
	tbl.Home()
	tbl.ToggleMark() // re-mark a-pod (count now 1: a-pod)
	tbl.SetRows(testRows(0))
	if rows := tbl.MarkedRows(); rows != nil {
		t.Fatalf("marks should prune when rows vanish, got %v", rows)
	}
	if tbl.MarkedCount() != 0 {
		t.Fatalf("pruned MarkedCount = %d, want 0", tbl.MarkedCount())
	}

	// ClearMarks drops everything.
	tbl.SetRows(testRows(3))
	tbl.ToggleMark()
	tbl.ClearMarks()
	if tbl.MarkedCount() != 0 {
		t.Fatalf("ClearMarks left %d marks", tbl.MarkedCount())
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

// BenchmarkTableRender renders a windowed view over a large row set to confirm
// per-frame cost stays low (only the visible window is drawn). Phase-2 gate.
func BenchmarkTableRender5k(b *testing.B) {
	tbl := NewTable(style.Default())
	tbl.SetColumns(testCols())
	tbl.SetSize(120, 40)
	tbl.SetRows(testRows5k())
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		_ = tbl.Header()
		_ = tbl.Body()
		tbl.MoveDown() // move the cursor so the render cache is exercised
	}
}

// BenchmarkTableRender5kRefresh is the shape the engine actually drives: a new
// snapshot replaces the rows, which drops the render cache, and the visible
// window is redrawn cold. This is the cost SetRows-invalidation adds over reusing
// stale lines, and it must stay proportional to the window (40 rows), not to the
// 5k rows behind it.
func BenchmarkTableRender5kRefresh(b *testing.B) {
	tbl := NewTable(style.Default())
	tbl.SetColumns(testCols())
	tbl.SetSize(120, 40)
	rows := testRows5k()
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		tbl.SetRows(rows)
		_ = tbl.Header()
		_ = tbl.Body()
	}
}

func testRows5k() []columns.Row {
	out := make([]columns.Row, 5000)
	for i := range out {
		name := "pod-" + string(rune('a'+i%26)) + "-" + itoaTest(i)
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

func itoaTest(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}

func TestTableRepaintsWhenCellTextChanges(t *testing.T) {
	tbl := newTestTable()
	rows := testRows(1)
	tbl.SetRows(rows)
	if body := tbl.Body(); !strings.Contains(body, "3h") {
		t.Fatalf("initial body missing the AGE cell: %q", body)
	}

	// Same object identity (UID and resourceVersion unchanged), different rendered
	// text. This is what an AGE tick and the metrics CPU/MEM overlay both do: the
	// row's content changes while its resourceVersion holds still. Keying the
	// render cache on identity alone freezes the old line on screen.
	rows2 := testRows(1)
	rows2[0].Cells[2].Text = "4h"
	tbl.SetRows(rows2)

	body := tbl.Body()
	if !strings.Contains(body, "4h") {
		t.Errorf("body did not repaint after cell text changed; got %q", body)
	}
	if strings.Contains(body, "3h") {
		t.Errorf("body still shows the stale cached text; got %q", body)
	}
}

func TestTableRendersRowsWithoutIdentity(t *testing.T) {
	// Server-side Table rows (the CRD browser) can arrive with no UID and no
	// resourceVersion. Under an identity-only cache key every such row shares one
	// key, so the first row's rendered line is reused for all of them.
	tbl := newTestTable()
	mk := func(name string) columns.Row {
		return columns.Row{
			Cells: []columns.Cell{
				{Text: name, Role: columns.RoleName},
				{Text: "Running", Role: columns.RoleStatus, Status: columns.StatusOK},
				{Text: "1h", Status: columns.StatusMuted},
			},
			SortKeys: []columns.SortKey{columns.StrKey(name), columns.StrKey("Running"), columns.NumKey(0)},
		}
	}
	// Three rows, so at least two are simultaneously unselected and unmarked and
	// therefore share every component of an identity-only key.
	tbl.SetRows([]columns.Row{mk("alpha"), mk("bravo"), mk("charlie")})

	body := tbl.Body()
	for _, want := range []string{"alpha", "bravo", "charlie"} {
		if !strings.Contains(body, want) {
			t.Errorf("row %q missing — identity-less rows collapsed to one cached line; got %q", want, body)
		}
	}
}

func TestSetThemeResetsResolvedWidths(t *testing.T) {
	// Density feeds CellPad, which feeds the width solver. Swapping the theme
	// without dropping the resolved widths leaves them stale for the page's life.
	tbl := newTestTable()
	tbl.SetRows(testRows(1))
	_ = tbl.Body()

	tbl.SetTheme(style.New(style.AccentPreset("green"), style.Compact))
	if tbl.widths != nil {
		t.Error("SetTheme left resolved widths in place; density changes column padding")
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
