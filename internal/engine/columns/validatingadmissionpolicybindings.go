package columns

import (
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

func init() { Register(validatingAdmissionPolicyBindingsProjector{}) }

// validatingAdmissionPolicyBindingsProjector projects
// ValidatingAdmissionPolicyBinding objects into table rows. It is cluster-scoped.
type validatingAdmissionPolicyBindingsProjector struct{}

// Kind returns the resource kind key for ValidatingAdmissionPolicyBinding objects.
func (validatingAdmissionPolicyBindingsProjector) Kind() string {
	return "validatingadmissionpolicybindings"
}

// Columns describes the column layout for ValidatingAdmissionPolicyBinding rows.
func (validatingAdmissionPolicyBindingsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "POLICY", MinWidth: 24, Grow: 2, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *ValidatingAdmissionPolicyBinding into a Row, returning
// ok=false for a wrong-typed object.
func (validatingAdmissionPolicyBindingsProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*admissionregistrationv1.ValidatingAdmissionPolicyBinding)
	if !ok {
		return Row{}, false
	}

	policy := o.Spec.PolicyName

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: dash(policy), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		StrKey(policy),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
