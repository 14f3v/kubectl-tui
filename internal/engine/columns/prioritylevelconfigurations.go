package columns

import (
	"strconv"
	"time"

	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
)

func init() { Register(priorityLevelConfigurationsProjector{}) }

// priorityLevelConfigurationsProjector projects PriorityLevelConfiguration
// objects into table rows. It is cluster-scoped.
type priorityLevelConfigurationsProjector struct{}

// Kind returns the resource kind key for PriorityLevelConfiguration objects.
func (priorityLevelConfigurationsProjector) Kind() string {
	return "prioritylevelconfigurations"
}

// Columns describes the column layout for PriorityLevelConfiguration rows.
func (priorityLevelConfigurationsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "TYPE", MinWidth: 10, Align: AlignLeft},
		{Title: "NOMINALCONCURRENCYSHARES", MinWidth: 24, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *PriorityLevelConfiguration into a Row, returning ok=false
// for a wrong-typed object. The nominal concurrency shares are shown as a dash
// unless the priority level is Limited and declares them.
func (priorityLevelConfigurationsProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*flowcontrolv1.PriorityLevelConfiguration)
	if !ok {
		return Row{}, false
	}

	plType := string(o.Spec.Type)

	shares := ""
	var sharesKey SortKey
	if o.Spec.Limited != nil && o.Spec.Limited.NominalConcurrencyShares != nil {
		n := *o.Spec.Limited.NominalConcurrencyShares
		shares = strconv.Itoa(int(n))
		sharesKey = NumKey(float64(n))
	}

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: dash(plType), Status: StatusMuted},
		{Text: dash(shares), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		StrKey(plType),
		sharesKey,
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
