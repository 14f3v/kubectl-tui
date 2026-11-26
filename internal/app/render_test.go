package app

import (
	"fmt"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// Mouse capture takes drag-to-select away from the terminal, and nothing in the
// app handles mouse events, so the View must not ask for it. Bracketed paste
// stays on: that is what delivers a paste as one PasteMsg instead of keys, and a
// pasted newline as text instead of enter.
func TestViewLeavesMouseOffAndPasteOn(t *testing.T) {
	m := &Model{theme: style.Default()}
	v := m.View()
	if v.MouseMode != tea.MouseModeNone {
		t.Errorf("MouseMode = %v, want MouseModeNone until mouse events are handled", v.MouseMode)
	}
	if v.DisableBracketedPasteMode {
		t.Error("bracketed paste is disabled; a pasted newline would arrive as enter")
	}
	if !v.AltScreen {
		t.Error("the full-screen UI lost the alt screen")
	}
}

// k3sLike is a kubeconfig the way k3s and many installers write it: context,
// cluster and user are all "default". The test fills in the server.
const k3sLike = `apiVersion: v1
kind: Config
current-context: default
clusters:
- name: default
  cluster: {server: "%s"}
users:
- name: default
  user: {token: t}
contexts:
- name: default
  context: {cluster: default, user: default}
`

// Two pasted k3s clusters both read default / default / default: the header
// and the window title say which one by the name it was saved under.
func TestHeaderShowsPasteName(t *testing.T) {
	fast := fastServer(t)
	s := savedStore(t, "")
	ref := func(name string) k8s.ClusterRef {
		t.Helper()
		r, err := clusters.Inspect([]byte(fmt.Sprintf(k3sLike, fast)))
		if err != nil {
			t.Fatal(err)
		}
		file, err := s.SavePaste(name, []byte(strings.Replace(string(r.Data), "token: t", "token: "+name, 1)))
		if err != nil {
			t.Fatal(err)
		}
		ps := recordPaste(s, file, name, r, "default", true)
		if ps.err != nil {
			t.Fatal(ps.err)
		}
		return ps.ref
	}
	prod, lab := ref("prod"), ref("lab")
	m := New(Config{Sink: func(tea.Msg) {}, Start: prod, StartKind: "portforwards", StartKindExplicit: true, Store: s})
	m.width, m.height = 120, 30
	t.Cleanup(func() {
		m.cancelConnect()
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	deliver(t, m, m.Init())
	if h := ansi.Strip(m.renderHeader()); !strings.Contains(h, "Context  prod (default)") {
		t.Errorf("the header does not name the paste:\n%s", h)
	}
	if got := m.View().WindowTitle; got != "default@prod (default) — kubetui" {
		t.Errorf("title = %q, want it to name the paste", got)
	}

	_, connect := m.switchContext(lab)
	deliver(t, m, connect)
	if h := ansi.Strip(m.renderHeader()); !strings.Contains(h, "Context  lab (default)") {
		t.Errorf("after the switch the header does not name lab:\n%s", h)
	}

	// A kubeconfig of the user's own has no saved name; nor does a paste
	// named after its context need to repeat it.
	file := k8s.ClusterRef{Source: k8s.SourceFile, Path: writeKubeconfig(t, fmt.Sprintf(k3sLike, fast)), Context: "default"}
	_, connect = m.switchContext(file)
	deliver(t, m, connect)
	if h := ansi.Strip(m.renderHeader()); !strings.Contains(h, "Context  default ") || strings.Contains(h, "(default)") {
		t.Errorf("a file's header names a paste:\n%s", h)
	}
	if got := m.View().WindowTitle; got != "default@default — kubetui" {
		t.Errorf("title = %q, want default@default", got)
	}
	_, connect = m.switchContext(ref("default"))
	deliver(t, m, connect)
	if h := ansi.Strip(m.renderHeader()); strings.Contains(h, "default (default)") {
		t.Errorf("a paste named after its context repeats it:\n%s", h)
	}
}

// ? while a direct launch connects shows help; it used to turn help on
// unseen behind "connecting", so q took two presses with nothing to say why.
func TestHelpShowsWhileDirectLaunchConnects(t *testing.T) {
	ref := k8s.ClusterRef{Source: k8s.SourceFile, Path: writeKubeconfig(t, fmt.Sprintf(k3sLike, "https://k.example")), Context: "default"}
	m, _ := launchWith(t, Config{Sink: func(tea.Msg) {}, Launch: LaunchDirect, Start: ref})
	m.handleKey(textKey('?'))
	if v := screen(m); !strings.Contains(v, "kubetui — keys") {
		t.Errorf("help is on but not on the screen:\n%s", v)
	}
	m.handleKey(textKey('x'))
	if v := screen(m); !strings.Contains(v, "connecting to cluster…") {
		t.Errorf("closing help did not go back to the connect:\n%s", v)
	}
}
