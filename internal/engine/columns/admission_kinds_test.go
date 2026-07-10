package columns

import (
	"testing"
	"time"

	admissionregistrationv1 "k8s.io/api/admissionregistration/v1"
)

func TestValidatingWebhookConfigurationsProject(t *testing.T) {
	proj := For("validatingwebhookconfigurations")
	o := &admissionregistrationv1.ValidatingWebhookConfiguration{
		ObjectMeta: meta("vwc"),
		Webhooks: []admissionregistrationv1.ValidatingWebhook{
			{Name: "a.example.com"},
			{Name: "b.example.com"},
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
	if len(row.Cells) != 3 {
		t.Fatalf("cells = %d, want 3", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "vwc" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "WEBHOOKS"); got.Text != "2" {
		t.Fatalf("WEBHOOKS = %q, want 2", got.Text)
	}

	if _, ok := proj.Project(nil, time.Now()); ok {
		t.Fatal("expected nil projection to fail")
	}
}

func TestMutatingWebhookConfigurationsProject(t *testing.T) {
	proj := For("mutatingwebhookconfigurations")
	o := &admissionregistrationv1.MutatingWebhookConfiguration{
		ObjectMeta: meta("mwc"),
		Webhooks: []admissionregistrationv1.MutatingWebhook{
			{Name: "a.example.com"},
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 3 {
		t.Fatalf("cells = %d, want 3", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "mwc" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "WEBHOOKS"); got.Text != "1" {
		t.Fatalf("WEBHOOKS = %q, want 1", got.Text)
	}

	if _, ok := proj.Project(&admissionregistrationv1.MutatingWebhookConfigurationList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestValidatingAdmissionPoliciesProject(t *testing.T) {
	proj := For("validatingadmissionpolicies")

	fail := admissionregistrationv1.Fail
	o := &admissionregistrationv1.ValidatingAdmissionPolicy{
		ObjectMeta: meta("vap"),
		Spec: admissionregistrationv1.ValidatingAdmissionPolicySpec{
			Validations:   []admissionregistrationv1.Validation{{Expression: "true"}, {Expression: "1==1"}},
			FailurePolicy: &fail,
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 4 {
		t.Fatalf("cells = %d, want 4", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "vap" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "VALIDATIONS"); got.Text != "2" {
		t.Fatalf("VALIDATIONS = %q, want 2", got.Text)
	}
	if got := find(t, proj, row, "FAILUREPOLICY"); got.Text != "Fail" {
		t.Fatalf("FAILUREPOLICY = %q, want Fail", got.Text)
	}

	// Nil failure policy → dash.
	o2 := &admissionregistrationv1.ValidatingAdmissionPolicy{ObjectMeta: meta("vap2")}
	row2, _ := proj.Project(o2, time.Now())
	if got := find(t, proj, row2, "FAILUREPOLICY"); got.Text != "—" {
		t.Fatalf("FAILUREPOLICY nil = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(&admissionregistrationv1.ValidatingAdmissionPolicyList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestValidatingAdmissionPolicyBindingsProject(t *testing.T) {
	proj := For("validatingadmissionpolicybindings")
	o := &admissionregistrationv1.ValidatingAdmissionPolicyBinding{
		ObjectMeta: meta("vapb"),
		Spec:       admissionregistrationv1.ValidatingAdmissionPolicyBindingSpec{PolicyName: "vap"},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 3 {
		t.Fatalf("cells = %d, want 3", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "vapb" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "POLICY"); got.Text != "vap" {
		t.Fatalf("POLICY = %q, want vap", got.Text)
	}

	if _, ok := proj.Project(&admissionregistrationv1.ValidatingAdmissionPolicyBindingList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestMutatingAdmissionPoliciesProject(t *testing.T) {
	proj := For("mutatingadmissionpolicies")

	ignore := admissionregistrationv1.Ignore
	o := &admissionregistrationv1.MutatingAdmissionPolicy{
		ObjectMeta: meta("map"),
		Spec: admissionregistrationv1.MutatingAdmissionPolicySpec{
			Mutations:     []admissionregistrationv1.Mutation{{PatchType: admissionregistrationv1.PatchTypeApplyConfiguration}},
			FailurePolicy: &ignore,
		},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 4 {
		t.Fatalf("cells = %d, want 4", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "map" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "MUTATIONS"); got.Text != "1" {
		t.Fatalf("MUTATIONS = %q, want 1", got.Text)
	}
	if got := find(t, proj, row, "FAILUREPOLICY"); got.Text != "Ignore" {
		t.Fatalf("FAILUREPOLICY = %q, want Ignore", got.Text)
	}

	// Nil failure policy → dash.
	o2 := &admissionregistrationv1.MutatingAdmissionPolicy{ObjectMeta: meta("map2")}
	row2, _ := proj.Project(o2, time.Now())
	if got := find(t, proj, row2, "FAILUREPOLICY"); got.Text != "—" {
		t.Fatalf("FAILUREPOLICY nil = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(&admissionregistrationv1.MutatingAdmissionPolicyList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestMutatingAdmissionPolicyBindingsProject(t *testing.T) {
	proj := For("mutatingadmissionpolicybindings")
	o := &admissionregistrationv1.MutatingAdmissionPolicyBinding{
		ObjectMeta: meta("mapb"),
		Spec:       admissionregistrationv1.MutatingAdmissionPolicyBindingSpec{PolicyName: "map"},
	}
	o.Namespace = ""
	row, ok := proj.Project(o, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if len(row.Cells) != 3 {
		t.Fatalf("cells = %d, want 3", len(row.Cells))
	}
	if got := find(t, proj, row, "NAME"); got.Text != "mapb" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "POLICY"); got.Text != "map" {
		t.Fatalf("POLICY = %q, want map", got.Text)
	}

	if _, ok := proj.Project(&admissionregistrationv1.MutatingAdmissionPolicyBindingList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}
