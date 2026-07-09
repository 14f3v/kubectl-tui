package columns

import (
	"strconv"
	"time"

	policyv1 "k8s.io/api/policy/v1"
)

func init() { Register(pdbProjector{}) }

type pdbProjector struct{}

func (pdbProjector) Kind() string { return "poddisruptionbudgets" }

func (pdbProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "MIN-AVAILABLE", MinWidth: 14, Align: AlignLeft},
		{Title: "MAX-UNAVAILABLE", MinWidth: 16, Align: AlignLeft},
		{Title: "ALLOWED-DISRUPTIONS", MinWidth: 20, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (pdbProjector) Project(obj any, now time.Time) (Row, bool) {
	p, ok := obj.(*policyv1.PodDisruptionBudget)
	if !ok {
		return Row{}, false
	}

	minAvailable := dash("")
	if p.Spec.MinAvailable != nil {
		minAvailable = p.Spec.MinAvailable.String()
	}
	maxUnavailable := dash("")
	if p.Spec.MaxUnavailable != nil {
		maxUnavailable = p.Spec.MaxUnavailable.String()
	}
	allowed := p.Status.DisruptionsAllowed

	health := StatusOK
	if allowed == 0 {
		health = StatusWarn
	}

	ageTxt, ageKey := age(p.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: p.Name, Role: RoleName},
		{Text: minAvailable, Status: StatusMuted},
		{Text: maxUnavailable, Status: StatusMuted},
		{Text: strconv.Itoa(int(allowed)), Status: health},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(p.Name),
		StrKey(minAvailable),
		StrKey(maxUnavailable),
		NumKey(float64(allowed)),
		ageKey,
	}
	return Row{
		UID: string(p.UID), Namespace: p.Namespace, Name: p.Name,
		Version: p.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
