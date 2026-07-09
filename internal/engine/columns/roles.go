package columns

import (
	"strconv"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
)

func init() { Register(rolesProjector{}) }

type rolesProjector struct{}

func (rolesProjector) Kind() string { return "roles" }

func (rolesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 28, Grow: 3, Align: AlignLeft},
		{Title: "RULES", MinWidth: 6, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (rolesProjector) Project(obj any, now time.Time) (Row, bool) {
	r, ok := obj.(*rbacv1.Role)
	if !ok {
		return Row{}, false
	}
	rules := len(r.Rules)
	health := StatusNeutral
	ageTxt, ageKey := age(r.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: r.Name, Role: RoleName},
		{Text: strconv.Itoa(rules), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(r.Name),
		NumKey(float64(rules)),
		ageKey,
	}
	return Row{
		UID: string(r.UID), Namespace: r.Namespace, Name: r.Name,
		Version: r.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
