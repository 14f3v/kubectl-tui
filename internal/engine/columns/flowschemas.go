package columns

import (
	"strconv"
	"time"

	flowcontrolv1 "k8s.io/api/flowcontrol/v1"
)

func init() { Register(flowSchemasProjector{}) }

// flowSchemasProjector projects FlowSchema objects into table rows. It is
// cluster-scoped.
type flowSchemasProjector struct{}

// Kind returns the resource kind key for FlowSchema objects.
func (flowSchemasProjector) Kind() string { return "flowschemas" }

// Columns describes the column layout for FlowSchema rows.
func (flowSchemasProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "PRIORITYLEVEL", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "MATCHINGPRECEDENCE", MinWidth: 18, Align: AlignRight},
		{Title: "DISTINGUISHERMETHOD", MinWidth: 20, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *FlowSchema into a Row, returning ok=false for a
// wrong-typed object.
func (flowSchemasProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*flowcontrolv1.FlowSchema)
	if !ok {
		return Row{}, false
	}

	priorityLevel := o.Spec.PriorityLevelConfiguration.Name
	precedence := o.Spec.MatchingPrecedence

	distinguisher := ""
	if o.Spec.DistinguisherMethod != nil {
		distinguisher = string(o.Spec.DistinguisherMethod.Type)
	}

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: dash(priorityLevel), Status: StatusMuted},
		{Text: strconv.Itoa(int(precedence)), Status: StatusMuted},
		{Text: dash(distinguisher), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		StrKey(priorityLevel),
		NumKey(float64(precedence)),
		StrKey(distinguisher),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
