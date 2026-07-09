package columns

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(resourceQuotasProjector{}) }

type resourceQuotasProjector struct{}

func (resourceQuotasProjector) Kind() string { return "resourcequotas" }

func (resourceQuotasProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "REQUEST-CPU", MinWidth: 12, Align: AlignLeft},
		{Title: "REQUEST-MEM", MinWidth: 12, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (resourceQuotasProjector) Project(obj any, now time.Time) (Row, bool) {
	q, ok := obj.(*corev1.ResourceQuota)
	if !ok {
		return Row{}, false
	}

	reqCPU := quotaHard(q, corev1.ResourceRequestsCPU)
	reqMem := quotaHard(q, corev1.ResourceRequestsMemory)

	ageTxt, ageKey := age(q.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: q.Name, Role: RoleName},
		{Text: reqCPU, Status: StatusMuted},
		{Text: reqMem, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(q.Name),
		StrKey(reqCPU),
		StrKey(reqMem),
		ageKey,
	}
	return Row{
		UID: string(q.UID), Namespace: q.Namespace, Name: q.Name,
		Version: q.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}

func quotaHard(q *corev1.ResourceQuota, name corev1.ResourceName) string {
	if v, present := q.Status.Hard[name]; present {
		return v.String()
	}
	if v, present := q.Spec.Hard[name]; present {
		return v.String()
	}
	return dash("")
}
