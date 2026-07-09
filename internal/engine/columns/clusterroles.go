package columns

import (
	"strconv"
	"time"

	rbacv1 "k8s.io/api/rbac/v1"
)

func init() { Register(clusterrolesProjector{}) }

type clusterrolesProjector struct{}

func (clusterrolesProjector) Kind() string { return "clusterroles" }

func (clusterrolesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 28, Grow: 3, Align: AlignLeft},
		{Title: "RULES", MinWidth: 6, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (clusterrolesProjector) Project(obj any, now time.Time) (Row, bool) {
	cr, ok := obj.(*rbacv1.ClusterRole)
	if !ok {
		return Row{}, false
	}
	rules := len(cr.Rules)
	health := StatusNeutral
	ageTxt, ageKey := age(cr.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: cr.Name, Role: RoleName},
		{Text: strconv.Itoa(rules), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(cr.Name),
		NumKey(float64(rules)),
		ageKey,
	}
	return Row{
		UID: string(cr.UID), Namespace: cr.Namespace, Name: cr.Name,
		Version: cr.ResourceVersion, Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
