package columns

import (
	"strconv"
	"time"

	schedulingv1 "k8s.io/api/scheduling/v1"
)

func init() { Register(priorityClassesProjector{}) }

type priorityClassesProjector struct{}

func (priorityClassesProjector) Kind() string { return "priorityclasses" }

func (priorityClassesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "VALUE", MinWidth: 12, Align: AlignRight},
		{Title: "GLOBAL-DEFAULT", MinWidth: 15, Align: AlignLeft},
		{Title: "PREEMPTION", MinWidth: 20, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (priorityClassesProjector) Project(obj any, now time.Time) (Row, bool) {
	pc, ok := obj.(*schedulingv1.PriorityClass)
	if !ok {
		return Row{}, false
	}

	globalDefault := strconv.FormatBool(pc.GlobalDefault)

	preemption := ""
	if pc.PreemptionPolicy != nil {
		preemption = string(*pc.PreemptionPolicy)
	}

	ageTxt, ageKey := age(pc.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: pc.Name, Role: RoleName},
		{Text: strconv.Itoa(int(pc.Value)), Status: StatusMuted},
		{Text: globalDefault, Status: StatusMuted},
		{Text: dash(preemption), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(pc.Name),
		NumKey(float64(pc.Value)),
		StrKey(globalDefault),
		StrKey(preemption),
		ageKey,
	}
	return Row{
		UID: string(pc.UID), Namespace: "", Name: pc.Name,
		Version: pc.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
