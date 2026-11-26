package app

import (
	"errors"
	"fmt"
	"path/filepath"
	"slices"
	"strings"
	"testing"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// pasteTwo is a kubeconfig with two token contexts. Its token, CA data and
// namespace must never reach the screen.
const pasteTwo = `apiVersion: v1
kind: Config
current-context: prod
clusters:
- name: dev
  cluster:
    server: https://dev.example:6443
    certificate-authority-data: Q0VSVElGSUNBVEUtREFUQS1TRUNSRVQ=
- name: prod
  cluster:
    server: https://prod.example:6443
users:
- name: admin
  user:
    token: tok-SECRET-0123456789
contexts:
- name: dev
  context:
    cluster: dev
    user: admin
    namespace: team-hidden-ns
- name: prod
  context:
    cluster: prod
    user: admin
`

// pasteExec has a token context, dev, on the server the test fills in, and an
// exec context, dev-sso, whose login command takes a secret.
const pasteExec = `apiVersion: v1
kind: Config
current-context: dev
clusters:
- name: dev
  cluster: {server: "%s"}
users:
- name: tok
  user: {token: tok-SECRET-0123456789}
- name: sso
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: aws
      args: [eks, get-token, --cluster-name, dev, --token, hunter2]
      env:
      - {name: AWS_PROFILE, value: profile-SECRET}
contexts:
- name: dev
  context: {cluster: dev, user: tok}
- name: dev-sso
  context: {cluster: dev, user: sso}
`

// pasteBoxModel opens the paste box on a model with a store and no Session.
func pasteBoxModel(t *testing.T) *Model {
	t.Helper()
	m := New(Config{Store: savedStore(t, "")})
	m.width, m.height = 120, 40
	m.Update(view.OpenPasteBox{})
	if m.paste == nil {
		t.Fatal("OpenPasteBox did not open the paste box")
	}
	return m
}

// pasteIn delivers a paste, and then the check of the text it starts.
func pasteIn(t *testing.T, m *Model, s string) {
	t.Helper()
	if _, check := m.Update(tea.PasteMsg{Content: s}); check != nil {
		m.Update(check())
	}
}

// press delivers keys and returns the command the last one answered with.
func press(m *Model, keys ...tea.KeyPressMsg) tea.Cmd {
	var cmd tea.Cmd
	for _, k := range keys {
		_, cmd = m.Update(k)
	}
	return cmd
}

func ctrlKey(r rune) tea.KeyPressMsg { return tea.KeyPressMsg{Code: r, Mod: tea.ModCtrl} }

func textKey(r rune) tea.KeyPressMsg {
	if r == ' ' {
		return tea.KeyPressMsg{Code: tea.KeySpace, Text: " "}
	}
	return tea.KeyPressMsg{Code: r, Text: string(r)}
}

// typeKeys is s as a terminal that types a paste in sends it: enter or ctrl+j
// for each newline, tab for each two-space indent and the rest as text.
func typeKeys(s string) []tea.KeyPressMsg {
	var keys []tea.KeyPressMsg
	for i, line := range strings.Split(s, "\n") {
		if i > 0 {
			nl := tea.KeyPressMsg{Code: tea.KeyEnter}
			if i%2 == 0 {
				nl = ctrlKey('j')
			}
			keys = append(keys, nl)
		}
		rest := strings.TrimLeft(line, " ")
		indent := len(line) - len(rest)
		for n := indent / 2; n > 0; n-- {
			keys = append(keys, tea.KeyPressMsg{Code: tea.KeyTab})
		}
		if indent%2 == 1 {
			rest = " " + rest
		}
		for _, r := range rest {
			keys = append(keys, textKey(r))
		}
	}
	return keys
}

// boxView renders the paste box without styling.
func boxView(m *Model) string { return ansi.Strip(m.renderPasteBox(m.height)) }

func contextNames(r *clusters.Report) []string {
	var out []string
	for _, c := range r.Contexts {
		out = append(out, c.Name)
	}
	return out
}

func TestPasteBoxTakesPasteWhole(t *testing.T) {
	m := pasteBoxModel(t)

	_, check := m.Update(tea.PasteMsg{Content: pasteTwo})
	if check == nil {
		t.Fatal("a paste started no check of the text")
	}
	if m.paste.report != nil {
		t.Fatal("the paste was parsed in Update; Inspect opens the files a kubeconfig names")
	}
	m.Update(check())

	b := m.paste
	if b.err != nil || b.report == nil {
		t.Fatalf("report = %v, err = %v; want the two contexts", b.report, b.err)
	}
	if got, want := contextNames(b.report), []string{"dev", "prod"}; !slices.Equal(got, want) {
		t.Errorf("contexts = %v, want %v", got, want)
	}
	if got := b.selected(); got != "prod" {
		t.Errorf("selected %q, want the paste's current-context prod", got)
	}
	if b.buf.String() != pasteTwo {
		t.Error("the box does not hold the paste as sent")
	}
	v := boxView(m)
	if want := fmt.Sprintf("received %d lines, 0.5 KB", strings.Count(pasteTwo, "\n")); !strings.Contains(v, want) {
		t.Errorf("the box does not say %q:\n%s", want, v)
	}
	for _, s := range []string{"› prod", "https://prod.example:6443", "dev", "https://dev.example:6443"} {
		if !strings.Contains(v, s) {
			t.Errorf("the box does not show %q:\n%s", s, v)
		}
	}
}

func TestPasteBoxConcatenatesPastes(t *testing.T) {
	t.Run("mid-line", func(t *testing.T) {
		m := pasteBoxModel(t)
		half := len(pasteTwo) / 2
		pasteIn(t, m, pasteTwo[:half])
		pasteIn(t, m, pasteTwo[half:])

		b := m.paste
		if b.err != nil || b.report == nil {
			t.Fatalf("report = %v, err = %v; want the two contexts", b.report, b.err)
		}
		if got := contextNames(b.report); len(got) != 2 {
			t.Errorf("contexts = %v, want dev and prod", got)
		}
		if string(b.report.Data) != pasteTwo {
			t.Error("the two pastes were not joined into one kubeconfig")
		}
	})

	// Terminals send CRLF or a lone CR; split between the CR and the LF, the
	// break is still one line.
	t.Run("crlf split", func(t *testing.T) {
		m := pasteBoxModel(t)
		crlf := strings.ReplaceAll(pasteTwo, "\n", "\r\n")
		cut := strings.Index(crlf, "\r\n") + 1
		pasteIn(t, m, crlf[:cut])
		pasteIn(t, m, crlf[cut:])

		b := m.paste
		if b.err != nil || b.report == nil || string(b.report.Data) != pasteTwo {
			t.Fatalf("err = %v; want the CRLF paste read as the LF one", b.err)
		}
		if got, want := b.lineCount(), strings.Count(pasteTwo, "\n"); got != want {
			t.Errorf("lines = %d, want %d", got, want)
		}
	})
}

func TestPasteBoxKeystrokesBuildSameText(t *testing.T) {
	pasted := pasteBoxModel(t)
	pasteIn(t, pasted, pasteTwo)

	m := pasteBoxModel(t)
	// A slip fixed with backspace, one of them on a two-byte rune.
	keys := []tea.KeyPressMsg{textKey('Z'), {Code: tea.KeyBackspace}, textKey('é'), {Code: tea.KeyBackspace}}
	keys = append(keys, typeKeys(pasteTwo)...)
	for _, k := range keys {
		if _, cmd := m.Update(k); cmd != nil {
			t.Fatalf("key %s answered with a command; the text is checked on ctrl+s, not per key", k)
		}
	}
	if m.paste.report != nil || m.paste.err != nil {
		t.Fatal("typed text was checked before ctrl+s")
	}
	if got := m.paste.lineCount(); got != pasted.paste.lineCount() {
		t.Errorf("typed %d lines, pasted %d", got, pasted.paste.lineCount())
	}

	check := press(m, ctrlKey('s'))
	if check == nil {
		t.Fatal("ctrl+s did not check the typed text")
	}
	_, ask := m.Update(check())

	if m.paste.err != nil || m.paste.report == nil {
		t.Fatalf("typed text: err = %v", m.paste.err)
	}
	if string(m.paste.report.Data) != string(pasted.paste.report.Data) {
		t.Errorf("typing built\n%s\nwant the pasted text", m.paste.report.Data)
	}
	// Nothing to review, so that ctrl+s goes on to the name.
	if ask == nil {
		t.Fatal("ctrl+s on a checked paste did not ask for a name")
	}
	if req, ok := ask().(view.PromptRequest); !ok || req.Title != "Name this cluster" {
		t.Errorf("ctrl+s asked %#v, want the name prompt", req)
	}
}

// Pages only see keys after the global ones, and base64 almost always holds a
// q: in the box every one of them is text.
func TestPasteBoxCapturesGlobalKeys(t *testing.T) {
	page := &lifePage{name: "pods"}
	m := pasteBoxModel(t)
	m.pages = []view.Page{page}

	for _, k := range []tea.KeyPressMsg{textKey('q'), textKey(':'), textKey('/'), textKey('?'), textKey('1'), textKey('0'), textKey('r'), textKey('p'), ctrlKey('d')} {
		if _, cmd := m.Update(k); cmd != nil {
			t.Errorf("key %s answered with a command", k)
		}
	}
	m.Update(tea.PasteMsg{Content: "y"})

	if m.paste == nil {
		t.Fatal("a key closed the box")
	}
	if got, want := m.paste.buf.String(), "q:/?10rpy"; got != want {
		t.Errorf("box text = %q, want %q", got, want)
	}
	if m.mode != modeNone || m.showHelp {
		t.Errorf("a key opened something: mode = %v, help = %v", m.mode, m.showHelp)
	}
	if page.updates != 0 {
		t.Errorf("the page under the box got %d messages", page.updates)
	}
}

func TestPasteBoxNeverRendersPastedText(t *testing.T) {
	m, _, _, _ := connected(t, fastServer(t))
	m.store = savedStore(t, "")
	m.width, m.height = 160, 50
	m.Update(view.OpenPasteBox{})
	hidden := []string{"tok-SECRET", "Q0VSVElG", "team-hidden-ns", "admin", "apiVersion", "kind:", "certificate-authority", "client.authentication"}
	check := func(what string, also ...string) {
		t.Helper()
		frame := ansi.Strip(m.render())
		for _, s := range append(hidden, also...) {
			if strings.Contains(frame, s) {
				t.Errorf("%s: the screen shows %q from the paste:\n%s", what, s, frame)
			}
		}
	}

	pasteIn(t, m, pasteTwo)
	if frame := ansi.Strip(m.render()); !strings.Contains(frame, "https://dev.example:6443") {
		t.Fatalf("the box is not on the screen:\n%s", frame)
	}
	check("token paste")

	press(m, ctrlKey('u'))
	pasteIn(t, m, fmt.Sprintf(pasteExec, "https://x.example"))
	press(m, ctrlKey('s'))
	frame := ansi.Strip(m.render())
	for _, s := range []string{"aws eks get-token --cluster-name dev --token ••••", "AWS_PROFILE"} {
		if !strings.Contains(frame, s) {
			t.Errorf("the review does not show %q:\n%s", s, frame)
		}
	}
	check("exec review", "hunter2", "profile-SECRET")

	press(m, ctrlKey('u'))
	pasteIn(t, m, "apiVersion: v1\nkind: Secret\nmetadata: {name: s}\ndata: {token: c2VjcmV0LXZhbHVl}\n")
	if m.paste.err == nil {
		t.Fatal("a Secret manifest was taken for a kubeconfig")
	}
	check("parse error", "c2VjcmV0LXZhbHVl")
}

func TestPasteBoxReviewBeforeSave(t *testing.T) {
	m := pasteBoxModel(t)
	pasteIn(t, m, fmt.Sprintf(pasteExec, "https://x.example"))

	if cmd := press(m, ctrlKey('s')); cmd != nil {
		t.Fatalf("the first ctrl+s answered %#v, want the review only", cmd())
	}
	if !m.paste.review || m.prompt != nil {
		t.Fatalf("review = %v, prompt = %v; want the review first", m.paste.review, m.prompt)
	}
	if v := boxView(m); !strings.Contains(v, "ctrl+s I've read this, save and connect · esc cancel") {
		t.Errorf("the footer does not say what ctrl+s now means:\n%s", v)
	}

	// The review was of the text as it was: an edit needs a new one. A paste is
	// checked as it lands; a typed edit is not checked until ctrl+s.
	pasteIn(t, m, "\n")
	if m.paste.review {
		t.Fatal("the review stayed up for pasted text it did not show")
	}
	if cmd := press(m, ctrlKey('s')); cmd != nil || !m.paste.review {
		t.Fatal("after another paste, ctrl+s skipped the review")
	}
	press(m, tea.KeyPressMsg{Code: tea.KeyEnter})
	if m.paste.review {
		t.Fatal("the review stayed up for text it did not check")
	}
	check := press(m, ctrlKey('s'))
	if check == nil {
		t.Fatal("ctrl+s did not check the edited text")
	}
	if _, ask := m.Update(check()); ask != nil || !m.paste.review {
		t.Fatal("the edited text went to the name prompt without a review")
	}

	ask := press(m, ctrlKey('s'))
	if ask == nil {
		t.Fatal("ctrl+s after the review did nothing")
	}
	req, ok := ask().(view.PromptRequest)
	if !ok {
		t.Fatalf("ctrl+s after the review answered %#v, want the name prompt", req)
	}
	if req.Title != "Name this cluster" || req.Label != "Name" || req.Initial != "dev" {
		t.Errorf("prompt = %q/%q/%q, want Name this cluster/Name/dev", req.Title, req.Label, req.Initial)
	}
	if req.Validate == nil || req.Validate("  ") == nil {
		t.Error("an empty name was accepted")
	}
}

func TestPasteBoxEscAsksWhenNotEmpty(t *testing.T) {
	m := pasteBoxModel(t)
	if cmd := press(m, tea.KeyPressMsg{Code: tea.KeyEsc}); cmd != nil || m.paste != nil {
		t.Fatal("esc on an empty box did not just close it")
	}

	m = pasteBoxModel(t)
	press(m, textKey('x'))
	ask := press(m, tea.KeyPressMsg{Code: tea.KeyEsc})
	if ask == nil || m.paste == nil {
		t.Fatal("esc threw away what was received without asking")
	}
	m.Update(ask())
	if m.confirm == nil {
		t.Fatal("esc did not ask")
	}
	// The dialog sits on top of the box and gets the keys and pastes.
	m.Update(tea.PasteMsg{Content: "y"})
	press(m, textKey('n'))
	if m.confirm != nil || m.paste == nil || m.paste.buf.String() != "x" {
		t.Fatalf("no: confirm open = %v, box = %v; want the box back as it was", m.confirm != nil, m.paste)
	}

	m.Update(press(m, tea.KeyPressMsg{Code: tea.KeyEsc})())
	yes := press(m, textKey('y'))
	if yes == nil {
		t.Fatal("yes did nothing")
	}
	m.Update(yes())
	if m.paste != nil {
		t.Error("yes did not close the box")
	}
}

func TestPasteBoxRefusesOversizedPaste(t *testing.T) {
	m := pasteBoxModel(t)
	big := strings.Repeat("a", clusters.MaxPasteBytes+1)

	_, check := m.Update(tea.PasteMsg{Content: big})
	if check != nil {
		m.Update(check())
	}
	if !errors.Is(m.paste.err, clusters.ErrTooLarge) {
		t.Fatalf("err = %v, want %v", m.paste.err, clusters.ErrTooLarge)
	}
	if v := boxView(m); !strings.Contains(v, clusters.ErrTooLarge.Error()) {
		t.Errorf("the box does not say it is too large:\n%s", v)
	}

	m.Update(tea.PasteMsg{Content: "more"})
	press(m, textKey('x'), tea.KeyPressMsg{Code: tea.KeyEnter}, tea.KeyPressMsg{Code: tea.KeyBackspace})
	if m.paste.buf.Len() != len(big) {
		t.Errorf("the box took input past the limit: %d bytes, want %d", m.paste.buf.Len(), len(big))
	}
	if cmd := press(m, ctrlKey('s')); cmd != nil || m.prompt != nil {
		t.Error("ctrl+s acted on text that is too large")
	}

	press(m, ctrlKey('u'))
	press(m, textKey('x'))
	if got := m.paste.buf.String(); got != "x" {
		t.Errorf("after ctrl+u the box holds %q, want it to take input again", got)
	}
}

func TestPasteBoxSaveConnects(t *testing.T) {
	fast := fastServer(t)
	s := savedStore(t, "")
	m := New(Config{Sink: func(tea.Msg) {}, StartKind: "portforwards", StartKindExplicit: true, Store: s})
	t.Cleanup(func() {
		m.cancelConnect()
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	m.width, m.height = 120, 40
	text := fmt.Sprintf(pasteExec, fast)
	m.Update(view.OpenPasteBox{})
	pasteIn(t, m, text)
	press(m, ctrlKey('s')) // the review
	m.Update(press(m, ctrlKey('s'))())
	if m.prompt == nil {
		t.Fatal("no name prompt after the review")
	}

	// The prompt opened on top of the box gets the keys.
	press(m, ctrlKey('u'))
	enter := press(m, append(typeKeys("my cluster"), tea.KeyPressMsg{Code: tea.KeyEnter})...)
	if m.paste == nil || m.paste.buf.String() != text {
		t.Fatal("typing the name reached the box")
	}
	save := deliver(t, m, enter)
	connect := deliver(t, m, save)

	files, err := s.PastedFiles()
	if err != nil || !slices.Equal(files, []string{"my-cluster.yaml"}) {
		t.Fatalf("saved files = %v (%v), want my-cluster.yaml", files, err)
	}
	entries, err := s.Entries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v (%v), want one per context", entries, err)
	}
	for _, e := range entries {
		if e.Source != k8s.SourcePasted || e.File != "my-cluster.yaml" || e.Name != "my cluster" || !slices.Equal(e.ApprovedExec, []string{"aws"}) {
			t.Errorf("entry %+v, want pasted my-cluster.yaml named %q approving aws", e, "my cluster")
		}
	}
	want := k8s.ClusterRef{Source: k8s.SourcePasted, Path: s.PastePath("my-cluster.yaml"), Context: "dev"}
	if m.paste != nil {
		t.Error("the box stayed open after the save")
	}
	if m.connecting == nil || *m.connecting != want {
		t.Fatalf("connecting = %v, want %+v", m.connecting, want)
	}
	for _, res := range messages(connect) {
		m.Update(res)
	}
	if m.sess == nil || m.sess.Ref != want {
		t.Fatalf("not connected to the paste: fatal = %v", m.fatal)
	}
}

// A pasted file with no state record shows the same review, then is recorded
// as it is, under the name its row had, with no name prompt. Its rows keep
// their labels: the recorded Name is the file's stem ("team"), the label
// clusters.Build gives an unreviewed file, not its base name ("team.yaml").
func TestPasteBoxReviewsSavedFile(t *testing.T) {
	fast := fastServer(t)
	s := savedStore(t, "")
	data := strings.Replace(fmt.Sprintf(pasteExec, fast), "current-context: dev", "current-context: dev-sso", 1)
	file, err := s.SavePaste("team", []byte(data))
	if err != nil {
		t.Fatal(err)
	}
	var row clusters.Row
	before := loadedRows(t, loadClustersCmd(s)).Rows
	for _, r := range before {
		if r.Ref.Context == "dev" {
			row = r
		}
	}
	if !row.NeedsReview {
		t.Fatalf("row %+v, want an unreviewed dev", row)
	}
	if want := []string{"team / dev", "team / dev-sso"}; !slices.Equal(pastedLabels(before), want) {
		t.Fatalf("unreviewed labels = %v, want %v", pastedLabels(before), want)
	}
	m := New(Config{Sink: func(tea.Msg) {}, StartKind: "portforwards", StartKindExplicit: true, Store: s})
	t.Cleanup(func() {
		m.cancelConnect()
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	m.width, m.height = 120, 40

	read := deliver(t, m, func() tea.Msg { return view.PickMsg(row, nil) })
	if m.paste == nil || m.paste.existing != file || read == nil {
		t.Fatalf("enter on an unreviewed row did not open its review: box = %+v", m.paste)
	}
	m.Update(read())
	if m.paste.report == nil || len(m.paste.report.Contexts) != 2 {
		t.Fatalf("report = %+v, err = %v; want the file's two contexts", m.paste.report, m.paste.err)
	}
	if got := m.paste.selected(); got != "dev" {
		t.Errorf("selected %q, want dev, the row picked", got)
	}

	// The file is reviewed, not edited.
	press(m, textKey('x'))
	m.Update(tea.PasteMsg{Content: "y"})
	if m.paste.buf.Len() != 0 {
		t.Errorf("the review took input: %q", m.paste.buf.String())
	}

	press(m, ctrlKey('s'))
	if !m.paste.review {
		t.Fatal("ctrl+s did not show the review")
	}
	record := press(m, ctrlKey('s'))
	if record == nil {
		t.Fatal("ctrl+s after the review did nothing")
	}
	res := record()
	if _, ok := res.(pasteSaved); !ok {
		t.Fatalf("ctrl+s answered %#v, want the record saved without a name prompt", res)
	}
	m.Update(res)

	entries, err := s.Entries()
	if err != nil || len(entries) != 2 {
		t.Fatalf("entries = %+v (%v), want one per context", entries, err)
	}
	for _, e := range entries {
		if e.File != file || e.Name != "team" || !slices.Equal(e.ApprovedExec, []string{"aws"}) {
			t.Errorf("entry %+v, want %s named team approving aws", e, file)
		}
	}
	if files, _ := s.PastedFiles(); !slices.Equal(files, []string{file}) {
		t.Errorf("files = %v, want only %s", files, file)
	}
	if m.connecting == nil || *m.connecting != row.Ref {
		t.Errorf("connecting = %v, want %+v", m.connecting, row.Ref)
	}
	after := loadedRows(t, loadClustersCmd(s)).Rows
	for _, r := range after {
		if r.NeedsReview {
			t.Errorf("%s still needs review", r.Label)
		}
	}
	if got, want := pastedLabels(after), pastedLabels(before); !slices.Equal(got, want) {
		t.Errorf("approving renamed the rows: labels %v, want %v as before", got, want)
	}
}

// pastedLabels returns the sorted labels of the pasted rows.
func pastedLabels(rows []clusters.Row) []string {
	var out []string
	for _, r := range rows {
		if r.Source == k8s.SourcePasted {
			out = append(out, r.Label)
		}
	}
	slices.Sort(out)
	return out
}

func TestOpenPasteBoxWithoutStore(t *testing.T) {
	m := New(Config{StoreErr: "no home"})
	m.Update(view.OpenPasteBox{})
	if m.paste != nil {
		t.Fatal("the box opened with nowhere to save the paste")
	}
	if want := "saved clusters are off: no home"; m.toast == nil || m.toast.Text != want || m.toast.Level != msg.LevelWarn {
		t.Errorf("toast = %+v, want a warning %q", m.toast, want)
	}
}

func TestApprovedExecComesFromTheEntry(t *testing.T) {
	s := savedStore(t, "")
	ref := k8s.ClusterRef{Source: k8s.SourcePasted, Path: s.PastePath("team.yaml"), Context: "dev"}
	if got := approvedExec(s, ref); got != nil {
		t.Fatalf("unknown entry approved %v", got)
	}
	err := s.Update(clusters.KeyFor(ref, ""), func(e *clusters.Entry) { e.ApprovedExec = []string{"aws"} })
	if err != nil {
		t.Fatal(err)
	}
	if got := approvedExec(s, ref); !slices.Equal(got, []string{"aws"}) {
		t.Fatalf("approved = %v, want [aws]", got)
	}
}

// manyCtxExec has six contexts on one exec user and a CA file: on a short
// screen a list of contexts drawn before the review pushed the login command
// out of view while ctrl+s still meant "I've read this".
const manyCtxExec = `apiVersion: v1
kind: Config
current-context: c1
clusters:
- name: k
  cluster:
    server: https://example.invalid:6443
    certificate-authority: /etc/kubetui-probe/ca.crt
users:
- name: sso
  user:
    exec:
      apiVersion: client.authentication.k8s.io/v1beta1
      command: definitely-not-aws
      args: [--run, curl-evil]
      env:
      - {name: AWS_PROFILE, value: x}
contexts:
- {name: c1, context: {cluster: k, user: sso}}
- {name: c2, context: {cluster: k, user: sso}}
- {name: c3, context: {cluster: k, user: sso}}
- {name: c4, context: {cluster: k, user: sso}}
- {name: c5, context: {cluster: k, user: sso}}
- {name: c6, context: {cluster: k, user: sso}}
`

// reviewBox opens the paste box over a Session at width×height, pastes text
// and shows its review.
func reviewBox(t *testing.T, width, height int, text string) *Model {
	t.Helper()
	m, _, _, _ := connected(t, fastServer(t))
	m.store = savedStore(t, "")
	m.width, m.height = width, height
	m.Update(view.OpenPasteBox{})
	pasteIn(t, m, text)
	if cmd := press(m, ctrlKey('s')); cmd != nil || !m.paste.review {
		t.Fatalf("ctrl+s did not show the review: err = %v", m.paste.err)
	}
	return m
}

// ctrl+s approves only a review the user can see whole. On 80×24 under the
// header (16 lines) the review comes first, so the command shows and ctrl+s
// approves it. With a little more room the contexts collapse to the selected
// one. On a screen too short for the review, ctrl+s does nothing and the footer
// says to make the window taller.
func TestPasteBoxReviewIsWholeOrNotApproved(t *testing.T) {
	const lastItem = "sends what is in it to the server" // the review's last line
	m := reviewBox(t, 80, 24, manyCtxExec)
	v := screen(m)
	for _, want := range []string{"definitely-not-aws --run curl-evil", "AWS_PROFILE", "/etc/kubetui-probe/ca.crt", "I've read this"} {
		if !strings.Contains(v, want) {
			t.Errorf("80×24: the screen has no %q:\n%s", want, v)
		}
	}
	if !strings.Contains(strings.Join(strings.Fields(v), " "), lastItem) {
		t.Errorf("80×24: the review's last line is cut:\n%s", v)
	}
	ask := press(m, ctrlKey('s'))
	if ask == nil {
		t.Fatal("80×24: ctrl+s on a review shown whole did not approve it")
	}
	if _, ok := ask().(view.PromptRequest); !ok {
		t.Error("80×24: ctrl+s did not go on to the name")
	}

	m = reviewBox(t, 80, 30, manyCtxExec)
	v = screen(m)
	for _, want := range []string{"› c1", "5 more (↑↓)"} {
		if !strings.Contains(v, want) {
			t.Errorf("80×30: the screen has no %q:\n%s", want, v)
		}
	}
	if strings.Contains(v, "c2") {
		t.Errorf("80×30: the contexts did not collapse to the selected one:\n%s", v)
	}
	if strings.Index(v, "definitely-not-aws") > strings.Index(v, "› c1") {
		t.Errorf("80×30: the contexts come before the review:\n%s", v)
	}

	m = reviewBox(t, 80, 14, manyCtxExec)
	v = screen(m)
	if strings.Contains(v, "I've read this") || !strings.Contains(v, "make the window taller to see the whole review") {
		t.Errorf("80×14: the footer does not say the review is cut:\n%s", v)
	}
	if cmd := press(m, ctrlKey('s')); cmd != nil || m.prompt != nil {
		t.Fatal("80×14: ctrl+s approved a review that is cut off the screen")
	}

	// At every height, ctrl+s is offered only with the whole review on screen,
	// and it approves exactly when it is offered.
	for h := 1; h <= 40; h++ {
		m.Update(tea.WindowSizeMsg{Width: 80, Height: h})
		v := screen(m)
		offered := strings.Contains(v, "I've read this")
		shown := strings.Contains(v, "definitely-not-aws --run curl-evil") && strings.Contains(strings.Join(strings.Fields(v), " "), lastItem)
		if offered && !shown {
			t.Errorf("80×%d: ctrl+s is offered with the review cut:\n%s", h, v)
		}
		if offered != m.reviewWhole() {
			t.Errorf("80×%d: the footer offers ctrl+s = %v, but it approves = %v", h, offered, m.reviewWhole())
		}
	}
	m.Update(tea.WindowSizeMsg{Width: 80, Height: 40})
	if cmd := press(m, ctrlKey('s')); cmd == nil {
		t.Fatal("80×40: ctrl+s did not approve once the review fits")
	}

	// With no size yet nothing is drawn, so nothing was read.
	m = reviewBox(t, 80, 24, manyCtxExec)
	m.width, m.height = 0, 0
	if cmd := press(m, ctrlKey('s')); cmd != nil {
		t.Fatal("ctrl+s approved a review before the screen had a size")
	}
}

// At launch, with the whole screen but the title and toast lines, a paste of
// a dozen contexts still shows its review first.
func TestPasteBoxReviewBeforeManyContexts(t *testing.T) {
	var many strings.Builder
	many.WriteString(strings.Split(manyCtxExec, "contexts:\n")[0] + "contexts:\n")
	for i := 1; i <= 12; i++ {
		fmt.Fprintf(&many, "- {name: c%02d, context: {cluster: k, user: sso}}\n", i)
	}
	m := pasteBoxModel(t)
	m.width, m.height = 80, 24
	pasteIn(t, m, many.String())
	press(m, ctrlKey('s'))
	if v := boxView(m); !strings.Contains(v, "definitely-not-aws --run curl-evil") || !strings.Contains(v, "I've read this") {
		t.Errorf("the review is not whole on the launch screen:\n%s", v)
	}
}

// A context's problem is wrapped onto its own indented lines, not cut at the
// edge of the screen.
func TestPasteBoxWrapsContextProblem(t *testing.T) {
	m := pasteBoxModel(t)
	m.width, m.height = 80, 40
	pasteIn(t, m, manyCtxExec)
	v := boxView(m)
	joined := strings.Join(strings.Fields(v), " ")
	if !strings.Contains(joined, "✗ invalid configuration:") || !strings.Contains(joined, "no such file or directory") {
		t.Errorf("the problem is not shown whole:\n%s", v)
	}
	for _, line := range strings.Split(v, "\n") {
		if strings.Contains(line, "✗") && strings.Contains(line, "c1") {
			t.Errorf("the problem shares the context's line, where it is cut: %q", line)
		}
	}
}

// A second paste while the box's own name prompt is open would have drawn the
// kubeconfig, token included, across the prompt, and enter would have made it
// the file name: the save error quoted it. It is refused.
func TestPasteIntoNamePromptIsRefused(t *testing.T) {
	m := pasteBoxModel(t)
	pasteIn(t, m, pasteTwo)
	m.Update(press(m, ctrlKey('s'))())
	if m.prompt == nil {
		t.Fatal("the name prompt did not open")
	}
	for _, p := range []string{pasteTwo, "tok-SECRET-" + strings.Repeat("x", 300)} {
		m.Update(tea.PasteMsg{Content: p})
		if m.prompt.buf != "prod" {
			t.Errorf("the prompt took the paste: %q", m.prompt.buf)
		}
		if want := "the box already holds the kubeconfig; type a name"; m.toast == nil || m.toast.Text != want {
			t.Errorf("toast = %+v, want %q", m.toast, want)
		}
		if v := screen(m); strings.Contains(strings.ReplaceAll(v, " ", ""), "tok-SECRET") {
			t.Errorf("the token is on the screen:\n%s", v)
		}
	}
	// A short paste of a name is taken, a trailing line break and all.
	m.Update(tea.PasteMsg{Content: "team-a\n"})
	if got := strings.TrimSpace(m.prompt.buf); got != "team-a" {
		t.Errorf("a pasted name gave %q, want team-a", m.prompt.buf)
	}
	save := deliver(t, m, press(m, tea.KeyPressMsg{Code: tea.KeyEnter}))
	res := save()
	if ps, ok := res.(pasteSaved); !ok || ps.err != nil || filepath.Base(ps.ref.Path) != "team-a.yaml" {
		t.Errorf("save = %+v, want team-a.yaml", res)
	}
}

// The name is one line of at most 64 characters, and its file name is capped
// too, so no name can make the save fail with "file name too long".
func TestNamePromptValidatesName(t *testing.T) {
	m := pasteBoxModel(t)
	pasteIn(t, m, pasteTwo)
	req, ok := press(m, ctrlKey('s'))().(view.PromptRequest)
	if !ok || req.Validate == nil {
		t.Fatal("no name prompt with a check")
	}
	for name, ok := range map[string]bool{
		"prod":                   true,
		strings.Repeat("é", 64):  true,
		strings.Repeat("é", 65):  false,
		"two\nlines":             false,
		"esc\x1b[2J":             false,
		"   ":                    false,
		strings.Repeat("x", 300): false,
	} {
		if err := req.Validate(name); (err == nil) != ok {
			t.Errorf("Validate(%q) = %v, want ok = %v", name, err, ok)
		}
	}
}

// The f prompt asks for a path, which has no line break: a paste with one is
// refused rather than flattened into the field.
func TestFilePromptRefusesLineBreaks(t *testing.T) {
	m := New(Config{Store: savedStore(t, "")})
	m.width, m.height = 120, 40
	p := view.NewClusterPicker(m.theme, nil, true)
	m.pushPage(p)
	m.Update(view.ClustersLoaded{})
	_, f := m.handleKey(textKey('f'))
	m.Update(f())
	if m.prompt == nil {
		t.Fatal("f did not open the path prompt")
	}
	m.Update(tea.PasteMsg{Content: "apiVersion: v1\nkind: Config\nusers:\n- {name: u, user: {token: tok-SECRET}}\n"})
	if m.prompt.buf != "" || m.toast == nil {
		t.Fatalf("buf = %q, toast = %+v; want the paste refused with a toast", m.prompt.buf, m.toast)
	}
	if strings.Contains(screen(m), "tok-SECRET") {
		t.Errorf("the token is on the screen:\n%s", screen(m))
	}
	m.Update(tea.PasteMsg{Content: "/tmp/dev.yaml\n"})
	if got := strings.TrimSpace(m.prompt.buf); got != "/tmp/dev.yaml" {
		t.Errorf("a pasted path gave %q, want /tmp/dev.yaml", m.prompt.buf)
	}
}

// Reviewing a file some of whose contexts are already recorded keeps the names
// they have; only the unnamed ones take the file's stem.
func TestPasteBoxReviewKeepsNames(t *testing.T) {
	s := savedStore(t, "")
	file, err := s.SavePaste("team", []byte(pasteTwo))
	if err != nil {
		t.Fatal(err)
	}
	named := k8s.ClusterRef{Source: k8s.SourcePasted, Path: s.PastePath(file), Context: "prod"}
	if err := s.Update(clusters.KeyFor(named, ""), func(e *clusters.Entry) { e.Name = "production" }); err != nil {
		t.Fatal(err)
	}
	r, err := clusters.Inspect([]byte(pasteTwo))
	if err != nil {
		t.Fatal(err)
	}
	if ps := recordPaste(s, file, "team", r, "dev", false); ps.err != nil {
		t.Fatal(ps.err)
	}
	entries, _ := s.Entries()
	got := map[string]string{}
	for _, e := range entries {
		got[e.Context] = e.Name
	}
	if got["prod"] != "production" || got["dev"] != "team" {
		t.Errorf("names = %v, want prod kept as production and dev named team", got)
	}
}

// A paste under 1 MiB that is not a kubeconfig says so, with no parse of any
// later text needed.
func TestPasteBoxSaysNotAKubeconfig(t *testing.T) {
	for name, text := range map[string]string{
		"prose":        "remember the milk\n",
		"secret":       "apiVersion: v1\nkind: Secret\nmetadata: {name: s}\ndata: {token: c2VjcmV0}\n",
		"half a meg":   strings.Repeat("a: b\n", 100_000),
		"yaml no kind": "clusters: []\nusers: []\n",
	} {
		t.Run(name, func(t *testing.T) {
			m := pasteBoxModel(t)
			pasteIn(t, m, text)
			if !errors.Is(m.paste.err, clusters.ErrNotKubeconfig) {
				t.Fatalf("err = %v, want %v", m.paste.err, clusters.ErrNotKubeconfig)
			}
			if v := boxView(m); !strings.Contains(v, "✗ this doesn't look like a kubeconfig") {
				t.Errorf("the box does not say so:\n%s", v)
			}
			if cmd := press(m, ctrlKey('s')); cmd != nil || m.prompt != nil {
				t.Error("ctrl+s acted on text that is not a kubeconfig")
			}
		})
	}
}
