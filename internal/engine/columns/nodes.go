package columns

import (
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(nodesProjector{}) }

type nodesProjector struct{}

func (nodesProjector) Kind() string { return "nodes" }

func (nodesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 22, Grow: 2, Align: AlignLeft},
		{Title: "STATUS", MinWidth: 18, Align: AlignLeft},
		{Title: "ROLES", MinWidth: 14, Grow: 1, Align: AlignLeft},
		{Title: "VERSION", MinWidth: 12, Align: AlignLeft},
		{Title: "INTERNAL-IP", MinWidth: 15, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (nodesProjector) Project(obj any, now time.Time) (Row, bool) {
	n, ok := obj.(*corev1.Node)
	if !ok {
		return Row{}, false
	}

	statusText, health := nodeStatus(n)
	ageTxt, ageKey := age(n.CreationTimestamp.Time, now)

	cells := []Cell{
		{Text: n.Name, Role: RoleName},
		{Text: statusText, Status: health, Role: RoleStatus},
		{Text: nodeRoles(n), Status: StatusMuted},
		{Text: n.Status.NodeInfo.KubeletVersion, Status: StatusMuted},
		{Text: nodeInternalIP(n), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(n.Name),
		StrKey(statusText),
		StrKey(nodeRoles(n)),
		StrKey(n.Status.NodeInfo.KubeletVersion),
		StrKey(nodeInternalIP(n)),
		ageKey,
	}
	return Row{
		UID: string(n.UID), Name: n.Name, Version: n.ResourceVersion,
		Health: health, Cells: cells, SortKeys: sortKeys,
	}, true
}

func nodeStatus(n *corev1.Node) (string, StatusClass) {
	ready := false
	for _, c := range n.Status.Conditions {
		if c.Type == corev1.NodeReady {
			ready = c.Status == corev1.ConditionTrue
			break
		}
	}
	status := "Ready"
	health := StatusOK
	if !ready {
		status, health = "NotReady", StatusError
	}
	if n.Spec.Unschedulable {
		status += ",SchedulingDisabled"
		if health == StatusOK {
			health = StatusWarn
		}
	}
	return status, health
}

func nodeRoles(n *corev1.Node) string {
	var roles []string
	for k := range n.Labels {
		const prefix = "node-role.kubernetes.io/"
		if strings.HasPrefix(k, prefix) {
			if r := strings.TrimPrefix(k, prefix); r != "" {
				roles = append(roles, r)
			}
		}
	}
	if len(roles) == 0 {
		return "<none>"
	}
	return strings.Join(roles, ",")
}

func nodeInternalIP(n *corev1.Node) string {
	for _, a := range n.Status.Addresses {
		if a.Type == corev1.NodeInternalIP {
			return a.Address
		}
	}
	return "—"
}
