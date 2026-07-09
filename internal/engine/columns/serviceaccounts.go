package columns

import (
	"strconv"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(serviceaccountsProjector{}) }

type serviceaccountsProjector struct{}

func (serviceaccountsProjector) Kind() string { return "serviceaccounts" }

func (serviceaccountsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 28, Grow: 3, Align: AlignLeft},
		{Title: "SECRETS", MinWidth: 8, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (serviceaccountsProjector) Project(obj any, now time.Time) (Row, bool) {
	sa, ok := obj.(*corev1.ServiceAccount)
	if !ok {
		return Row{}, false
	}
	secrets := len(sa.Secrets)
	health := StatusNeutral
	ageTxt, ageKey := age(sa.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: sa.Name, Role: RoleName},
		{Text: strconv.Itoa(secrets), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(sa.Name),
		NumKey(float64(secrets)),
		ageKey,
	}
	return Row{
		UID: string(sa.UID), Namespace: sa.Namespace, Name: sa.Name,
		Version: sa.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
