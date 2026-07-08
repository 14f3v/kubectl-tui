package columns

import (
	"fmt"
	"strconv"
	"time"

	appsv1 "k8s.io/api/apps/v1"
)

func init() { Register(deploymentsProjector{}) }

type deploymentsProjector struct{}

func (deploymentsProjector) Kind() string { return "deployments" }

func (deploymentsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "READY", MinWidth: 8, Align: AlignLeft},
		{Title: "UP-TO-DATE", MinWidth: 11, Align: AlignRight},
		{Title: "AVAILABLE", MinWidth: 10, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (deploymentsProjector) Project(obj any, now time.Time) (Row, bool) {
	d, ok := obj.(*appsv1.Deployment)
	if !ok {
		return Row{}, false
	}
	desired := int32(1)
	if d.Spec.Replicas != nil {
		desired = *d.Spec.Replicas
	}
	ready := d.Status.ReadyReplicas

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
		{Text: fmt.Sprintf("%d/%d", ready, desired), Status: health},
		{Text: strconv.Itoa(int(d.Status.UpdatedReplicas)), Status: StatusMuted},
		{Text: strconv.Itoa(int(d.Status.AvailableReplicas)), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(d.Name),
		NumKey(float64(ready)),
		NumKey(float64(d.Status.UpdatedReplicas)),
		NumKey(float64(d.Status.AvailableReplicas)),
		ageKey,
	}
	return Row{
		UID: string(d.UID), Namespace: d.Namespace, Name: d.Name,
		Version: d.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
