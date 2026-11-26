package app

import (
	"fmt"
	"strings"
	"testing"
	"unicode"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// evilNames is a pasted kubeconfig whose names carry terminal control: the
// user's BEL ends the OSC 2 window title Bubble Tea writes and the ESC [ after
// it starts a colour, the cluster's BEL and ESC ] 52 write the clipboard, and
// the context's CR moves the cursor back over its own line. It needs no
// review, so one ctrl+s saves it. The test fills in the server.
const evilNames = `apiVersion: v1
kind: Config
current-context: "x\rprod-eu"
clusters:
- name: "c\a\e]52;c;ZWNobyBvd25lZA==\a"
  cluster: {server: "%s"}
users:
- name: "u\a\e[31mINJECTED"
  user: {token: t}
contexts:
- name: "x\rprod-eu"
  context: {cluster: "c\a\e]52;c;ZWNobyBvd25lZA==\a", user: "u\a\e[31mINJECTED"}
`

// evilContext is evilNames' context as the kubeconfig spells it, and
// evilDisplay the same name as kubetui draws it.
const (
	evilContext = "x\rprod-eu"
	evilDisplay = "xprod-eu"
)

// noControls fails when frame, as written to the terminal, holds a control
// character from a kubeconfig: a BEL, a CR, an OSC, or the injected colour.
// The frame's own styling escapes are the only ESCs allowed in it.
func noControls(t *testing.T, what, frame string) {
	t.Helper()
	for _, bad := range []string{"\a", "\r", "\x1b]", "\x1b[31mINJECTED"} {
		if strings.Contains(frame, bad) {
			t.Errorf("%s: the frame holds %q from the kubeconfig:\n%q", what, bad, frame)
		}
	}
	if i := strings.IndexFunc(ansi.Strip(frame), func(r rune) bool { return r != '\n' && unicode.IsControl(r) }); i >= 0 {
		t.Errorf("%s: a control character is left once styling is stripped:\n%q", what, ansi.Strip(frame))
	}
}

// titleClean fails when the window title holds any control character: Bubble
// Tea writes it inside an OSC 2 with no filtering, so a BEL ends the sequence
// and the rest reaches the terminal raw.
func titleClean(t *testing.T, title string) {
	t.Helper()
	if strings.IndexFunc(title, unicode.IsControl) >= 0 {
		t.Errorf("window title %q holds a control character; Bubble Tea writes %q", title, ansi.SetWindowTitle(title))
	}
}

// A pasted kubeconfig's names reach the window title, the header and the
// picker only with their control characters stripped. The raw names stay the
// identifiers: the paste is recorded under its raw context, so its row needs
// no review, and the connect to it finds the context.
func TestPastedNamesAreSanitizedOnScreen(t *testing.T) {
	fast := fastServer(t)
	// The default kubeconfig holds the same names, so the picker also lists a
	// row labelled with the raw context.
	s := savedStore(t, writeKubeconfig(t, fmt.Sprintf(evilNames, fast)))
	r, err := clusters.Inspect([]byte(fmt.Sprintf(evilNames, fast)))
	if err != nil {
		t.Fatalf("inspect: %v", err)
	}
	if len(r.Contexts) != 1 || r.Contexts[0].Name != evilContext || r.Contexts[0].Display != evilDisplay {
		t.Fatalf("contexts = %+v, want the raw name %q shown as %q", r.Contexts, evilContext, evilDisplay)
	}
	if r.CurrentContext != evilContext {
		t.Errorf("current-context = %q, want the raw %q", r.CurrentContext, evilContext)
	}
	file, err := s.SavePaste("evil", r.Data)
	if err != nil {
		t.Fatal(err)
	}
	ps := recordPaste(s, file, "evil", r, r.Contexts[0].Name)
	want := k8s.ClusterRef{Source: k8s.SourcePasted, Path: s.PastePath(file), Context: evilContext}
	if ps.err != nil || ps.ref != want {
		t.Fatalf("recorded %+v (%v), want a connect to %+v", ps.ref, ps.err, want)
	}
	rows := loadedRows(t, loadClustersCmd(s)).Rows
	var pasted []clusters.Row
	for _, row := range rows {
		if row.Source == k8s.SourcePasted {
			pasted = append(pasted, row)
		}
	}
	if len(rows) != 2 || len(pasted) != 1 || pasted[0].Ref != want || pasted[0].NeedsReview {
		t.Fatalf("rows = %+v, want the kubeconfig's and one reviewed pasted row for %+v", rows, want)
	}

	m := New(Config{Sink: func(tea.Msg) {}, Start: ps.ref, StartKind: "portforwards", StartKindExplicit: true, Store: s})
	m.width, m.height = 100, 30
	t.Cleanup(func() {
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	deliver(t, m, m.Init())
	if m.sess == nil || m.sess.Ref != want {
		t.Fatalf("the box's ref did not connect: toast = %+v", m.toast)
	}
	titleClean(t, m.View().WindowTitle)
	noControls(t, "header", m.render())
	if h := ansi.Strip(m.renderHeader()); !strings.Contains(h, evilDisplay) || !strings.Contains(h, "u[31mINJECTED") {
		t.Errorf("the header does not show the names without their control characters:\n%s", h)
	}

	// The picker over the session draws the rows the same way.
	m.openPicker()
	m.Update(view.ClustersLoaded{Rows: rows})
	noControls(t, "picker", m.render())
}

// The report's case through the box itself: a paste whose context, cluster and
// user names hold BEL, ESC and CR is saved with one ctrl+s and an enter,
// connects, and leaves no control character on the screen or in the title.
// Its row is not left needing review.
func TestPasteWithControlCharactersSavesAndConnects(t *testing.T) {
	fast := fastServer(t)
	s := savedStore(t, "")
	m := New(Config{Sink: func(tea.Msg) {}, StartKind: "portforwards", StartKindExplicit: true, Store: s})
	t.Cleanup(func() {
		m.cancelConnect()
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	m.width, m.height = 100, 30
	m.Update(view.OpenPasteBox{})
	pasteIn(t, m, fmt.Sprintf(evilNames, fast))
	if m.paste.err != nil || m.paste.report == nil {
		t.Fatalf("the paste was not read: %v", m.paste.err)
	}
	noControls(t, "the box", m.render())

	ask := press(m, ctrlKey('s'))
	if ask == nil {
		t.Fatal("ctrl+s on a paste that needs no review did not ask for a name")
	}
	m.Update(ask())
	if m.prompt == nil || m.prompt.buf != evilDisplay {
		t.Fatalf("name prompt = %+v, want it opened with %q", m.prompt, evilDisplay)
	}
	noControls(t, "the name prompt", m.render())

	save := deliver(t, m, press(m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	connect := deliver(t, m, save)
	if m.toast != nil {
		t.Fatalf("the save toasted %q", m.toast.Text)
	}
	for _, res := range messages(connect) {
		m.Update(res)
	}
	if m.sess == nil || m.sess.Ref.Context != evilContext {
		t.Fatalf("not connected to the raw context %q: toast = %+v", evilContext, m.toast)
	}
	titleClean(t, m.View().WindowTitle)
	noControls(t, "after the connect", m.render())
	for _, r := range loadedRows(t, loadClustersCmd(s)).Rows {
		if r.NeedsReview {
			t.Errorf("row %q still needs review after the save", r.Label)
		}
	}
}

// A connect error is shown through clusters.SafeLoadError: client-go prints a
// duplicate list with %v, token included.
func TestConnectErrorHidesSecrets(t *testing.T) {
	dup := writeKubeconfig(t, `apiVersion: v1
kind: Config
clusters:
- name: k
  cluster: {server: "https://k.example"}
users:
- name: u
  user: {token: tok-SECRET-1}
- name: u
  user: {token: tok-SECRET-2}
contexts:
- name: dup
  context: {cluster: k, user: u}
`)
	ref := k8s.ClusterRef{Source: k8s.SourceFile, Path: dup, Context: "dup"}

	t.Run("picker row", func(t *testing.T) {
		m, _, _, _ := connected(t, fastServer(t))
		m.store = savedStore(t, "")
		remember(t, m.store, ref)
		_, load := m.runCommand("ctx")
		m.Update(loadedRows(t, load))
		_, connect := m.Update(view.SwitchContextRequest{Ref: ref})
		deliver(t, m, connect)
		// Wide enough that nothing is cut at the edge: the whole error shows.
		v := ansi.Strip(m.active().View(2000, 30))
		if !strings.Contains(v, "✗ load kubeconfig:") {
			t.Fatalf("the picker does not show the error:\n%s", v)
		}
		if strings.Contains(v, "tok-SECRET") {
			t.Errorf("the picker row shows the token:\n%s", v)
		}
	})

	t.Run("toast", func(t *testing.T) {
		m, _, _, _ := connected(t, fastServer(t))
		_, connect := m.switchContext(ref) // no picker open: the toast says why
		deliver(t, m, connect)
		if m.toast == nil || !strings.HasPrefix(m.toast.Text, "connect dup: ") {
			t.Fatalf("toast = %+v, want the connect error", m.toast)
		}
		if strings.Contains(m.toast.Text, "tok-SECRET") {
			t.Errorf("the toast shows the token: %q", m.toast.Text)
		}
	})
}
