package columns

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(secretsProjector{}) }

type secretsProjector struct{}

func (secretsProjector) Kind() string { return "secrets" }

func (secretsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "TYPE", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "DATA", MinWidth: 5, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (secretsProjector) Project(obj any, now time.Time) (Row, bool) {
	s, ok := obj.(*corev1.Secret)
	if !ok {
		return Row{}, false
	}
	// SECURITY: never read or render any Data/StringData value; only its count.
	dataCount := len(s.Data)
	typeTxt := dash(string(s.Type))

	ageTxt, ageKey := age(s.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: s.Name, Role: RoleName},
		{Text: typeTxt, Status: StatusMuted},
		{Text: strconv.Itoa(dataCount), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(s.Name),
		StrKey(string(s.Type)),
		NumKey(float64(dataCount)),
		ageKey,
	}
	return Row{
		UID: string(s.UID), Namespace: s.Namespace, Name: s.Name,
		Version: s.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
