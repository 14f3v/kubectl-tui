package columns

import (
	"strconv"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

func init() { Register(mutatingAdmissionPoliciesProjector{}) }

// mutatingAdmissionPoliciesProjector projects MutatingAdmissionPolicy objects
// into table rows. It is cluster-scoped.
type mutatingAdmissionPoliciesProjector struct{}

// Kind returns the resource kind key for MutatingAdmissionPolicy objects.
func (mutatingAdmissionPoliciesProjector) Kind() string {
	return "mutatingadmissionpolicies"
}

// Columns describes the column layout for MutatingAdmissionPolicy rows.
func (mutatingAdmissionPoliciesProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "MUTATIONS", MinWidth: 12, Align: AlignRight},
		{Title: "FAILUREPOLICY", MinWidth: 14, Align: AlignLeft},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *MutatingAdmissionPolicy into a Row, returning ok=false for
// a wrong-typed object.
func (mutatingAdmissionPoliciesProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*admissionregistrationv1.MutatingAdmissionPolicy)
	if !ok {
		return Row{}, false
	}

	mutations := len(o.Spec.Mutations)

	failurePolicy := ""
	if o.Spec.FailurePolicy != nil {
		failurePolicy = string(*o.Spec.FailurePolicy)
	}

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: strconv.Itoa(mutations), Status: StatusMuted},
		{Text: dash(failurePolicy), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		NumKey(float64(mutations)),
		StrKey(failurePolicy),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
