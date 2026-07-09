package columns

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
	rbacv1 "k8s.io/api/rbac/v1"
)

func TestServiceAccountsProject(t *testing.T) {
	proj := For("serviceaccounts")
	sa := &corev1.ServiceAccount{
		ObjectMeta: meta("builder"),
		Secrets:    []corev1.ObjectReference{{Name: "s1"}, {Name: "s2"}},
	}
	row, ok := proj.Project(sa, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "NAME").Text; got != "builder" {
		t.Fatalf("NAME = %q", got)
	}
	if got := find(t, proj, row, "SECRETS").Text; got != "2" {
		t.Fatalf("SECRETS = %q, want 2", got)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestRolesProject(t *testing.T) {
	proj := For("roles")
	r := &rbacv1.Role{
		ObjectMeta: meta("reader"),
		Rules: []rbacv1.PolicyRule{
			{Verbs: []string{"get"}},
			{Verbs: []string{"list"}},
			{Verbs: []string{"watch"}},
		},
	}
	row, ok := proj.Project(r, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "RULES").Text; got != "3" {
		t.Fatalf("RULES = %q, want 3", got)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestRoleBindingsProject(t *testing.T) {
	proj := For("rolebindings")
	rb := &rbacv1.RoleBinding{
		ObjectMeta: meta("bind"),
		RoleRef:    rbacv1.RoleRef{Kind: "Role", Name: "reader"},
	}
	row, ok := proj.Project(rb, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "ROLE").Text; got != "Role/reader" {
		t.Fatalf("ROLE = %q, want Role/reader", got)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestClusterRolesProject(t *testing.T) {
	proj := For("clusterroles")
	cr := &rbacv1.ClusterRole{
		ObjectMeta: meta("admin"),
		Rules: []rbacv1.PolicyRule{
			{Verbs: []string{"*"}},
		},
	}
	row, ok := proj.Project(cr, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "RULES").Text; got != "1" {
		t.Fatalf("RULES = %q, want 1", got)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}

func TestClusterRoleBindingsProject(t *testing.T) {
	proj := For("clusterrolebindings")
	crb := &rbacv1.ClusterRoleBinding{
		ObjectMeta: meta("cbind"),
		RoleRef:    rbacv1.RoleRef{Kind: "ClusterRole", Name: "admin"},
	}
	row, ok := proj.Project(crb, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "ROLE").Text; got != "ClusterRole/admin" {
		t.Fatalf("ROLE = %q, want ClusterRole/admin", got)
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want neutral", row.Health)
	}
}
