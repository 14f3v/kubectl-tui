package columns

import (
	"time"

	nodev1 "k8s.io/api/node/v1"
)

func init() { Register(runtimeClassesProjector{}) }

type runtimeClassesProjector struct{}

func (runtimeClassesProjector) Kind() string { return "runtimeclasses" }

func (runtimeClassesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "HANDLER", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (runtimeClassesProjector) Project(obj any, now time.Time) (Row, bool) {
	rc, ok := obj.(*nodev1.RuntimeClass)
	if !ok {
		return Row{}, false
	}

	ageTxt, ageKey := age(rc.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: rc.Name, Role: RoleName},
		{Text: rc.Handler, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(rc.Name),
		StrKey(rc.Handler),
		ageKey,
	}
	return Row{
		UID: string(rc.UID), Namespace: "", Name: rc.Name,
		Version: rc.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
