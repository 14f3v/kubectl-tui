package k8s

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
)

const twoContexts = `apiVersion: v1
kind: Config
current-context: a
clusters:
- name: ca
  cluster: {server: "https://a.example"}
- name: cb
  cluster: {server: "https://b.example"}
users:
- name: ua
  user: {token: ta}
- name: ub
  user: {token: tb}
contexts:
- name: a
  context: {cluster: ca, user: ua, namespace: nsa}
- name: b
  context: {cluster: cb, user: ub, namespace: nsb}
`

func writeKubeconfig(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

func TestIdentityFollowsContextOverride(t *testing.T) {
	path := writeKubeconfig(t, twoContexts)
	s, err := NewSession(t.Context(), path, "b", false, func(tea.Msg) {})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Dispose()
	want := Identity{Context: "b", Cluster: "cb", User: "ub", Server: "https://b.example", Namespace: "nsb"}
	if s.Identity != want {
		t.Errorf("identity = %+v, want %+v", s.Identity, want)
	}
	if _, cur := s.Contexts(); cur != "b" {
		t.Errorf("current = %q, want b", cur)
	}
}

func TestDisposeIsNilSafe(t *testing.T) {
	(&Session{}).Dispose() // must not panic
}
