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

// headerAt renders the header of a model showing id at the given width, with
// an optional saved name, and fails if any line is wider than the terminal.
func headerAt(t *testing.T, width int, name string, id k8s.Identity) string {
	t.Helper()
	m := &Model{theme: style.Default(), width: width, height: 30, name: name}
	m.sess = &k8s.Session{Identity: id}
	h := ansi.Strip(m.renderHeader())
	for _, line := range strings.Split(h, "\n") {
		if w := ansi.StringWidth(line); w > width {
			t.Errorf("width %d: header line is %d cells: %q", width, w, line)
		}
	}
	return h
}

// valueWidth is the cells a header value gets in the first column.
func valueWidth(width int) int { return (width-4)*30/100 - 9 }

// An EKS context is an ARN and a GKE one ends in the cluster name: what tells
// two clusters apart is at the end, so a value too wide for its column keeps
// its end and loses its start.
func TestHeaderKeepsTheEndOfLongContexts(t *testing.T) {
	const prefix = "arn:aws:eks:us-east-1:123456789012:cluster/"
	for _, width := range []int{80, 120, 160} {
		var heads []string
		for _, name := range []string{"prod-payments", "staging-payments"} {
			h := headerAt(t, width, "", k8s.Identity{Context: prefix + name})
			heads = append(heads, h)
			full := prefix + name
			tail := full[len(full)-(valueWidth(width)-1):]
			if !strings.Contains(h, "…"+tail) {
				t.Errorf("width %d: header lost the end of %q (want …%s):\n%s", width, name, tail, h)
			}
		}
		if heads[0] == heads[1] {
			t.Errorf("width %d: two different clusters render the same header:\n%s", width, heads[0])
		}
	}
}

// The saved name of a paste takes the room first; the context is cut from its
// start to fit what is left.
func TestHeaderPasteNameBeatsLongContext(t *testing.T) {
	ctx := "arn:aws:eks:us-east-1:123456789012:cluster/prod-payments"
	h := headerAt(t, 80, "prod", k8s.Identity{Context: ctx})
	if !strings.Contains(h, "Context  prod ") {
		t.Errorf("the saved name is not whole:\n%s", h)
	}
	if strings.Contains(h, "prod (arn") {
		t.Errorf("the context was cut from its end:\n%s", h)
	}
	if !strings.Contains(h, "ments)") {
		t.Errorf("the context lost its end or its closing paren:\n%s", h)
	}

	// A name wider than the column is cut from its end, and the context goes.
	long := strings.Repeat("n", 40)
	h = headerAt(t, 80, long, k8s.Identity{Context: ctx})
	if !strings.Contains(h, "Context  nnnn") || !strings.Contains(h, "…") || strings.Contains(h, "(") {
		t.Errorf("a long name should be cut from its end, alone:\n%s", h)
	}
}

func TestHeaderKeepsTheEndOfClusterAndUser(t *testing.T) {
	long := "arn:aws:eks:us-east-1:123456789012:cluster/prod-payments"
	for _, width := range []int{80, 120, 160} {
		h := headerAt(t, width, "", k8s.Identity{Context: "c", Cluster: long, User: long})
		n := valueWidth(width) - 1
		want := "…" + long[len(long)-n:]
		if got := strings.Count(h, want); got != 2 {
			t.Errorf("width %d: cluster and user should both end in %q (found %d):\n%s", width, want, got, h)
		}
	}
}

// A cut that lands inside a wide rune must not leave the value a cell too wide.
func TestKeepEndWideRunes(t *testing.T) {
	for _, in := range []string{"日本語日本語日本", "a日本語bc日"} {
		runes := []rune(in)
		last := string(runes[len(runes)-1])
		for w := 1; w <= 10; w++ {
			got := keepEnd(in, w)
			if sw := ansi.StringWidth(got); sw > w {
				t.Errorf("keepEnd(%q, %d) = %q is %d cells", in, w, got, sw)
			}
			// The last rune must survive whenever "…" and it fit together.
			if w >= 1+ansi.StringWidth(last) && !strings.HasSuffix(got, last) {
				t.Errorf("keepEnd(%q, %d) = %q lost the last rune", in, w, got)
			}
		}
	}
}

func TestHeaderWideRunesDoNotWrap(t *testing.T) {
	ctx := "日本語日本語日本語日本語日本語日本語"
	for _, width := range []int{80, 81, 120, 160} {
		h := headerAt(t, width, "", k8s.Identity{Context: ctx, Cluster: ctx, User: ctx})
		if !strings.Contains(h, "K8s Rev") {
			t.Errorf("width %d: a wide value wrapped and pushed the last row out:\n%s", width, h)
		}
	}
}

func TestHeaderNamedPasteWithEmptyContext(t *testing.T) {
	h := headerAt(t, 120, "prod", k8s.Identity{})
	if strings.Contains(h, "()") || !strings.Contains(h, "Context  prod") {
		t.Errorf("empty context:\n%s", h)
	}
}
