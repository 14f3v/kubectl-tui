package columns

import (
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
)

func init() { Register(clusterrolebindingsProjector{}) }

type clusterrolebindingsProjector struct{}

func (clusterrolebindingsProjector) Kind() string { return "clusterrolebindings" }

func (clusterrolebindingsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 28, Grow: 3, Align: AlignLeft},
		{Title: "ROLE", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (clusterrolebindingsProjector) Project(obj any, now time.Time) (Row, bool) {
	crb, ok := obj.(*rbacv1.ClusterRoleBinding)
	if !ok {
		return Row{}, false
	}
	role := crb.RoleRef.Kind + "/" + crb.RoleRef.Name
	health := StatusNeutral
	ageTxt, ageKey := age(crb.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: crb.Name, Role: RoleName},
		{Text: role, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(crb.Name),
		StrKey(role),
		ageKey,
	}
	return Row{
		UID: string(crb.UID), Namespace: crb.Namespace, Name: crb.Name,
		Version: crb.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
