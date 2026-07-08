// Package columns defines the kind-agnostic table row model and the per-kind
// projectors that turn a typed Kubernetes object into a table Row. It is a leaf
// package: it depends only on Kubernetes types, never on the engine, the UI, or
// styling. Colors are chosen at render time by mapping a cell's StatusClass onto
// the active Theme, so this package carries semantics, not pixels.
package columns

import "time"

// StatusClass is a semantic status bucket. The renderer maps it to a color from
// the active theme; the projector decides which bucket a cell belongs to.
type StatusClass int

const (
	// StatusNeutral uses the default text color.
	StatusNeutral StatusClass = iota
	// StatusOK is healthy/running (green).
	StatusOK
	// StatusWarn is pending/transitional (yellow).
	StatusWarn
	// StatusError is failed/crashing (red).
	StatusError
	// StatusInfo is completed/informational (blue).
	StatusInfo
	// StatusMuted is de-emphasized (grey), e.g. zero restarts or terminating.
	StatusMuted
)

// CellRole tells the renderer how to draw a cell beyond its color.
type CellRole int

const (
	// RolePlain is an ordinary text cell.
	RolePlain CellRole = iota
	// RoleName is the primary identifier column; it is emphasized on the
	// selected row.
	RoleName
	// RoleStatus is drawn as a colored status dot followed by the text.
	RoleStatus
)

// Align controls horizontal alignment within a column.
type Align int

const (
	// AlignLeft left-aligns cell text (the default).
	AlignLeft Align = iota
	// AlignRight right-aligns cell text (used for numeric columns).
	AlignRight
)

// Column is the static description of a table column. Widths are resolved by the
// renderer: a column is at least MinWidth wide, and any leftover terminal width
// is distributed across columns in proportion to Grow.
type Column struct {
	Title    string
	MinWidth int
	Grow     int // flex weight for surplus width; 0 = fixed at MinWidth
	Align    Align
}

// Cell is one rendered value plus the semantics needed to color and draw it.
type Cell struct {
	Text   string
	Status StatusClass
	Role   CellRole
}

// SortKey is a typed sort value so ordering is correct for numbers and ages even
// though the displayed text is humanized (e.g. "4d2h", "318Mi").
type SortKey struct {
	IsNum bool
	Num   float64
	Str   string
}

// Less reports whether a should sort before b. Numeric keys compare numerically;
// otherwise keys compare as strings. Mixed kinds put numbers first.
func (a SortKey) Less(b SortKey) bool {
	if a.IsNum && b.IsNum {
		return a.Num < b.Num
	}
	if a.IsNum != b.IsNum {
		return a.IsNum
	}
	return a.Str < b.Str
}

// Str builds a string sort key.
func StrKey(s string) SortKey { return SortKey{Str: s} }

// Num builds a numeric sort key.
func NumKey(n float64) SortKey { return SortKey{IsNum: true, Num: n} }

// Row is a fully projected table row: display cells plus typed sort keys and a
// stable identity for the render cache and selection. Cells and SortKeys are
// parallel to a projector's Columns.
type Row struct {
	UID       string // stable object identity (metadata.uid)
	Namespace string
	Name      string
	Version   string      // resourceVersion; invalidates the per-row render cache
	Health    StatusClass // overall row health, for the header count line
	Cells     []Cell
	SortKeys  []SortKey
}

// Projector turns typed objects of one kind into rows and describes its columns.
// Implementations must be pure and cheap: they run inside the engine's coalescer
// on every flush.
type Projector interface {
	// Kind is the canonical resource kind key, e.g. "pods".
	Kind() string
	// Columns returns the column layout; Project returns cells parallel to it.
	Columns() []Column
	// Project converts one object into a Row. It returns ok=false to omit the
	// object (e.g. a wrong-typed item). now is passed so age columns are stable
	// within a single flush.
	Project(obj any, now time.Time) (row Row, ok bool)
}
