package columns

import (
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
)

func init() { Register(rolebindingsProjector{}) }

type rolebindingsProjector struct{}

func (rolebindingsProjector) Kind() string { return "rolebindings" }

func (rolebindingsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 28, Grow: 3, Align: AlignLeft},
		{Title: "ROLE", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (rolebindingsProjector) Project(obj any, now time.Time) (Row, bool) {
	rb, ok := obj.(*rbacv1.RoleBinding)
	if !ok {
		return Row{}, false
	}
	role := rb.RoleRef.Kind + "/" + rb.RoleRef.Name
	health := StatusNeutral
	ageTxt, ageKey := age(rb.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: rb.Name, Role: RoleName},
		{Text: role, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(rb.Name),
		StrKey(role),
		ageKey,
	}
	return Row{
		UID: string(rb.UID), Namespace: rb.Namespace, Name: rb.Name,
		Version: rb.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
