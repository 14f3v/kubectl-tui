package columns

import (
	"time"

	networkingv1 "k8s.io/api/networking/v1"
)

func init() { Register(ingressClassesProjector{}) }

type ingressClassesProjector struct{}

func (ingressClassesProjector) Kind() string { return "ingressclasses" }

func (ingressClassesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "CONTROLLER", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "PARAMETERS", MinWidth: 20, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

func (ingressClassesProjector) Project(obj any, now time.Time) (Row, bool) {
	ic, ok := obj.(*networkingv1.IngressClass)
	if !ok {
		return Row{}, false
	}

	name := ic.Name
	if ic.Annotations["ingressclass.kubernetes.io/is-default-class"] == "true" {
		name += " (default)"
	}

	parameters := ""
	if ic.Spec.Parameters != nil {
		parameters = ic.Spec.Parameters.Kind + "/" + ic.Spec.Parameters.Name
	}

	ageTxt, ageKey := age(ic.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: name, Role: RoleName},
		{Text: ic.Spec.Controller, Status: StatusMuted},
		{Text: dash(parameters), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(name),
		StrKey(ic.Spec.Controller),
		StrKey(parameters),
		ageKey,
	}
	return Row{
		UID: string(ic.UID), Namespace: "", Name: ic.Name,
		Version: ic.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
