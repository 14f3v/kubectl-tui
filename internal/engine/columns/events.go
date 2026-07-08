package columns

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(eventsProjector{}) }

type eventsProjector struct{}

func (eventsProjector) Kind() string { return "events" }

func (eventsProjector) Columns() []Column {
	return []Column{
		{Title: "LAST SEEN", MinWidth: 10, Align: AlignLeft},
		{Title: "TYPE", MinWidth: 9, Align: AlignLeft},
		{Title: "REASON", MinWidth: 18, Align: AlignLeft},
		{Title: "OBJECT", MinWidth: 24, Grow: 1, Align: AlignLeft},
		{Title: "COUNT", MinWidth: 6, Align: AlignRight},
		{Title: "MESSAGE", MinWidth: 30, Grow: 3, Align: AlignLeft},
	}
}

func (eventsProjector) Project(obj any, now time.Time) (Row, bool) {
	e, ok := obj.(*corev1.Event)
	if !ok {
		return Row{}, false
	}

	last := eventTime(e)
	lastTxt, lastKey := age(last, now)

	health := StatusNeutral
	typeClass := StatusMuted
	if e.Type == corev1.EventTypeWarning {
		health, typeClass = StatusWarn, StatusWarn
	}

	object := e.InvolvedObject.Kind + "/" + e.InvolvedObject.Name

	cells := []Cell{
		{Text: lastTxt, Status: StatusMuted},
		{Text: e.Type, Status: typeClass, Role: RoleStatus},
		{Text: e.Reason, Status: StatusNeutral},
		{Text: object, Status: StatusMuted},
		{Text: strconv.Itoa(int(e.Count)), Status: StatusMuted},
		{Text: singleLine(e.Message), Status: StatusNeutral},
	}
	sortKeys := []SortKey{
		lastKey,
		StrKey(e.Type),
		StrKey(e.Reason),
		StrKey(object),
		NumKey(float64(e.Count)),
		StrKey(e.Message),
	}
	return Row{
		UID: string(e.UID), Namespace: e.Namespace, Name: e.Name,
		Version: e.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}

// eventTime returns the most recent timestamp available on an event, preferring
// the series/last timestamps over the creation time.
func eventTime(e *corev1.Event) time.Time {
	if !e.LastTimestamp.IsZero() {
		return e.LastTimestamp.Time
	}
	if e.Series != nil && !e.Series.LastObservedTime.IsZero() {
		return e.Series.LastObservedTime.Time
	}
	if !e.EventTime.IsZero() {
		return e.EventTime.Time
	}
	return e.CreationTimestamp.Time
}

func singleLine(s string) string {
	out := make([]rune, 0, len(s))
	for _, r := range s {
		if r == '\n' || r == '\r' || r == '\t' {
			r = ' '
		}
		out = append(out, r)
	}
	return string(out)
}
