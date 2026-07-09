package columns

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(limitRangesProjector{}) }

type limitRangesProjector struct{}

func (limitRangesProjector) Kind() string { return "limitranges" }

func (limitRangesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "TYPES", MinWidth: 16, Grow: 1, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (limitRangesProjector) Project(obj any, now time.Time) (Row, bool) {
	l, ok := obj.(*corev1.LimitRange)
	if !ok {
		return Row{}, false
	}

	types := limitRangeTypes(l)

	ageTxt, ageKey := age(l.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: l.Name, Role: RoleName},
		{Text: types, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(l.Name),
		StrKey(types),
		ageKey,
	}
	return Row{
		UID: string(l.UID), Namespace: l.Namespace, Name: l.Name,
		Version: l.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}

func limitRangeTypes(l *corev1.LimitRange) string {
	var parts []string
	seen := map[string]bool{}
	for _, item := range l.Spec.Limits {
		t := string(item.Type)
		if seen[t] {
			continue
		}
		seen[t] = true
		parts = append(parts, t)
	}
	if len(parts) == 0 {
		return dash("")
	}
	return strings.Join(parts, ",")
}
