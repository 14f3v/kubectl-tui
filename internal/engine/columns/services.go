package columns

import (
	"strconv"
	"strings"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func init() { Register(servicesProjector{}) }

type servicesProjector struct{}

func (servicesProjector) Kind() string { return "services" }

func (servicesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 22, Grow: 2, Align: AlignLeft},
		{Title: "TYPE", MinWidth: 12, Align: AlignLeft},
		{Title: "CLUSTER-IP", MinWidth: 15, Align: AlignLeft},
		{Title: "EXTERNAL-IP", MinWidth: 15, Grow: 1, Align: AlignLeft},
		{Title: "PORTS", MinWidth: 16, Grow: 1, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (servicesProjector) Project(obj any, now time.Time) (Row, bool) {
	s, ok := obj.(*corev1.Service)
	if !ok {
		return Row{}, false
	}
	ageTxt, ageKey := age(s.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: s.Name, Role: RoleName},
		{Text: string(s.Spec.Type), Status: StatusNeutral},
		{Text: dash(s.Spec.ClusterIP), Status: StatusMuted},
		{Text: externalIP(s), Status: StatusMuted},
		{Text: servicePorts(s), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(s.Name),
		StrKey(string(s.Spec.Type)),
		StrKey(s.Spec.ClusterIP),
		StrKey(externalIP(s)),
		StrKey(servicePorts(s)),
		ageKey,
	}
	return Row{
		UID: string(s.UID), Namespace: s.Namespace, Name: s.Name,
		Version: s.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}

func externalIP(s *corev1.Service) string {
	switch s.Spec.Type {
	case corev1.ServiceTypeExternalName:
		return dash(s.Spec.ExternalName)
	case corev1.ServiceTypeLoadBalancer:
		var ips []string
		for _, ing := range s.Status.LoadBalancer.Ingress {
			if ing.IP != "" {
				ips = append(ips, ing.IP)
			} else if ing.Hostname != "" {
				ips = append(ips, ing.Hostname)
			}
		}
		if len(ips) == 0 {
			return "<pending>"
		}
		return strings.Join(ips, ",")
	default:
		if len(s.Spec.ExternalIPs) > 0 {
			return strings.Join(s.Spec.ExternalIPs, ",")
		}
		return "<none>"
	}
}

func servicePorts(s *corev1.Service) string {
	if len(s.Spec.Ports) == 0 {
		return "—"
	}
	parts := make([]string, 0, len(s.Spec.Ports))
	for _, p := range s.Spec.Ports {
		seg := strconv.Itoa(int(p.Port))
		if p.NodePort != 0 {
			seg += ":" + strconv.Itoa(int(p.NodePort))
		}
		seg += "/" + string(p.Protocol)
		parts = append(parts, seg)
	}
	return strings.Join(parts, ",")
}
