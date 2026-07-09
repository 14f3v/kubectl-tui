package columns

import (
	"testing"
	"time"

	corev1 "k8s.io/api/core/v1"
)

func TestSecretsProject(t *testing.T) {
	proj := For("secrets")
	s := &corev1.Secret{
		ObjectMeta: meta("x"),
		Type:       corev1.SecretTypeOpaque,
		Data: map[string][]byte{
			"username": []byte("admin"),
			"password": []byte("s3cr3t"),
		},
	}
	row, ok := proj.Project(s, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "TYPE").Text; got != "Opaque" {
		t.Fatalf("TYPE = %q, want %q", got, "Opaque")
	}
	if got := find(t, proj, row, "DATA").Text; got != "2" {
		t.Fatalf("DATA = %q, want %q", got, "2")
	}
	if row.Health != StatusNeutral {
		t.Fatalf("health = %v, want %v", row.Health, StatusNeutral)
	}
}

func TestSecretsProject_EmptyType(t *testing.T) {
	proj := For("secrets")
	s := &corev1.Secret{ObjectMeta: meta("x")}
	row, ok := proj.Project(s, time.Now())
	if !ok {
		t.Fatal("projection failed")
	}
	if got := find(t, proj, row, "TYPE").Text; got != "—" {
		t.Fatalf("TYPE = %q, want dash", got)
	}
	if got := find(t, proj, row, "DATA").Text; got != "0" {
		t.Fatalf("DATA = %q, want %q", got, "0")
	}
}
