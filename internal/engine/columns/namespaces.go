package columns

import (
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(namespacesProjector{}) }

type namespacesProjector struct{}

func (namespacesProjector) Kind() string { return "namespaces" }

func (namespacesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 28, Grow: 3, Align: AlignLeft},
		{Title: "STATUS", MinWidth: 16, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (namespacesProjector) Project(obj any, now time.Time) (Row, bool) {
	ns, ok := obj.(*corev1.Namespace)
	if !ok {
		return Row{}, false
	}
	status := string(ns.Status.Phase)
	health := StatusOK
	if ns.Status.Phase == corev1.NamespaceTerminating {
		health = StatusWarn
	}
	ageTxt, ageKey := age(ns.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: ns.Name, Role: RoleName},
		{Text: status, Status: health, Role: RoleStatus},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{StrKey(ns.Name), StrKey(status), ageKey}
	return Row{
		UID: string(ns.UID), Name: ns.Name, Version: ns.ResourceVersion,
		Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}
