package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/k8s"
)

func TestExitStatus(t *testing.T) {
	cases := []struct {
		name       string
		err        error
		code       int
		wantReport bool
	}{
		{"clean quit", nil, 0, false},
		// The exact shape Program.Run returns after os.Interrupt (Ctrl+Break on
		// Windows): ErrInterrupted wrapped under ErrProgramKilled with two %w verbs.
		{"interrupt as Run wraps it", fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrInterrupted), 130, false},
		{"bare interrupt", tea.ErrInterrupted, 130, false},
		{"panic", fmt.Errorf("%w: %w", tea.ErrProgramKilled, tea.ErrProgramPanic), 1, true},
		{"killed", tea.ErrProgramKilled, 1, true},
		{"other error", errors.New("open /dev/tty: no such device"), 1, true},
	}
	for _, c := range cases {
		code, report := exitStatus(c.err)
		if code != c.code || report != c.wantReport {
			t.Errorf("%s: exitStatus(%v) = %d, %v; want %d, %v", c.name, c.err, code, report, c.code, c.wantReport)
		}
	}
}

const twoContexts = `apiVersion: v1
kind: Config
current-context: a
clusters:
- name: ca
  cluster: {server: "https://a.example"}
- name: cb
  cluster: {server: "https://b.example"}
users:
- name: u
  user: {token: t}
contexts:
- name: a
  context: {cluster: ca, user: u}
- name: b
  context: {cluster: cb, user: u}
`

// writeConfig writes twoContexts into dir and returns the file's name in it.
func writeConfig(t *testing.T, dir string) string {
	t.Helper()
	if err := os.WriteFile(filepath.Join(dir, "config"), []byte(twoContexts), 0o600); err != nil {
		t.Fatal(err)
	}
	return "config"
}

func TestStartRef(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, writeConfig(t, dir))
	t.Setenv("KUBECONFIG", abs)
	cases := []struct {
		name, kubeconfig, context string
		want                      k8s.ClusterRef
	}{
		// Without --context the file's current-context is pinned at launch.
		{"default rules", "", "", k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "a"}},
		{"default rules, --context", "", "b", k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "b"}},
		{"--kubeconfig", abs, "", k8s.ClusterRef{Source: k8s.SourceFile, Path: abs, Context: "a"}},
		{"--kubeconfig, --context", abs, "b", k8s.ClusterRef{Source: k8s.SourceFile, Path: abs, Context: "b"}},
	}
	for _, c := range cases {
		if got := startRef(c.kubeconfig, c.context); got != c.want {
			t.Errorf("%s: startRef(%q, %q) = %+v, want %+v", c.name, c.kubeconfig, c.context, got, c.want)
		}
	}
}

func TestStartRefMakesKubeconfigPathAbsolute(t *testing.T) {
	dir := t.TempDir()
	name := writeConfig(t, dir)
	t.Chdir(dir)
	got := startRef(name, "")
	// The temp dir may sit behind a symlink (macOS /var → /private/var), so compare
	// against the same Abs the code uses rather than a joined literal.
	want, err := filepath.Abs(name)
	if err != nil {
		t.Fatal(err)
	}
	if !filepath.IsAbs(got.Path) || got.Path != want || got.Context != "a" {
		t.Errorf("startRef(%q) = %+v, want Path %q, Context a", name, got, want)
	}
}

// With no kubeconfig there is no context to pin; the bootstrap reports the
// problem as it always has.
func TestStartRefWithoutKubeconfigLeavesContextEmpty(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "missing"))
	want := k8s.ClusterRef{Source: k8s.SourceKubeconfig}
	if got := startRef("", ""); got != want {
		t.Errorf("startRef = %+v, want %+v", got, want)
	}
}
