// Package component holds the hand-rolled TUI widgets: the windowed table, the
// command line, gauges, the confirm dialog, the help grid, and toasts. Widgets
// are plain structs with imperative methods; they do not speak tea.Msg. Only
// pages do that.
package component

import (
	"sort"
	"strconv"
	"strings"

	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/style"
)

const (
	markerWidth = 2 // "▶ " or "  "
	minColWidth = 3
)

// Table is a windowed, per-cell-styled resource table. It owns cursor position,
// vertical scroll, and sort state; the page owns filtering and hands it rows.
// Only the visible window is rendered each frame, with a per-row render cache
// keyed by identity, version, width, and selection, so a 5k-row cluster costs
// O(visible) per frame.
type Table struct {
	theme    style.Theme
	cols     []columns.Column
	rows     []columns.Row
	sortCol  int
	sortDesc bool

	cursor int
	offset int
	width  int
	height int // number of body rows visible

	widths []int
	cache  map[string]string

	// marked is the multi-select set, keyed by row UID so a selection survives
	// re-sorts and row refreshes (like the cursor). Empty means "no bulk
	// selection"; actions then target the cursor row alone.
	marked map[string]bool
}

// NewTable returns an empty table with the given theme.
func NewTable(theme style.Theme) *Table {
	return &Table{theme: theme, cache: map[string]string{}, marked: map[string]bool{}}
}

// SetTheme swaps the theme and invalidates the render cache.
func (t *Table) SetTheme(theme style.Theme) {
	t.theme = theme
	t.cache = map[string]string{}
}

// SetColumns sets the column layout and invalidates cached widths.
func (t *Table) SetColumns(cols []columns.Column) {
	t.cols = cols
	t.widths = nil
	t.cache = map[string]string{}
	if t.sortCol >= len(cols) {
		t.sortCol = 0
	}
}

// SetRows replaces the row set, preserving the selected object by UID when it is
// still present, then applies the current sort.
func (t *Table) SetRows(rows []columns.Row) {
	var selUID string
	if r, ok := t.Selected(); ok {
		selUID = r.UID
	}
	t.rows = rows
	t.applySort()
	// Restore selection by identity.
	t.cursor = 0
	if selUID != "" {
		for i, r := range t.rows {
			if r.UID == selUID {
				t.cursor = i
				break
			}
		}
	}
	t.clampCursor()
	t.ensureVisible()
}

// SetSize sets the available width and the number of visible body rows.
func (t *Table) SetSize(width, bodyHeight int) {
	if width != t.width {
		t.widths = nil
		t.cache = map[string]string{}
	}
	t.width = width
	t.height = bodyHeight
	t.ensureVisible()
}

// SortColumn returns the current sort column index and direction.
func (t *Table) SortColumn() (int, bool) { return t.sortCol, t.sortDesc }

// ColumnCount returns the number of columns, for runtime sort cycling.
func (t *Table) ColumnCount() int { return len(t.cols) }

// SetSortState sets the sort column and direction directly (no toggle) and
// re-sorts. Used by pages that open with a non-default sort, e.g. events newest
// first.
func (t *Table) SetSortState(col int, desc bool) {
	if col < 0 || col >= len(t.cols) {
		return
	}
	t.sortCol = col
	t.sortDesc = desc
	t.applySort()
	t.cache = map[string]string{}
}

// SetSort selects a sort column; selecting the current column again toggles
// direction.
func (t *Table) SetSort(col int) {
	if col < 0 || col >= len(t.cols) {
		return
	}
	if col == t.sortCol {
		t.sortDesc = !t.sortDesc
	} else {
		t.sortCol = col
		t.sortDesc = false
	}
	t.applySort()
	t.cache = map[string]string{}
}

func (t *Table) applySort() {
	col := t.sortCol
	sort.SliceStable(t.rows, func(i, j int) bool {
		if col >= len(t.rows[i].SortKeys) || col >= len(t.rows[j].SortKeys) {
			return false
		}
		a, b := t.rows[i].SortKeys[col], t.rows[j].SortKeys[col]
		if t.sortDesc {
			return b.Less(a)
		}
		return a.Less(b)
	})
}

// RowCount returns the number of rows currently held.
func (t *Table) RowCount() int { return len(t.rows) }

// Cursor returns the selected row index.
func (t *Table) Cursor() int { return t.cursor }

// Selected returns the selected row, or ok=false when the table is empty.
func (t *Table) Selected() (columns.Row, bool) {
	if t.cursor < 0 || t.cursor >= len(t.rows) {
		return columns.Row{}, false
	}
	return t.rows[t.cursor], true
}

