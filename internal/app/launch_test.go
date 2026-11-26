package app

import (
	"fmt"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// brokenAndGood is a kubeconfig whose context a works and whose context bad
// names a cluster that is not in the file; the test fills in a's server.
const brokenAndGood = `apiVersion: v1
kind: Config
clusters:
- name: ca
  cluster: {server: "%s"}
users:
- name: u
  user: {token: t}
contexts:
- name: a
  context: {cluster: ca, user: u}
- name: bad
  context: {cluster: gone, user: u}
`

// launchModel builds the root model as main does for a plain `kubetui`, with
// store s, and runs Init. The port-forwards page starts no informer, so a
// connect talks to nothing but the version probe.
func launchModel(t *testing.T, s *clusters.Store) (*Model, tea.Cmd) {
	t.Helper()
	return launchWith(t, Config{Sink: func(tea.Msg) {}, Launch: LaunchPicker, StartKind: "portforwards", Store: s})
}

// launchWith builds the root model from cfg at a usable size and runs Init.
func launchWith(t *testing.T, cfg Config) (*Model, tea.Cmd) {
	t.Helper()
	m := New(cfg)
	m.width, m.height = 120, 40
	t.Cleanup(func() {
		m.cancelConnect()
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	return m, m.Init()
}

// launchLoad runs cmd, and each command of a batch, each on its own goroutine,
// and returns the launch picker's rows that one of them loads. Nothing else is
// waited for: a toast's expiry tick sleeps for seconds.
func launchLoad(t *testing.T, cmd tea.Cmd) launchRows {
	t.Helper()
	found := make(chan launchRows, 4)
	var run func(tea.Cmd)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		go func() {
			switch got := c().(type) {
			case tea.BatchMsg:
				for _, c := range got {
					run(c)
				}
			case launchRows:
				found <- got
			}
		}()
	}
	run(cmd)
	select {
	case l := <-found:
		if l.loaded.Err != nil {
			t.Fatalf("listing clusters: %v", l.loaded.Err)
		}
		return l
	case <-time.After(5 * time.Second):
		t.Fatal("the launch did not load the cluster rows")
		return launchRows{}
	}
}

// screen renders the whole frame without styling.
func screen(m *Model) string { return ansi.Strip(m.render()) }

func TestLaunchDirectWithKubeconfig(t *testing.T) {
	fast := fastServer(t)
	path := writeKubeconfig(t, fmt.Sprintf(threeClusters, fast, fast, fast))
	ref := k8s.ClusterRef{Source: k8s.SourceFile, Path: path, Context: "b"}
	m, connect := launchWith(t, Config{Sink: func(tea.Msg) {}, Launch: LaunchDirect, Start: ref, StartKind: "portforwards"})

	if m.active() != nil {
		t.Fatalf("a direct launch opened %T; it connects without asking", m.active())
	}
	if m.connecting == nil || *m.connecting != ref {
		t.Fatalf("connecting = %v, want %+v", m.connecting, ref)
	}
	if v := screen(m); !strings.Contains(v, "connecting to cluster…") {
		t.Errorf("the screen does not say it is connecting:\n%s", v)
	}
	deliver(t, m, connect)
	if m.sess == nil || m.sess.Ref != ref {
		t.Fatalf("not connected to %+v after the launch connect (ref = %+v)", ref, m.ref)
	}
}

func TestLaunchPickerWithoutFlags(t *testing.T) {
	fast := fastServer(t)
	s := savedStore(t, writeKubeconfig(t, fmt.Sprintf(threeClusters, fast, fast, fast)))
	b := k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "b"}
	if err := s.Update(clusters.KeyFor(b, fast), func(e *clusters.Entry) { e.LastUsed = time.Now() }); err != nil {
		t.Fatal(err)
	}
	m, launch := launchModel(t, s)

	if m.picker() == nil {
		t.Fatalf("the active page is %T, want the cluster picker", m.active())
	}
	if m.sess != nil || m.connecting != nil {
		t.Fatalf("connecting to %v before a cluster was picked", m.connecting)
	}
	m.Update(launchLoad(t, launch))

	v := screen(m)
	for _, want := range []string{"kubetui · pick a cluster", "▶ b", "q quit"} {
		if !strings.Contains(v, want) {
			t.Errorf("the launch screen has no %q:\n%s", want, v)
		}
	}
	// No header or breadcrumb: they describe a Session.
	for _, gone := range []string{"Context", "<clusters"} {
		if strings.Contains(v, gone) {
			t.Errorf("the launch screen shows %q, which needs a Session:\n%s", gone, v)
		}
	}

	// The cursor starts on the cluster used last, so going back to it is one enter.
	_, pick := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	connect := deliver(t, m, pick)
	if m.connecting == nil || *m.connecting != b {
		t.Fatalf("connecting = %v, want %+v", m.connecting, b)
	}
	if v := screen(m); !strings.Contains(v, "connecting…") {
		t.Errorf("the row does not say it is connecting:\n%s", v)
	}
	deliver(t, m, connect)
	if m.sess == nil || m.sess.Ref != b {
		t.Fatalf("not connected to %+v after enter (ref = %+v)", b, m.ref)
	}
	if m.picker() != nil {
		t.Error("the launch picker stayed up after the connect")
	}
}

func TestLaunchEmptyOpensPasteBox(t *testing.T) {
	m, launch := launchModel(t, savedStore(t, "")) // no kubeconfig, nothing saved
	rows := launchLoad(t, launch)
	if len(rows.loaded.Rows) != 0 {
		t.Fatalf("rows = %v, want none", labelsOf(rows.loaded.Rows))
	}
	m.Update(rows)

	if m.paste == nil {
		t.Fatal("there is no cluster to pick, and the paste box did not open")
	}
	if v := screen(m); !strings.Contains(v, "paste a kubeconfig") {
		t.Errorf("the screen does not show the paste box:\n%s", v)
	}

	// esc goes back to the empty picker, and a reload there leaves it closed:
	// only the launch's own rows open it.
	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})
	if m.paste != nil || m.picker() == nil {
		t.Fatalf("after esc: paste box open = %v, active page %T; want the picker", m.paste != nil, m.active())
	}
	_, reload := m.Update(clustersChanged{})
	m.Update(loadedRows(t, reload))
	if m.paste != nil {
		t.Error("a reload with no rows opened the paste box again")
	}
}

