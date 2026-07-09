package columns

import (
	"strconv"
	"strings"
	"time"

	discoveryv1 "k8s.io/api/discovery/v1"
)

func init() { Register(endpointSlicesProjector{}) }

type endpointSlicesProjector struct{}

func (endpointSlicesProjector) Kind() string { return "endpointslices" }

func (endpointSlicesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "ADDRESSTYPE", MinWidth: 12, Align: AlignLeft},
		{Title: "PORTS", MinWidth: 16, Grow: 1, Align: AlignLeft},
		{Title: "ENDPOINTS", MinWidth: 10, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (endpointSlicesProjector) Project(obj any, now time.Time) (Row, bool) {
	es, ok := obj.(*discoveryv1.EndpointSlice)
	if !ok {
		return Row{}, false
	}

	var ports []string
	for _, p := range es.Ports {
		if p.Port != nil {
			ports = append(ports, strconv.Itoa(int(*p.Port)))
		}
	}
	portsTxt := dash(strings.Join(ports, ","))

	endpoints := len(es.Endpoints)

	ageTxt, ageKey := age(es.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: es.Name, Role: RoleName},
		{Text: string(es.AddressType), Status: StatusMuted},
		{Text: portsTxt, Status: StatusMuted},
		{Text: strconv.Itoa(endpoints), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(es.Name),
		StrKey(string(es.AddressType)),
		StrKey(portsTxt),
		NumKey(float64(endpoints)),
		ageKey,
	}
	return Row{
		UID: string(es.UID), Namespace: es.Namespace, Name: es.Name,
		Version: es.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
