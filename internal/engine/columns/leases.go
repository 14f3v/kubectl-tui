package columns

import (
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
)

func init() { Register(leasesProjector{}) }

type leasesProjector struct{}

func (leasesProjector) Kind() string { return "leases" }

func (leasesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "HOLDER", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (leasesProjector) Project(obj any, now time.Time) (Row, bool) {
	l, ok := obj.(*coordinationv1.Lease)
	if !ok {
		return Row{}, false
	}

	holder := ""
	if l.Spec.HolderIdentity != nil {
		holder = *l.Spec.HolderIdentity
	}

	ageTxt, ageKey := age(l.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: l.Name, Role: RoleName},
		{Text: dash(holder), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(l.Name),
		StrKey(holder),
		ageKey,
	}
	return Row{
		UID: string(l.UID), Namespace: l.Namespace, Name: l.Name,
		Version: l.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
