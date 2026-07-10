package columns

import (
	"strconv"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

func init() { Register(validatingWebhookConfigurationsProjector{}) }

// validatingWebhookConfigurationsProjector projects ValidatingWebhookConfiguration
// objects into table rows. It is cluster-scoped.
type validatingWebhookConfigurationsProjector struct{}

// Kind returns the resource kind key for ValidatingWebhookConfiguration objects.
func (validatingWebhookConfigurationsProjector) Kind() string {
	return "validatingwebhookconfigurations"
}

// Columns describes the column layout for ValidatingWebhookConfiguration rows.
func (validatingWebhookConfigurationsProjector) Columns() []Column {
	return []Column{
		{Title: "NAME", MinWidth: 24, Grow: 3, Align: AlignLeft},
		{Title: "WEBHOOKS", MinWidth: 10, Align: AlignRight},
		{Title: "AGE", MinWidth: 5, Align: AlignRight},
	}
}

// Project converts a *ValidatingWebhookConfiguration into a Row, returning
// ok=false for a wrong-typed object.
func (validatingWebhookConfigurationsProjector) Project(obj any, now time.Time) (Row, bool) {
	o, ok := obj.(*admissionregistrationv1.ValidatingWebhookConfiguration)
	if !ok {
		return Row{}, false
	}

	webhooks := len(o.Webhooks)

	ageTxt, ageKey := age(o.CreationTimestamp.Time, now)
	cells := []Cell{
		{Text: o.Name, Role: RoleName},
		{Text: strconv.Itoa(webhooks), Status: StatusMuted},
		{Text: ageTxt, Status: StatusMuted},
	}
	sortKeys := []SortKey{
		StrKey(o.Name),
		NumKey(float64(webhooks)),
		ageKey,
	}
	return Row{
		UID: string(o.UID), Namespace: "", Name: o.Name,
		Version: o.ResourceVersion, Health: StatusNeutral, Cells: cells, SortKeys: sortKeys,
	}, true
}
