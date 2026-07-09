package columns

import (
	"fmt"
	"time"

	appsv1 "k8s.io/api/apps/v1"
)

func init() { Register(statefulSetsProjector{}) }

type statefulSetsProjector struct{}

func (statefulSetsProjector) Kind() string { return "statefulsets" }

func (statefulSetsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "READY", MinWidth: 8, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (statefulSetsProjector) Project(obj any, now time.Time) (Row, bool) {
	s, ok := obj.(*appsv1.StatefulSet)
	if !ok {
		return Row{}, false
	}
	desired := int32(1)
	if s.Spec.Replicas != nil {
		desired = *s.Spec.Replicas
	}
	ready := s.Status.ReadyReplicas

	health := StatusOK
	switch {
	case desired == 0:
		health = StatusMuted
	case ready == 0:
		health = StatusError
	case ready < desired:
		health = StatusWarn
	}

	ageTxt, ageKey := age(s.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: s.Name, Role: RoleName},
		{Text: fmt.Sprintf("%d/%d", ready, desired), Status: health},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(s.Name),
		NumKey(float64(ready)),
		ageKey,
	}
	return Row{
		UID: string(s.UID), Namespace: s.Namespace, Name: s.Name,
		Version: s.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
