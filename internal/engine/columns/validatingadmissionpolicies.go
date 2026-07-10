package columns

import (
	"strconv"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

func init() { Register(validatingAdmissionPoliciesProjector{}) }

// validatingAdmissionPoliciesProjector projects ValidatingAdmissionPolicy
// objects into table rows. It is cluster-scoped.
type validatingAdmissionPoliciesProjector struct{}

// Kind returns the resource kind key for ValidatingAdmissionPolicy objects.
func (validatingAdmissionPoliciesProjector) Kind() string {
	return "validatingadmissionpolicies"
}

// Columns describes the column layout for ValidatingAdmissionPolicy rows.
func (validatingAdmissionPoliciesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "VALIDATIONS", MinWidth: 12, Align: AlignRight},
		{Title: "FAILUREPOLICY", MinWidth: 14, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *ValidatingAdmissionPolicy into a Row, returning ok=false
// for a wrong-typed object.
func (validatingAdmissionPoliciesProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*admissionregistrationv1.ValidatingAdmissionPolicy)
	if !ok {
		return Row{}, false
	}

	validations := len(o.Spec.Validations)

	failurePolicy := ""
	if o.Spec.FailurePolicy != nil {
		failurePolicy = string(*o.Spec.FailurePolicy)
	}

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: strconv.Itoa(validations), Status: StatusMuted},
		{Text: dash(failurePolicy), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		NumKey(float64(validations)),
		StrKey(failurePolicy),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