// ToggleMark flips the multi-select mark on the cursor row. No-op on an empty
// table. Marks are keyed by UID so they follow the object across re-sorts.
func (t *Table) ToggleMark() {
	r, ok := t.Selected()
	if !ok {
		return
	}
	if t.marked == nil {
		t.marked = map[string]bool{}
	}
	if t.marked[r.UID] {
		delete(t.marked, r.UID)
	} else {
		t.marked[r.UID] = true
	}
}

// ClearMarks drops the whole multi-select set.
func (t *Table) ClearMarks() { t.marked = map[string]bool{} }

// MarkedCount is how many rows are marked.
func (t *Table) MarkedCount() int { return len(t.marked) }

// MarkedRows returns the currently-marked rows in display order. Only rows still
// present in the table are returned, so a mark on a since-removed object is
// silently dropped from the result (the map entry is pruned).
func (t *Table) MarkedRows() []columns.Row {
	if len(t.marked) == 0 {
		return nil
	}
	out := make([]columns.Row, 0, len(t.marked))
	present := map[string]bool{}
	for _, r := range t.rows {
		if t.marked[r.UID] {
			out = append(out, r)
			present[r.UID] = true
		}
	}
	for uid := range t.marked {
		if !present[uid] {
			delete(t.marked, uid)
		}
	}
	return out
}

// MoveUp/MoveDown/PageUp/PageDown/Home/End move the cursor and keep it visible.
func (t *Table) MoveUp()   { t.cursor--; t.clampCursor(); t.ensureVisible() }
func (t *Table) MoveDown() { t.cursor++; t.clampCursor(); t.ensureVisible() }
func (t *Table) Home()     { t.cursor = 0; t.ensureVisible() }
func (t *Table) End()      { t.cursor = len(t.rows) - 1; t.clampCursor(); t.ensureVisible() }
func (t *Table) PageUp()   { t.cursor -= t.height; t.clampCursor(); t.ensureVisible() }
func (t *Table) PageDown() { t.cursor += t.height; t.clampCursor(); t.ensureVisible() }

func (t *Table) clampCursor() {
	if t.cursor < 0 {
		t.cursor = 0
	}
	if t.cursor >= len(t.rows) {
		t.cursor = len(t.rows) - 1
	}
	if t.cursor < 0 {
		t.cursor = 0
	}
}

func (t *Table) ensureVisible() {
	if t.height <= 0 {
		return
	}
	if t.cursor < t.offset {
		t.offset = t.cursor
	}
	if t.cursor >= t.offset+t.height {
		t.offset = t.cursor - t.height + 1
	}
	if t.offset < 0 {
		t.offset = 0
	}
}

// Header renders the uppercase column header row with the sort column accented.
func (t *Table) Header() string {
	t.resolveWidths()
	pad := t.theme.Density.CellPad()
	var b strings.Builder
	b.WriteString(strings.Repeat(" ", markerWidth))
	for i, c := range t.cols {
		title := c.Title
		if i == t.sortCol {
			title += sortArrow(t.sortDesc)
		}
		st := t.theme.ColHeader
		if i == t.sortCol {
			st = t.theme.SortHeader
		}
		b.WriteString(cell(title, t.widths[i], c.Align, st))
		if i < len(t.cols)-1 {
			b.WriteString(strings.Repeat(" ", pad))
		}
	}
	return b.String()
}

// Body renders the visible window of rows.
func (t *Table) Body() string {
	t.resolveWidths()
	if t.height <= 0 {
		return ""
	}
	var b strings.Builder
	end := t.offset + t.height
	if end > len(t.rows) {
		end = len(t.rows)
	}
	for i := t.offset; i < end; i++ {
		b.WriteString(t.renderRow(t.rows[i], i == t.cursor))
		if i < end-1 {
			b.WriteByte('\n')
		}
	}
	// Pad the remaining body height with blank lines so the layout is stable.
	rendered := end - t.offset
	for i := rendered; i < t.height; i++ {
		b.WriteByte('\n')
	}
	return b.String()
}