// Without a store a paste could not be saved, so the empty picker stays and its
// p says why.
func TestLaunchEmptyWithoutStoreStaysOnPicker(t *testing.T) {
	t.Setenv("KUBECONFIG", filepath.Join(t.TempDir(), "none"))
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	m, launch := launchWith(t, Config{Sink: func(tea.Msg) {}, Launch: LaunchPicker, StoreErr: "no config folder"})
	m.Update(launchLoad(t, launch))

	if m.paste != nil {
		t.Error("the paste box opened with no store to save into")
	}
	if m.picker() == nil {
		t.Fatalf("the active page is %T, want the cluster picker", m.active())
	}
	_, p := m.handleKey(textKey('p'))
	if p == nil {
		t.Fatal("p in the launch picker did nothing")
	}
	if got, ok := p().(msg.Toast); !ok || got.Text != "saved clusters are off: no config folder" {
		t.Errorf("p = %#v, want a toast saying why paste is off", got)
	}
}

// A pick that fails at launch has no Session to fall back on. It is not a dead
// end: the picker stays, with the error under the row.
func TestLaunchErrorReturnsToPicker(t *testing.T) {
	s := savedStore(t, writeKubeconfig(t, fmt.Sprintf(brokenAndGood, fastServer(t))))
	m, launch := launchModel(t, s)
	m.Update(launchLoad(t, launch))
	m.handleKey(tea.KeyPressMsg{Code: 'j', Text: "j"}) // onto bad

	_, pick := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	connect := deliver(t, m, pick)
	deliver(t, m, connect)

	if m.fatal != nil {
		t.Fatalf("a failed launch connect is a dead end: %v", m.fatal)
	}
	if m.picker() == nil {
		t.Fatalf("the active page is %T, want the picker back", m.active())
	}
	if m.connecting != nil {
		t.Errorf("connecting = %v after the connect failed, want nil", m.connecting)
	}
	v := screen(m)
	if !strings.Contains(v, "✗ load kubeconfig:") {
		t.Errorf("the picker does not show the error under the row:\n%s", v)
	}
	if strings.Contains(v, "connecting…") {
		t.Errorf("the row still says connecting after the failure:\n%s", v)
	}
	if m.toast != nil {
		t.Errorf("toast %q: the row shows the error, a toast says it twice", m.toast.Text)
	}
}

