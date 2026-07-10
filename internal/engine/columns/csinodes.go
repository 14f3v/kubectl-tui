package columns

import (
	"strconv"
	"time"

	storagev1 "k8s.io/api/storage/v1"
)

func init() { Register(csiNodesProjector{}) }

// csiNodesProjector projects CSINode objects into table rows. It is
// cluster-scoped.
type csiNodesProjector struct{}

// Kind returns the resource kind key for CSINode objects.
func (csiNodesProjector) Kind() string { return "csinodes" }

// Columns describes the column layout for CSINode rows.
func (csiNodesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "DRIVERS", MinWidth: 8, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *CSINode into a Row, returning ok=false for a wrong-typed
// object.
func (csiNodesProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*storagev1.CSINode)
	if !ok {
		return Row{}, false
	}

	drivers := len(o.Spec.Drivers)

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: strconv.Itoa(drivers), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		NumKey(float64(drivers)),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
