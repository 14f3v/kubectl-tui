package columns

import (
	"testing"
	"time"

	coordinationv1 "k8s.io/api/coordination/v1"
	apiv1 "k8s.io/api/core/v1"
	networkingv1 "k8s.io/api/networking/v1"
	nodev1 "k8s.io/api/node/v1"
	schedulingv1 "k8s.io/api/scheduling/v1"
)

func TestPriorityClassesProject(t *testing.T) {
	proj := For("priorityclasses")

	// With a preemption policy set.
	policy := apiv1.PreemptNever
	pc := &schedulingv1.PriorityClass{
		ObjectMeta:       meta("high"),
		Value:            1000,
		GlobalDefault:    true,
		PreemptionPolicy: &policy,
	}
	// Cluster-scoped: metadata namespace must be ignored.
	pc.Namespace = ""
	row, ok := proj.Project(pc, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
	if got := find(t, proj, row, "NAME"); got.Text != "high" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "VALUE"); got.Text != "1000" {
		t.Fatalf("VALUE = %q, want 1000", got.Text)
	}
	if got := find(t, proj, row, "GLOBAL-DEFAULT"); got.Text != "true" {
		t.Fatalf("GLOBAL-DEFAULT = %q, want true", got.Text)
	}
	if got := find(t, proj, row, "PREEMPTION"); got.Text != "Never" {
		t.Fatalf("PREEMPTION = %q, want Never", got.Text)
	}

	// Nil preemption policy → dash.
	pc2 := &schedulingv1.PriorityClass{ObjectMeta: meta("low"), Value: -100}
	row2, _ := proj.Project(pc2, time.Now())
	if got := find(t, proj, row2, "PREEMPTION"); got.Text != "—" {
		t.Fatalf("PREEMPTION nil = %q, want dash", got.Text)
	}
	if got := find(t, proj, row2, "GLOBAL-DEFAULT"); got.Text != "false" {
		t.Fatalf("GLOBAL-DEFAULT = %q, want false", got.Text)
	}
	if got := find(t, proj, row2, "VALUE"); got.Text != "-100" {
		t.Fatalf("VALUE = %q, want -100", got.Text)
	}

	// Wrong type → not ok.
	if _, ok := proj.Project(&schedulingv1.PriorityClassList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestRuntimeClassesProject(t *testing.T) {
	proj := For("runtimeclasses")
	rc := &nodev1.RuntimeClass{ObjectMeta: meta("gvisor"), Handler: "runsc"}
	rc.Namespace = ""
	row, ok := proj.Project(rc, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
	if got := find(t, proj, row, "NAME"); got.Text != "gvisor" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "HANDLER"); got.Text != "runsc" {
		t.Fatalf("HANDLER = %q, want runsc", got.Text)
	}

	if _, ok := proj.Project(&nodev1.RuntimeClassList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestIngressClassesProject(t *testing.T) {
	proj := For("ingressclasses")

	// Default class annotation + parameters set.
	ic := &networkingv1.IngressClass{
		ObjectMeta: meta("nginx"),
		Spec: networkingv1.IngressClassSpec{
			Controller: "k8s.io/ingress-nginx",
			Parameters: &networkingv1.IngressClassParametersReference{
				Kind: "IngressClassParams",
				Name: "nginx-params",
			},
		},
	}
	ic.Annotations = map[string]string{"ingressclass.kubernetes.io/is-default-class": "true"}
	ic.Namespace = ""
	row, ok := proj.Project(ic, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if row.Namespace != "" {
		t.Fatalf("Namespace = %q, want empty (cluster-scoped)", row.Namespace)
	}
	if row.Name != "nginx" {
		t.Fatalf("Row.Name = %q, want nginx (unsuffixed)", row.Name)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
	if got := find(t, proj, row, "NAME"); got.Text != "nginx (default)" {
		t.Fatalf("NAME = %q, want 'nginx (default)'", got.Text)
	}
	if got := find(t, proj, row, "CONTROLLER"); got.Text != "k8s.io/ingress-nginx" {
		t.Fatalf("CONTROLLER = %q", got.Text)
	}
	if got := find(t, proj, row, "PARAMETERS"); got.Text != "IngressClassParams/nginx-params" {
		t.Fatalf("PARAMETERS = %q", got.Text)
	}

	// No annotation + nil parameters → plain name and dash.
	ic2 := &networkingv1.IngressClass{
		ObjectMeta: meta("traefik"),
		Spec:       networkingv1.IngressClassSpec{Controller: "traefik.io/ingress-controller"},
	}
	row2, _ := proj.Project(ic2, time.Now())
	if got := find(t, proj, row2, "NAME"); got.Text != "traefik" {
		t.Fatalf("NAME = %q, want traefik", got.Text)
	}
	if got := find(t, proj, row2, "PARAMETERS"); got.Text != "—" {
		t.Fatalf("PARAMETERS nil = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(&networkingv1.IngressClassList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}

func TestLeasesProject(t *testing.T) {
	proj := For("leases")

	holder := "node-1"
	l := &coordinationv1.Lease{
		ObjectMeta: meta("kube-scheduler"),
		Spec:       coordinationv1.LeaseSpec{HolderIdentity: &holder},
	}
	row, ok := proj.Project(l, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	// Namespaced: metadata namespace preserved (meta() sets "default").
	if row.Namespace != "default" {
		t.Fatalf("Namespace = %q, want default (namespaced)", row.Namespace)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
	if got := find(t, proj, row, "NAME"); got.Text != "kube-scheduler" || got.Role != RoleName {
		t.Fatalf("NAME = %q role=%v", got.Text, got.Role)
	}
	if got := find(t, proj, row, "HOLDER"); got.Text != "node-1" {
		t.Fatalf("HOLDER = %q, want node-1", got.Text)
	}

	// Nil holder → dash.
	l2 := &coordinationv1.Lease{ObjectMeta: meta("empty-lease")}
	row2, _ := proj.Project(l2, time.Now())
	if got := find(t, proj, row2, "HOLDER"); got.Text != "—" {
		t.Fatalf("HOLDER nil = %q, want dash", got.Text)
	}

	if _, ok := proj.Project(&coordinationv1.LeaseList{}, time.Now()); ok {
		t.Fatal("expected wrong-type projection to fail")
	}
}
