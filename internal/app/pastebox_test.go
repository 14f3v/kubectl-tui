package app

import (
	"errors"
	"fmt"
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
	m := New(Config{Sink: func(tea.Msg) {}, StartKind: "portforwards", Store: s})
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
	m := New(Config{Sink: func(tea.Msg) {}, StartKind: "portforwards", Store: s})
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
