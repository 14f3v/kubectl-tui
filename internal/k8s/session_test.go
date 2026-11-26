package k8s

import (
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"
	clientcmdapi "k8s.io/client-go/tools/clientcmd/api"
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
	s, err := NewSession(t.Context(), ClusterRef{Source: SourceFile, Path: path, Context: "b"}, Options{Sink: func(tea.Msg) {}})
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

// The ref's context wins over the file's current-context ("a"), so the Session
// talks to the cluster the ref names and carries that ref for a later reconnect.
func TestNewSessionPinsRefContext(t *testing.T) {
	path := writeKubeconfig(t, twoContexts)
	ref := ClusterRef{Source: SourceFile, Path: path, Context: "b"}
	s, err := NewSession(t.Context(), ref, Options{Sink: func(tea.Msg) {}})
	if err != nil {
		t.Fatal(err)
	}
	defer s.Dispose()
	if s.Ref != ref {
		t.Errorf("Ref = %+v, want %+v", s.Ref, ref)
	}
	if s.RestCfg.Host != "https://b.example" {
		t.Errorf("RestCfg.Host = %q, want https://b.example", s.RestCfg.Host)
	}
}

func TestDisposeIsNilSafe(t *testing.T) {
	(&Session{}).Dispose() // must not panic
}

const execKubeconfig = `apiVersion: v1
kind: Config
clusters:
- name: dev
  cluster: {server: "https://dev.example:6443"}
users:
- name: u
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args: [eks, get-token]
      interactiveMode: Never
contexts:
- name: dev
  context: {cluster: dev, user: u}
current-context: dev
`

func execSession(t *testing.T, src Source, approved []string) *Session {
	t.Helper()
	path := writeKubeconfig(t, execKubeconfig)
	s, err := NewSession(t.Context(), ClusterRef{Source: src, Path: path, Context: "dev"},
		Options{Sink: func(tea.Msg) {}, ApprovedExec: approved})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(s.Dispose)
	return s
}

func TestPastedExecAllowlist(t *testing.T) {
	p := execSession(t, SourcePasted, []string{"aws"}).RestCfg.ExecProvider.PluginPolicy
	if p.PolicyType != clientcmdapi.PluginPolicyAllowlist || len(p.Allowlist) != 1 || p.Allowlist[0].Command != "aws" {
		t.Fatalf("policy = %+v", p)
	}
}

func TestPastedExecDeniedWhenUnapproved(t *testing.T) {
	p := execSession(t, SourcePasted, nil).RestCfg.ExecProvider.PluginPolicy
	if p.PolicyType != clientcmdapi.PluginPolicyDenyAll || len(p.Allowlist) != 0 {
		t.Fatalf("policy = %+v", p)
	}
}

func TestFileSourceExecUntouched(t *testing.T) {
	p := execSession(t, SourceFile, []string{"aws"}).RestCfg.ExecProvider.PluginPolicy
	if p.PolicyType != "" || len(p.Allowlist) != 0 {
		t.Fatalf("policy = %+v", p)
	}
}
