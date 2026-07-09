package columns

import (
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
)

func init() { Register(replicaSetsProjector{}) }

type replicaSetsProjector struct{}

func (replicaSetsProjector) Kind() string { return "replicasets" }

func (replicaSetsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "DESIRED", MinWidth: 8, Align: AlignRight},
		{Title: "CURRENT", MinWidth: 8, Align: AlignRight},
		{Title: "READY", MinWidth: 6, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (replicaSetsProjector) Project(obj any, now time.Time) (Row, bool) {
	r, ok := obj.(*appsv1.ReplicaSet)
	if !ok {
		return Row{}, false
	}
	desired := int32(0)
	if r.Spec.Replicas != nil {
		desired = *r.Spec.Replicas
	}
	ready := r.Status.ReadyReplicas

	health := StatusOK
	switch {
	case desired == 0:
		health = StatusMuted
	case ready == 0:
		health = StatusError
	case ready < desired:
		health = StatusWarn
	}

	ageTxt, ageKey := age(r.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: r.Name, Role: RoleName},
		{Text: strconv.Itoa(int(desired)), Status: StatusMuted},
		{Text: strconv.Itoa(int(r.Status.Replicas)), Status: StatusMuted},
		{Text: strconv.Itoa(int(ready)), Status: health},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(r.Name),
		NumKey(float64(desired)),
		NumKey(float64(r.Status.Replicas)),
		NumKey(float64(ready)),
		ageKey,
	}
	return Row{
		UID: string(r.UID), Namespace: r.Namespace, Name: r.Name,
		Version: r.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
