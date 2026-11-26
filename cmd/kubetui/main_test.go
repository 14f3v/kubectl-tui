package main

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"testing"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/app"
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

// noGetwd fails the test if launchConfig asks for the working directory, which
// only a relative -kubeconfig path needs.
func noGetwd(t *testing.T) func() (string, error) {
	return func() (string, error) {
		t.Error("launchConfig asked for the working directory with no relative path to resolve")
		return "", errors.New("no working directory")
	}
}

func TestLaunchModeFromFlags(t *testing.T) {
	dir := t.TempDir()
	abs := filepath.Join(dir, writeConfig(t, dir))
	missing := filepath.Join(dir, "missing")
	// A set $KUBECONFIG names files, often several clusters' worth: it decides
	// which rows the picker lists, and never skips it.
	t.Setenv("KUBECONFIG", abs)
	cases := []struct {
		name string
		args []string
		want app.Config
	}{
		{"no flags, $KUBECONFIG set", nil, app.Config{Launch: app.LaunchPicker}},
		// Without -context the file's current-context is pinned at launch, so a
		// reconnect cannot follow a `kubectl config use-context` run elsewhere.
		{"-kubeconfig", []string{"-kubeconfig", abs}, app.Config{
			Launch: app.LaunchDirect, Start: k8s.ClusterRef{Source: k8s.SourceFile, Path: abs, Context: "a"}}},
		{"-context", []string{"-context", "b"}, app.Config{
			Launch: app.LaunchDirect, Start: k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "b"}}},
		{"-kubeconfig and -context", []string{"-kubeconfig", abs, "-context", "b"}, app.Config{
			Launch: app.LaunchDirect, Start: k8s.ClusterRef{Source: k8s.SourceFile, Path: abs, Context: "b"}}},
		// With no current context to pin, the context stays empty and the connect
		// says what is wrong.
		{"-kubeconfig, missing file", []string{"-kubeconfig", missing}, app.Config{
			Launch: app.LaunchDirect, Start: k8s.ClusterRef{Source: k8s.SourceFile, Path: missing}}},
		{"-kind only", []string{"-kind", "deploy"}, app.Config{
			Launch: app.LaunchPicker, StartKind: "deploy", StartKindExplicit: true}},
		// -kind pods is a choice too, not the default restated.
		{"-kind pods", []string{"-kind", "pods"}, app.Config{
			Launch: app.LaunchPicker, StartKind: "pods", StartKindExplicit: true}},
		{"empty values name nothing", []string{"-kubeconfig", "", "-context", "", "-kind", ""}, app.Config{Launch: app.LaunchPicker}},
	}
	for _, c := range cases {
		got, err := launchConfig(c.args, noGetwd(t))
		if err != nil {
			t.Errorf("%s: launchConfig(%q) failed: %v", c.name, c.args, err)
			continue
		}
		if got.Launch != c.want.Launch || got.Start != c.want.Start ||
			got.StartKind != c.want.StartKind || got.StartKindExplicit != c.want.StartKindExplicit {
			t.Errorf("%s: launchConfig(%q) = launch %v, start %+v, kind %q (explicit %v); want launch %v, start %+v, kind %q (explicit %v)",
				c.name, c.args, got.Launch, got.Start, got.StartKind, got.StartKindExplicit,
				c.want.Launch, c.want.Start, c.want.StartKind, c.want.StartKindExplicit)
		}
	}
}

func TestLaunchConfigVersion(t *testing.T) {
	if _, err := launchConfig([]string{"-version"}, noGetwd(t)); !errors.Is(err, errVersion) {
		t.Errorf("launchConfig(-version) = %v, want errVersion", err)
	}
}

// A relative -kubeconfig is remembered, so it must be made absolute: launched
// from another folder, "./dev.yaml" would name another file or none.
func TestRememberFileStoresAbsolutePath(t *testing.T) {
	dir := t.TempDir()
	if err := os.WriteFile(filepath.Join(dir, "dev.yaml"), []byte(twoContexts), 0o600); err != nil {
		t.Fatal(err)
	}
	got, err := launchConfig([]string{"-kubeconfig", "./dev.yaml"}, func() (string, error) { return dir, nil })
	if err != nil {
		t.Fatal(err)
	}
	want := k8s.ClusterRef{Source: k8s.SourceFile, Path: filepath.Join(dir, "dev.yaml"), Context: "a"}
	if !filepath.IsAbs(got.Start.Path) || got.Start != want {
		t.Errorf("Start = %+v, want %+v", got.Start, want)
	}
}
