package columns

import (
	"strings"
	"time"

	networkingv1 "k8s.io/api/networking/v1"
)

func init() { Register(ingressesProjector{}) }

type ingressesProjector struct{}

func (ingressesProjector) Kind() string { return "ingresses" }

func (ingressesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 22, Grow: 2, Align: AlignLeft},
		{Title: "CLASS", MinWidth: 12, Align: AlignLeft},
		{Title: "HOSTS", MinWidth: 20, Grow: 2, Align: AlignLeft},
		{Title: "ADDRESS", MinWidth: 16, Grow: 1, Align: AlignLeft},
		{Title: "PORTS", MinWidth: 8, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (ingressesProjector) Project(obj any, now time.Time) (Row, bool) {
	ing, ok := obj.(*networkingv1.Ingress)
	if !ok {
		return Row{}, false
	}

	class := dash("")
	if ing.Spec.IngressClassName != nil {
		class = dash(*ing.Spec.IngressClassName)
	}

	var hosts []string
	for _, r := range ing.Spec.Rules {
		if r.Host != "" {
			hosts = append(hosts, r.Host)
		}
	}
	hostsTxt := dash(strings.Join(hosts, ","))

	var addrs []string
	for _, lb := range ing.Status.LoadBalancer.Ingress {
		if lb.IP != "" {
			addrs = append(addrs, lb.IP)
		} else if lb.Hostname != "" {
			addrs = append(addrs, lb.Hostname)
		}
	}
	addrTxt := dash(strings.Join(addrs, ","))

	ports := "80"
	if len(ing.Spec.TLS) > 0 {
		ports = "80, 443"
	}

	ageTxt, ageKey := age(ing.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: ing.Name, Role: RoleName},
		{Text: class, Status: StatusMuted},
		{Text: hostsTxt, Status: StatusMuted},
		{Text: addrTxt, Status: StatusMuted},
		{Text: ports, Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(ing.Name),
		StrKey(class),
		StrKey(hostsTxt),
		StrKey(addrTxt),
		StrKey(ports),
		ageKey,
	}
	return Row{
		UID: string(ing.UID), Namespace: ing.Namespace, Name: ing.Name,
		Version: ing.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