func (t *Table) renderRow(row columns.Row, selected bool) string {
	marked := t.marked[row.UID]
	key := row.UID + "|" + row.Version + "|" + strconv.Itoa(t.width) + "|" + boolStr(selected) + "|" + boolStr(marked) + "|" + strconv.Itoa(t.sortCol)
	if cached, ok := t.cache[key]; ok {
		return cached
	}

	pad := t.theme.Density.CellPad()
	selBg := lipgloss.Color("#1a1d2e")

	seg := func(text string, w int, align columns.Align, st lipgloss.Style) string {
		if selected {
			st = st.Background(selBg)
		}
		return cell(text, w, align, st)
	}

	var b strings.Builder
	// Marker column (2 cells): the cursor arrow ("▶") in the first, the
	// multi-select mark ("✓") in the second — so a marked row under the cursor
	// reads "▶✓", marked-but-not-cursor reads " ✓", and cursor-only reads "▶ ".
	arrow, arrowStyle := " ", t.theme.Base
	if selected {
		arrow, arrowStyle = "▶", t.theme.AccentText
	}
	mark, markStyle := " ", t.theme.Base
	if marked {
		mark = "✓"
		markStyle = lipgloss.NewStyle().Foreground(t.theme.Pal.Warn)
	}
	if selected {
		arrowStyle = arrowStyle.Background(selBg)
		markStyle = markStyle.Background(selBg)
	}
	b.WriteString(arrowStyle.Render(arrow) + markStyle.Render(mark))

	for i, c := range t.cols {
		var s string
		if i < len(row.Cells) {
			s = t.renderCell(row.Cells[i], t.widths[i], c.Align, selected, seg)
		} else {
			s = seg("", t.widths[i], c.Align, t.theme.Base)
		}
		b.WriteString(s)
		if i < len(t.cols)-1 {
			gap := strings.Repeat(" ", pad)
			if selected {
				gap = lipgloss.NewStyle().Background(selBg).Render(gap)
			}
			b.WriteString(gap)
		}
	}
	out := b.String()
	t.cache[key] = out
	return out
}

func (t *Table) renderCell(c columns.Cell, w int, align columns.Align, selected bool, seg func(string, int, columns.Align, lipgloss.Style) string) string {
	fg := t.theme.StatusColor(c.Status)
	switch c.Role {
	case columns.RoleName:
		st := t.theme.Base.Bold(selected)
		return seg(c.Text, w, align, st)
	case columns.RoleStatus:
		// Colored dot + colored text, packed into the column width.
		dot := "● "
		text := c.Text
		avail := w - 2
		if avail < 0 {
			avail = 0
		}
		if ansi.StringWidth(text) > avail {
			text = ansi.Truncate(text, avail, "…")
		}
		st := lipgloss.NewStyle().Foreground(fg)
		if selected {
			st = st.Background(lipgloss.Color("#1a1d2e"))
		}
		gap := w - 2 - ansi.StringWidth(text)
		if gap < 0 {
			gap = 0
		}
		return st.Render(dot + text + strings.Repeat(" ", gap))
	default:
		st := lipgloss.NewStyle().Foreground(fg)
		return seg(c.Text, w, align, st)
	}
}

// resolveWidths computes per-column content widths for the current terminal
// width, distributing surplus to flexible columns and shrinking them under
// pressure before truncating.
func (t *Table) resolveWidths() {
	if t.widths != nil && len(t.widths) == len(t.cols) {
		return
	}
	n := len(t.cols)
	w := make([]int, n)
	if n == 0 {
		t.widths = w
		return
	}
	pad := t.theme.Density.CellPad()
	avail := t.width - markerWidth - pad*(n-1)
	if avail < n*minColWidth {
		avail = n * minColWidth
	}

	totalMin, totalGrow := 0, 0
	for i, c := range t.cols {
		w[i] = c.MinWidth
		totalMin += c.MinWidth
		totalGrow += c.Grow
	}
	surplus := avail - totalMin
	switch {
	case surplus > 0 && totalGrow > 0:
		given := 0
		for i, c := range t.cols {
			if c.Grow > 0 {
				add := surplus * c.Grow / totalGrow
				w[i] += add
				given += add
			}
		}
		for i, c := range t.cols { // remainder to the first flexible column
			if c.Grow > 0 {
				w[i] += surplus - given
				break
			}
		}
	case surplus < 0:
		deficit := -surplus
		for i := range t.cols { // shrink flexible columns first
			if deficit <= 0 {
				break
			}
			if t.cols[i].Grow > 0 {
				take := min(w[i]-minColWidth, deficit)
				if take > 0 {
					w[i] -= take
					deficit -= take
				}
			}
		}
		for deficit > 0 { // then shave the widest columns
			mi := 0
			for i := range w {
				if w[i] > w[mi] {
					mi = i
				}
			}
			if w[mi] <= minColWidth {
				break
			}
			w[mi]--
			deficit--
		}
	}
	t.widths = w
}

// cell truncates and pads text to width w with the given alignment and style.
func cell(text string, w int, align columns.Align, st lipgloss.Style) string {
	if w < 0 {
		w = 0
	}
	if ansi.StringWidth(text) > w {
		text = ansi.Truncate(text, w, "…")
	}
	gap := w - ansi.StringWidth(text)
	if gap < 0 {
		gap = 0
	}
	if align == columns.AlignRight {
		text = strings.Repeat(" ", gap) + text
	} else {
		text = text + strings.Repeat(" ", gap)
	}
	return st.Render(text)
}

func sortArrow(desc bool) string {
	if desc {
		return " ↓"
	}
	return " ↑"
}

func boolStr(b bool) string {
	if b {
		return "1"
	}
	return "0"
}
