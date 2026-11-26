package app

import (
	"bytes"
	"errors"
	"fmt"
	"io/fs"
	"net/http"
	"os"
	"path/filepath"
	"slices"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// savedStore opens a Store in a temp folder. It also points the default
// kubeconfig at kubeconfig ("" for none: a missing file lists no contexts) and
// rules out in-cluster, so the picker lists only what the test set up.
func savedStore(t *testing.T, kubeconfig string) *clusters.Store {
	t.Helper()
	if kubeconfig == "" {
		kubeconfig = filepath.Join(t.TempDir(), "none")
	}
	t.Setenv("KUBECONFIG", kubeconfig)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	s, err := clusters.Open(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	return s
}

// remember records each ref in the store, as connecting to it does.
func remember(t *testing.T, s *clusters.Store, refs ...k8s.ClusterRef) {
	t.Helper()
	for _, r := range refs {
		if err := s.Update(clusters.KeyFor(r, ""), func(*clusters.Entry) {}); err != nil {
			t.Fatal(err)
		}
	}
}

// messages runs cmd, and each command of a batch, and returns what they produce.
// Only for commands that hold no toast tick, which would sleep.
func messages(cmd tea.Cmd) []tea.Msg {
	if cmd == nil {
		return nil
	}
	got := cmd()
	batch, ok := got.(tea.BatchMsg)
	if !ok {
		return []tea.Msg{got}
	}
	var out []tea.Msg
	for _, c := range batch {
		out = append(out, messages(c)...)
	}
	return out
}

// loadedRows runs cmd and returns the ClustersLoaded it produces.
func loadedRows(t *testing.T, cmd tea.Cmd) view.ClustersLoaded {
	t.Helper()
	for _, m := range messages(cmd) {
		if l, ok := m.(view.ClustersLoaded); ok {
			if l.Err != nil {
				t.Fatalf("listing clusters: %v", l.Err)
			}
			return l
		}
	}
	t.Fatal("the command did not load the cluster rows")
	return view.ClustersLoaded{}
}

func labelsOf(rows []clusters.Row) []string {
	out := make([]string, len(rows))
	for i, r := range rows {
		out[i] = r.Label
	}
	return out
}

// pickerView renders the active page, which must be the picker, without styling.
func pickerView(t *testing.T, m *Model) string {
	t.Helper()
	if m.picker() == nil {
		t.Fatalf("the active page is %T, not the cluster picker", m.active())
	}
	return ansi.Strip(m.active().View(120, 30))
}

func TestCtxOpensPickerWithRows(t *testing.T) {
	m, a, _, _ := connected(t, fastServer(t))
	m.store = savedStore(t, "")
	remember(t, m.store, a.Ref)

	_, cmd := m.runCommand("ctx")

	if m.picker() == nil {
		t.Fatalf("the active page is %T, not the cluster picker", m.active())
	}
	got := loadedRows(t, cmd)
	if want := []string{"a", "b", "c"}; !slices.Equal(labelsOf(got.Rows), want) {
		t.Fatalf("rows = %v, want %v", labelsOf(got.Rows), want)
	}
	m.Update(got)
	if v := pickerView(t, m); !strings.Contains(v, "● a") {
		t.Errorf("the picker does not mark the connected cluster:\n%s", v)
	}
}

func TestCtxNameOneMatchSwitches(t *testing.T) {
	m, a, refB, _ := connected(t, fastServer(t))
	m.store = savedStore(t, "")
	remember(t, m.store, a.Ref)

	_, lookup := m.runCommand("ctx b")
	if m.connecting != nil {
		t.Fatal("the name was resolved in Update; listing clusters reads files")
	}
	// The one match goes back through Update as enter's SwitchContextRequest.
	pick := deliver(t, m, lookup)
	connect := deliver(t, m, pick)

	// The row for b names the --kubeconfig file, so the switch stays in it.
	if m.connecting == nil || *m.connecting != refB {
		t.Fatalf("connecting = %v, want %+v", m.connecting, refB)
	}
	deliver(t, m, connect)
	if m.sess.Ref != refB {
		t.Errorf("on %+v after the switch, want %+v", m.sess.Ref, refB)
	}
}

// ":ctx <name>" with one match follows enter's rules (view.PickMsg): the
// connected cluster and a missing file only toast, and an unreviewed paste asks
// for review. None of them connects.
func TestCtxNameFollowsEnterRules(t *testing.T) {
	fast := fastServer(t)
	m, a, _, _ := connected(t, fast)
	m.store = savedStore(t, "")
	remember(t, m.store, a.Ref, k8s.ClusterRef{
		Source: k8s.SourceFile, Path: filepath.Join(t.TempDir(), "gone.yaml"), Context: "gone",
	})
	if _, err := m.store.SavePaste("fresh", []byte(fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: k
  cluster: {server: "%s"}
users:
- name: u
  user: {token: t}
contexts:
- name: new
  context: {cluster: k, user: u}
`, fast))); err != nil {
		t.Fatal(err)
	}

	for _, tc := range []struct{ name, toast string }{
		{name: "a", toast: "already on a"},
		{name: "gone", toast: "gone: file missing"},
		{name: "fresh"}, // unreviewed: no toast, a ReviewCluster
	} {
		t.Run(tc.name, func(t *testing.T) {
			m.toast = nil
			_, lookup := m.runCommand("ctx " + tc.name)
			got := deliver(t, m, lookup)()
			if tc.toast == "" {
				if r, ok := got.(view.ReviewCluster); !ok || r.Row.Label != "fresh" {
					t.Fatalf("pick = %#v, want a ReviewCluster for fresh", got)
				}
			} else {
				if _, ok := got.(msg.Toast); !ok {
					t.Fatalf("pick = %#v, want a toast", got)
				}
				m.Update(got)
				if m.toast == nil || m.toast.Text != tc.toast {
					t.Errorf("toast = %+v, want %q", m.toast, tc.toast)
				}
			}
			if m.connecting != nil || m.sess != a {
				t.Errorf("connecting = %v, session changed = %v; want no connect", m.connecting, m.sess != a)
			}
		})
	}
}

func TestCtxNameSeveralMatchesOpensPicker(t *testing.T) {
	fast := fastServer(t)
	m, a, _, _ := connected(t, fast)
	other := writeKubeconfig(t, fmt.Sprintf(`apiVersion: v1
kind: Config
clusters:
- name: k
  cluster: {server: "%s"}
users:
- name: u
  user: {token: t}
contexts:
- name: b
  context: {cluster: k, user: u}
- name: x
  context: {cluster: k, user: u}
`, fast))
	m.store = savedStore(t, other)
	remember(t, m.store, a.Ref)

	_, lookup := m.runCommand("ctx b")
	deliver(t, m, lookup)

	if m.picker() == nil {
		t.Fatalf("two clusters named b: the active page is %T, want the picker", m.active())
	}
	if m.connecting != nil {
		t.Fatalf("two clusters named b, and it connected to %+v without asking", *m.connecting)
	}
	// Rows sort by label, then path: the cursor belongs on the default
	// kubeconfig's b, not on the connected a where the picker starts.
	_, enter := m.handleKey(tea.KeyPressMsg{Code: tea.KeyEnter})
	if enter == nil {
		t.Fatal("enter on the picker did nothing")
	}
	want := k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "b"}
	if req, ok := enter().(view.SwitchContextRequest); !ok || req.Ref != want {
		t.Errorf("enter = %#v, want a switch to %+v", req, want)
	}
}

func TestCtxNameNoMatchToasts(t *testing.T) {
	m, a, _, _ := connected(t, fastServer(t))
	m.store = savedStore(t, "")
	remember(t, m.store, a.Ref)

	_, lookup := m.runCommand("ctx nope")
	if m.connecting != nil {
		t.Fatalf("connecting to %+v before any cluster was found by that name", *m.connecting)
	}
	m.Update(lookup())

	if want := `no cluster named "nope"`; m.toast == nil || m.toast.Text != want {
		t.Errorf("toast = %+v, want %q", m.toast, want)
	}
	if m.connecting != nil || m.sess != a || m.picker() != nil {
		t.Errorf("no match acted: connecting = %v, session changed = %v, picker open = %v",
			m.connecting, m.sess != a, m.picker() != nil)
	}
}

// f in the picker remembers a file as --kubeconfig does: by absolute path, so the
// entry still works when kubetui starts in another folder.
func TestAddKubeconfigFileRemembersAbsolutePath(t *testing.T) {
	s := savedStore(t, "")
	t.Chdir(t.TempDir())
	data := fmt.Sprintf(threeClusters, "https://a.example", "https://b.example", "https://c.example")
	if err := os.WriteFile("dev.yaml", []byte(data), 0o600); err != nil {
		t.Fatal(err)
	}
	wd, err := os.Getwd()
	if err != nil {
		t.Fatal(err)
	}
	want := filepath.Join(wd, "dev.yaml")
	m := New(Config{Store: s})

	_, add := m.Update(view.AddKubeconfigFile{Path: "dev.yaml"})
	_, reload := m.Update(add())

	if m.toast != nil {
		t.Fatalf("toast %q, want the file added", m.toast.Text)
	}
	entries, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	paths := map[string]string{} // context → path
	for _, e := range entries {
		if e.Source != k8s.SourceFile {
			t.Errorf("entry %+v, want source file", e)
		}
		paths[e.Context] = e.Path
	}
	if len(paths) != 3 || paths["a"] != want || paths["b"] != want || paths["c"] != want {
		t.Errorf("remembered %v, want a, b and c at %s", paths, want)
	}
	if rows := loadedRows(t, reload).Rows; !slices.Equal(labelsOf(rows), []string{"a", "b", "c"}) {
		t.Errorf("the picker reloaded %v, want the file's contexts", labelsOf(rows))
	}
}

func TestAddKubeconfigFileRejectsNonKubeconfig(t *testing.T) {
	s := savedStore(t, "")
	dir := t.TempDir()
	notes := filepath.Join(dir, "notes.txt")
	empty := filepath.Join(dir, "empty.yaml")
	if err := os.WriteFile(notes, []byte("remember the milk\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(empty, []byte("apiVersion: v1\nkind: Config\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	for _, path := range []string{notes, empty, filepath.Join(dir, "missing.yaml")} {
		m := New(Config{Store: s})
		_, add := m.Update(view.AddKubeconfigFile{Path: path})
		m.Update(add())
		if m.toast == nil || m.toast.Level != msg.LevelError {
			t.Errorf("%s: toast = %+v, want an error", filepath.Base(path), m.toast)
		}
	}
	if entries, err := s.Entries(); err != nil || len(entries) != 0 {
		t.Errorf("entries = %+v (%v), want none", entries, err)
	}
}

// savedPaste saves a three-context paste named dev with servers a, b and c, and
// records every context, as approving a paste does. It returns the file's
// base name and a ref maker.
func savedPaste(t *testing.T, s *clusters.Store, a, b, c string) (string, func(string) k8s.ClusterRef) {
	t.Helper()
	file, err := s.SavePaste("dev", []byte(fmt.Sprintf(threeClusters, a, b, c)))
	if err != nil {
		t.Fatal(err)
	}
	ref := func(name string) k8s.ClusterRef {
		return k8s.ClusterRef{Source: k8s.SourcePasted, Path: s.PastePath(file), Context: name}
	}
	for _, name := range []string{"a", "b", "c"} {
		if err := s.Update(clusters.KeyFor(ref(name), ""), func(e *clusters.Entry) { e.Name = "dev" }); err != nil {
			t.Fatal(err)
		}
	}
	return file, ref
}

func TestForgetPastedDeletesFile(t *testing.T) {
	s := savedStore(t, "")
	file, _ := savedPaste(t, s, "https://a.example", "https://b.example", "https://c.example")
	rows := loadedRows(t, loadClustersCmd(s)).Rows
	if want := []string{"dev / a", "dev / b", "dev / c"}; !slices.Equal(labelsOf(rows), want) {
		t.Fatalf("rows = %v, want %v", labelsOf(rows), want)
	}
	m := New(Config{Store: s})

	_, forget := m.Update(view.ForgetCluster{Row: rows[1]})
	_, reload := m.Update(forget())

	if m.toast != nil {
		t.Fatalf("toast %q, want the paste removed", m.toast.Text)
	}
	if _, err := os.Stat(s.PastePath(file)); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("the pasted file is still there (stat: %v)", err)
	}
	if entries, err := s.Entries(); err != nil || len(entries) != 0 {
		t.Errorf("entries = %+v (%v), want every context of the paste gone", entries, err)
	}
	if rows := loadedRows(t, reload).Rows; len(rows) != 0 {
		t.Errorf("the picker reloaded %v, want no rows", labelsOf(rows))
	}
}

func TestForgetFileLeavesFile(t *testing.T) {
	s := savedStore(t, "")
	data := fmt.Sprintf(threeClusters, "https://a.example", "https://b.example", "https://c.example")
	path, kept := writeKubeconfig(t, data), writeKubeconfig(t, data)
	file := func(p, name string) k8s.ClusterRef {
		return k8s.ClusterRef{Source: k8s.SourceFile, Path: p, Context: name}
	}
	remember(t, s, file(path, "a"), file(path, "b"), file(kept, "a"))
	var row clusters.Row
	for _, r := range loadedRows(t, loadClustersCmd(s)).Rows {
		if r.Ref == file(path, "b") {
			row = r
		}
	}
	m := New(Config{Store: s})

	_, forget := m.Update(view.ForgetCluster{Row: row})
	_, reload := m.Update(forget())

	if m.toast != nil {
		t.Fatalf("toast %q, want the file forgotten", m.toast.Text)
	}
	if got, err := os.ReadFile(path); err != nil || !bytes.Equal(got, []byte(data)) {
		t.Errorf("the forgotten file changed: %v", err)
	}
	entries, err := s.Entries()
	if err != nil || len(entries) != 1 || entries[0].Path != kept {
		t.Errorf("entries = %+v (%v), want only the other file's", entries, err)
	}
	for _, r := range loadedRows(t, reload).Rows {
		if r.Ref.Path == path {
			t.Errorf("the picker still lists %+v", r.Ref)
		}
	}
}

// Deleting the pasted file under a live Session would break it, and an oidc
// token refresh would write the file back unreviewed. Any row of that file
// deletes it, so every one is refused.
func TestRemoveConnectedEntryIsRefused(t *testing.T) {
	fast := fastServer(t)
	s := savedStore(t, "")
	file, ref := savedPaste(t, s, fast, fast, fast)
	m := New(Config{Sink: func(tea.Msg) {}, Start: ref("a"), StartKind: "portforwards", StartKindExplicit: true, Store: s})
	t.Cleanup(func() {
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	deliver(t, m, m.Init())
	if m.sess == nil {
		t.Fatalf("did not connect to the paste: fatal = %v", m.fatal)
	}

	for _, r := range loadedRows(t, loadClustersCmd(s)).Rows {
		m.toast = nil
		m.Update(view.ForgetCluster{Row: r})
		if want := "disconnect first: switch to another cluster"; m.toast == nil || m.toast.Text != want {
			t.Errorf("remove %s: toast = %+v, want %q", r.Label, m.toast, want)
		}
	}
	if _, err := os.Stat(s.PastePath(file)); err != nil {
		t.Errorf("the connected cluster's file is gone: %v", err)
	}
	if entries, err := s.Entries(); err != nil || len(entries) != 3 {
		t.Errorf("entries = %+v (%v), want all three kept", entries, err)
	}
}

func TestSwitchFailureShowsOnPickerRow(t *testing.T) {
	m, a, _, _ := connected(t, fastServer(t))
	broken := writeKubeconfig(t, `apiVersion: v1
kind: Config
users:
- name: u
  user: {token: t}
contexts:
- name: bad
  context: {cluster: gone, user: u}
`)
	m.store = savedStore(t, "")
	bad := k8s.ClusterRef{Source: k8s.SourceFile, Path: broken, Context: "bad"}
	remember(t, m.store, bad)
	_, load := m.runCommand("ctx")
	m.Update(loadedRows(t, load))

	_, connect := m.Update(view.SwitchContextRequest{Ref: bad}) // enter on its row
	if v := pickerView(t, m); !strings.Contains(v, "connecting…") {
		t.Errorf("the row does not say it is connecting:\n%s", v)
	}
	deliver(t, m, connect)

	if m.sess != a || disposed(a) {
		t.Fatal("a failed switch took the current session down")
	}
	v := pickerView(t, m)
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

// esc on the picker stays on the current cluster: the connect it started is
// cut short and its Session disposed.
func TestEscOnPickerCancelsConnect(t *testing.T) {
	probed := make(chan struct{}, 1)
	slow := apiServer(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case probed <- struct{}{}:
		default:
		}
		<-r.Context().Done() // answer only once the client gives up
	})
	m, a, refB, _ := connected(t, slow)
	m.runCommand("ctx")
	_, cmdB := m.Update(view.SwitchContextRequest{Ref: refB})
	result := make(chan tea.Msg, 1)
	go func() { result <- cmdB() }()
	select {
	case <-probed:
	case <-time.After(5 * time.Second):
		t.Fatal("b's version probe never reached its server")
	}

	m.handleKey(tea.KeyPressMsg{Code: tea.KeyEsc})

	if m.picker() != nil {
		t.Error("esc left the picker open")
	}
	if m.connecting != nil {
		t.Errorf("connecting = %v after esc, want nil", m.connecting)
	}
	select {
	case res := <-result:
		m.Update(res) // stale now, so b's Session is disposed
		if r, ok := res.(msg.SessionReady); ok && !disposed(r.Session.(*k8s.Session)) {
			t.Error("the cancelled connect's session was not disposed")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("esc did not cancel b's version probe")
	}
	if m.sess != a || disposed(a) {
		t.Error("esc on the picker left the current cluster")
	}
}
