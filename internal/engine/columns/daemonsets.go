package columns

import (
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
)

func init() { Register(daemonSetsProjector{}) }

type daemonSetsProjector struct{}

func (daemonSetsProjector) Kind() string { return "daemonsets" }

func (daemonSetsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "DESIRED", MinWidth: 8, Align: AlignRight},
		{Title: "CURRENT", MinWidth: 8, Align: AlignRight},
		{Title: "READY", MinWidth: 6, Align: AlignRight},
		{Title: "UP-TO-DATE", MinWidth: 11, Align: AlignRight},
		{Title: "AVAILABLE", MinWidth: 10, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (daemonSetsProjector) Project(obj any, now time.Time) (Row, bool) {
	d, ok := obj.(*appsv1.DaemonSet)
	if !ok {
		return Row{}, false
	}
	desired := d.Status.DesiredNumberScheduled
	ready := d.Status.NumberReady

	health := StatusOK
	switch {
	case desired == 0:
		health = StatusMuted
	case ready == 0:
		health = StatusError
	case ready < desired:
		health = StatusWarn
	}

	ageTxt, ageKey := age(d.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: d.Name, Role: RoleName},
		{Text: strconv.Itoa(int(desired)), Status: StatusMuted},
		{Text: strconv.Itoa(int(d.Status.CurrentNumberScheduled)), Status: StatusMuted},
		{Text: strconv.Itoa(int(ready)), Status: health},
		{Text: strconv.Itoa(int(d.Status.UpdatedNumberScheduled)), Status: StatusMuted},
		{Text: strconv.Itoa(int(d.Status.NumberAvailable)), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(d.Name),
		NumKey(float64(desired)),
		NumKey(float64(d.Status.CurrentNumberScheduled)),
		NumKey(float64(ready)),
		NumKey(float64(d.Status.UpdatedNumberScheduled)),
		NumKey(float64(d.Status.NumberAvailable)),
		ageKey,
	}
	return Row{
		UID: string(d.UID), Namespace: d.Namespace, Name: d.Name,
		Version: d.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