// `kubetui -context bad` fails before any picker was shown: kubetui opens the
// launch picker on that row, with the error under it, and toasts it too, since
// a context that does not exist has no row to show it on.
func TestLaunchDirectErrorOpensPicker(t *testing.T) {
	s := savedStore(t, writeKubeconfig(t, fmt.Sprintf(brokenAndGood, fastServer(t))))
	bad := k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "bad"}
	m, connect := launchWith(t, Config{Sink: func(tea.Msg) {}, Launch: LaunchDirect, Start: bad, StartKind: "portforwards", Store: s})

	load := deliver(t, m, connect)

	if m.fatal != nil {
		t.Fatalf("a failed launch connect is a dead end: %v", m.fatal)
	}
	if m.picker() == nil {
		t.Fatalf("the active page is %T, want the launch picker", m.active())
	}
	if m.toast == nil || !strings.HasPrefix(m.toast.Text, "connect bad: ") {
		t.Errorf("toast = %+v, want one starting %q", m.toast, "connect bad: ")
	}
	m.Update(launchLoad(t, load))
	if v := screen(m); !strings.Contains(v, "✗ load kubeconfig:") || !strings.Contains(v, "q quit") {
		t.Errorf("the launch picker does not show the error under bad's row:\n%s", v)
	}
	_, pick := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if req, ok := pick().(view.SwitchContextRequest); !ok || req.Ref != bad {
		t.Errorf("enter = %#v, want the cursor on bad, the row that failed", req)
	}
}

// Before a cluster is connected only the picker's keys, ? and q work: :, / and
// the digit jumps need a cluster.
func TestLaunchKeysBeforeSession(t *testing.T) {
	fast := fastServer(t)
	m, launch := launchModel(t, savedStore(t, writeKubeconfig(t, fmt.Sprintf(threeClusters, fast, fast, fast))))
	m.Update(launchLoad(t, launch))

	for _, k := range []tea.KeyPressMsg{textKey(':'), textKey('/'), textKey('1'), textKey('0'), {Code: tea.KeyEsc}} {
		_, cmd := m.handleKey(k)
		if cmd != nil || m.mode != modeNone || m.showHelp || m.picker() == nil {
			t.Errorf("%q acted before a cluster was connected: mode %v, cmd %v, page %T", k.String(), m.mode, cmd != nil, m.active())
		}
	}
	// The picker's own keys still reach it.
	if _, p := m.handleKey(textKey('p')); p == nil {
		t.Error("p in the launch picker did nothing")
	} else if _, ok := p().(view.OpenPasteBox); !ok {
		t.Error("p in the launch picker did not ask for the paste box")
	}
	m.handleKey(textKey('?'))
	if !m.showHelp {
		t.Error("? did not open help")
	}
	if v := screen(m); strings.Contains(v, "command line") {
		t.Errorf("help offers the command line, which needs a cluster:\n%s", v)
	}
	m.handleKey(textKey('x'))
	if m.showHelp {
		t.Error("a key did not close help")
	}
	_, quit := m.handleKey(textKey('q'))
	if quit == nil {
		t.Fatal("q did nothing")
	}
	if _, ok := quit().(tea.QuitMsg); !ok {
		t.Error("q did not quit")
	}
}

func TestHeaderWithoutSession(t *testing.T) {
	m := &Model{theme: style.Default(), width: 80, height: 24}
	defer func() {
		if r := recover(); r != nil {
			t.Fatalf("renderHeader with no session panicked: %v", r)
		}
	}()
	if h := ansi.Strip(m.renderHeader()); !strings.Contains(h, "Context") {
		t.Errorf("header = %q, want its labels with no values", h)
	}
}
