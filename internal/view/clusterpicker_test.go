package view

import (
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func pickRow(label string, src k8s.Source) clusters.Row {
	r := clusters.Row{
		Ref:    k8s.ClusterRef{Source: src, Context: label},
		Label:  label,
		Source: src,
		Server: "https://" + label + ".example:6443",
	}
	switch src {
	case k8s.SourcePasted:
		r.Ref.Path = "/data/clusters/" + label + ".yaml"
		r.Key = clusters.Key{Source: src, File: label + ".yaml", Context: label}
	case k8s.SourceFile:
		r.Ref.Path = "/home/me/" + label + ".yaml"
		r.Key = clusters.Key{Source: src, Path: r.Ref.Path, Context: label}
	}
	return r
}

func loadedPicker(t *testing.T, current *k8s.ClusterRef, rows ...clusters.Row) *clusterPickerPage {
	t.Helper()
	p := NewClusterPicker(style.Default(), current, true)
	np, _ := p.Update(ClustersLoaded{Rows: rows})
	return np.(*clusterPickerPage)
}

func press(p *clusterPickerPage, k string) (*clusterPickerPage, tea.Msg) {
	var code rune
	var text string
	switch k {
	case "enter":
		code = tea.KeyEnter
	case "ctrl+d":
		np, cmd := p.Update(tea.KeyPressMsg{Code: 'd', Mod: tea.ModCtrl})
		return np.(*clusterPickerPage), runCmd(cmd)
	case "j":
		code, text = 'j', "j"
	default:
		code, text = rune(k[0]), k
	}
	np, cmd := p.Update(tea.KeyPressMsg{Code: code, Text: text})
	return np.(*clusterPickerPage), runCmd(cmd)
}

func runCmd(c tea.Cmd) tea.Msg {
	if c == nil {
		return nil
	}
	return c()
}

func TestPickerEnterEmitsRef(t *testing.T) {
	a, b := pickRow("a", k8s.SourceKubeconfig), pickRow("b", k8s.SourcePasted)
	cur := a.Ref
	p := loadedPicker(t, &cur, a, b)
	if p.cursor != 0 {
		t.Fatalf("cursor = %d, want the current row", p.cursor)
	}
	p, _ = press(p, "j")
	_, m := press(p, "enter")
	got, ok := m.(SwitchContextRequest)
	if !ok || got.Ref != b.Ref {
		t.Fatalf("enter = %#v, want SwitchContextRequest{%v}", m, b.Ref)
	}
}

// PickMsg is the one rule for enter and for ":ctx <name>"'s single match.
func TestPickMsg(t *testing.T) {
	plain, cur := pickRow("a", k8s.SourceKubeconfig), pickRow("b", k8s.SourceFile)
	missing := pickRow("gone", k8s.SourceFile)
	missing.Missing = true
	fresh := pickRow("new", k8s.SourcePasted)
	fresh.NeedsReview = true
	// A missing file is reported first, even on the connected cluster's ref.
	missingCur := cur
	missingCur.Missing = true

	for _, tc := range []struct {
		name    string
		row     clusters.Row
		current *k8s.ClusterRef
		want    tea.Msg
	}{
		{"switch", plain, &cur.Ref, SwitchContextRequest{Ref: plain.Ref}},
		{"switch with no session", cur, nil, SwitchContextRequest{Ref: cur.Ref}},
		{"connected", cur, &cur.Ref, StayOnCluster{Label: "b"}},
		{"missing", missing, nil, msg.Toast{Text: "gone: file missing", Level: msg.LevelWarn}},
		{"missing connected", missingCur, &cur.Ref, msg.Toast{Text: "b: file missing", Level: msg.LevelWarn}},
		{"needs review", fresh, nil, ReviewCluster{Row: fresh}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			got := PickMsg(tc.row, tc.current)
			if r, ok := tc.want.(ReviewCluster); ok {
				if g, ok := got.(ReviewCluster); !ok || g.Row.Ref != r.Row.Ref {
					t.Fatalf("PickMsg = %#v, want %#v", got, tc.want)
				}
				return
			}
			if got != tc.want {
				t.Fatalf("PickMsg = %#v, want %#v", got, tc.want)
			}
		})
	}
}

// ":ctx <name>" with several matches opens the picker on the first of them.
func TestPickerLoadSelectsRow(t *testing.T) {
	a, b, c := pickRow("a", k8s.SourceKubeconfig), pickRow("b", k8s.SourceKubeconfig), pickRow("c", k8s.SourceFile)
	cur := b.Ref
	p := NewClusterPicker(style.Default(), &cur, false)
	np, _ := p.Update(ClustersLoaded{Rows: []clusters.Row{a, b, c}, Select: &c.Ref})
	if p = np.(*clusterPickerPage); p.cursor != 2 {
		t.Fatalf("cursor = %d, want 2, the selected row", p.cursor)
	}
	np, _ = p.Update(ClustersLoaded{Rows: []clusters.Row{a, b, c}})
	if p = np.(*clusterPickerPage); p.cursor != 1 {
		t.Fatalf("cursor = %d after a plain reload, want 1, the connected row", p.cursor)
	}
}

func TestPickerEnterOnConnectedRowStays(t *testing.T) {
	a := pickRow("a", k8s.SourceKubeconfig)
	cur := a.Ref
	p := loadedPicker(t, &cur, a)
	_, m := press(p, "enter")
	if got, ok := m.(StayOnCluster); !ok || got.Label != "a" {
		t.Fatalf("enter = %#v, want StayOnCluster{a}", m)
	}
}

func TestPickerEnterOnMissingRowToasts(t *testing.T) {
	r := pickRow("gone", k8s.SourceFile)
	r.Missing = true
	p := loadedPicker(t, nil, r)
	_, m := press(p, "enter")
	if tm, ok := m.(msg.Toast); !ok || tm.Text != "gone: file missing" {
		t.Fatalf("enter = %#v, want the file-missing toast", m)
	}
	if !strings.Contains(ansi.Strip(p.View(120, 10)), "(file missing)") {
		t.Fatal("a missing row should say (file missing)")
	}
}

func TestPickerEnterOnUnreviewedRowAsksReview(t *testing.T) {
	r := pickRow("new", k8s.SourcePasted)
	r.NeedsReview = true
	p := loadedPicker(t, nil, r)
	_, m := press(p, "enter")
	if got, ok := m.(ReviewCluster); !ok || got.Row.Ref != r.Ref {
		t.Fatalf("enter = %#v, want ReviewCluster", m)
	}
	if !strings.Contains(ansi.Strip(p.View(120, 10)), "needs review") {
		t.Fatal("an unreviewed row should say needs review in SERVER")
	}
}

func TestPickerRemoveConfirmPerSource(t *testing.T) {
	p1, p2 := pickRow("a", k8s.SourcePasted), pickRow("b", k8s.SourcePasted)
	p1.Key.File, p2.Key.File = "shared.yaml", "shared.yaml"
	f := pickRow("f", k8s.SourceFile)
	k := pickRow("k", k8s.SourceKubeconfig)
	ic := clusters.Row{Source: k8s.SourceInCluster, Label: "in-cluster", Ref: k8s.ClusterRef{Source: k8s.SourceInCluster}}
	p := loadedPicker(t, nil, p1, p2, f, k, ic)

	confirm := func(p *clusterPickerPage) ConfirmRequest {
		t.Helper()
		_, m := press(p, "ctrl+d")
		c, ok := m.(ConfirmRequest)
		if !ok || !c.Danger {
			t.Fatalf("ctrl+d = %#v, want a danger ConfirmRequest", m)
		}
		return c
	}

	c := confirm(p)
	if c.Prompt != "Delete shared.yaml and its contexts: a, b?" {
		t.Fatalf("pasted prompt = %q", c.Prompt)
	}
	if fg, ok := c.Action().(ForgetCluster); !ok || fg.Row.Ref != p1.Ref {
		t.Fatalf("pasted action = %#v", c.Action())
	}

	p.cursor = 2
	c = confirm(p)
	if c.Prompt != "Forget "+f.Ref.Path+"? The file is not touched." {
		t.Fatalf("file prompt = %q", c.Prompt)
	}
	if fg, ok := c.Action().(ForgetCluster); !ok || fg.Row.Ref != f.Ref {
		t.Fatalf("file action = %#v", c.Action())
	}

	for _, i := range []int{3, 4} {
		p.cursor = i
		_, m := press(p, "ctrl+d")
		tm, ok := m.(msg.Toast)
		if !ok || !strings.Contains(tm.Text, "comes from your kubeconfig; kubetui doesn't edit it") {
			t.Fatalf("row %d ctrl+d = %#v, want the kubeconfig toast", i, m)
		}
	}
}

func TestPickerViewFitsWidth(t *testing.T) {
	cjk := strings.Repeat("集群", 30) // 60 runes, 120 cells
	rows := []clusters.Row{
		pickRow(cjk, k8s.SourcePasted),
		pickRow("prod", k8s.SourceKubeconfig),
		pickRow("gone", k8s.SourceFile),
	}
	rows[1].LastUsed = time.Now().Add(-2 * time.Hour)
	rows[2].Missing = true
	cur := rows[1].Ref
	p := loadedPicker(t, &cur, rows...)
	conn := rows[0].Ref
	p.SetConnecting(&conn)
	p.Update(ClusterConnectFailed{Ref: rows[2].Ref, Err: strings.Repeat("boom ", 40)})
	for _, w := range []int{40, 120} {
		for i, line := range strings.Split(p.View(w, 20), "\n") {
			if got := ansi.StringWidth(line); got > w {
				t.Errorf("width %d line %d is %d cells: %q", w, i, got, ansi.Strip(line))
			}
		}
	}
}

func TestPickerShowsConnectingAndErrors(t *testing.T) {
	a, b := pickRow("a", k8s.SourceKubeconfig), pickRow("b", k8s.SourcePasted)
	p := loadedPicker(t, nil, a, b)
	p.SetConnecting(&b.Ref)
	out := ansi.Strip(p.View(120, 10))
	if !strings.Contains(out, "connecting…") {
		t.Fatalf("no connecting row:\n%s", out)
	}
	p.SetConnecting(nil)
	np, _ := p.Update(ClusterConnectFailed{Ref: b.Ref, Err: "x509: bad cert"})
	p = np.(*clusterPickerPage)
	out = ansi.Strip(p.View(120, 10))
	if strings.Contains(out, "connecting…") || !strings.Contains(out, "x509: bad cert") {
		t.Fatalf("want the error and no connecting row:\n%s", out)
	}
}

func TestPickerPasteOffWithoutStore(t *testing.T) {
	p := NewClusterPicker(style.Default(), nil, true)
	np, _ := p.Update(ClustersLoaded{Rows: []clusters.Row{pickRow("a", k8s.SourceKubeconfig)}, NoStore: "no config dir"})
	p = np.(*clusterPickerPage)
	for _, k := range []string{"p", "f"} {
		_, m := press(p, k)
		if tm, ok := m.(msg.Toast); !ok || tm.Text != "no config dir" {
			t.Fatalf("%s = %#v, want the NoStore toast", k, m)
		}
	}
	p.Update(ClustersLoaded{Rows: nil})
	_, m := press(p, "p")
	if _, ok := m.(OpenPasteBox); !ok {
		t.Fatalf("p = %#v, want OpenPasteBox", m)
	}
	_, m = press(p, "f")
	pr, ok := m.(PromptRequest)
	if !ok || pr.Title != "Add kubeconfig file" {
		t.Fatalf("f = %#v, want PromptRequest", m)
	}
	if got := pr.Action(`  "/tmp/x.yaml" `); got != (AddKubeconfigFile{Path: "/tmp/x.yaml"}) {
		t.Fatalf("prompt action = %#v", got)
	}
}

func TestPickerAgoText(t *testing.T) {
	now := time.Date(2026, 11, 26, 12, 0, 0, 0, time.UTC)
	for d, want := range map[time.Duration]string{
		2 * time.Hour: "2h ago", 30 * time.Hour: "yesterday", 5 * 24 * time.Hour: "5 days ago", 0: "—",
	} {
		var at time.Time
		if d > 0 {
			at = now.Add(-d)
		}
		if got := lastUsedText(at, now); got != want {
			t.Errorf("lastUsedText(-%v) = %q, want %q", d, got, want)
		}
	}
}

// Labels, servers, errors, toasts and the remove confirm come from kubeconfigs
// and are drawn without their control characters: a CR in a context name moves
// the cursor back over its own row, a line feed adds a fake one.
func TestPickerSanitizesKubeconfigText(t *testing.T) {
	evil := pickRow("x\rprod", k8s.SourcePasted)
	evil.Ref.Context = "x\rprod"
	evil.Key.File = "a\x1b]52;c;eA==\a.yaml"
	evil.Server = "https://\x1b[2Jsrv\n.example"
	cfg := pickRow("k\a\rk", k8s.SourceKubeconfig)
	p := loadedPicker(t, nil, evil, cfg)
	p.Update(ClusterConnectFailed{Ref: evil.Ref, Err: "boom\a\x1b]0;title\a\rdone"})
	out := p.View(160, 20)
	for _, bad := range []string{"\r", "\a", "\x1b]", "\x1b[2J"} {
		if strings.Contains(out, bad) {
			t.Errorf("the picker draws %q:\n%q", bad, out)
		}
	}
	if plain := ansi.Strip(out); !strings.Contains(plain, "xprod") || !strings.Contains(plain, "https://[2Jsrv.example") || !strings.Contains(plain, "boom]0;titledone") {
		t.Errorf("the picker does not show the text without its control characters:\n%s", plain)
	}

	clean := func(what, s string) {
		t.Helper()
		if strings.ContainsAny(s, "\r\a\x1b\n") {
			t.Errorf("%s holds a control character: %q", what, s)
		}
	}
	_, m := press(p, "ctrl+d")
	c, ok := m.(ConfirmRequest)
	if !ok {
		t.Fatalf("ctrl+d = %#v, want the remove confirm", m)
	}
	clean("remove confirm", c.Prompt)
	cur := cfg.Ref
	missing := evil
	missing.Missing = true
	if got, ok := PickMsg(cfg, &cur).(StayOnCluster); !ok {
		t.Fatalf("PickMsg = %#v, want StayOnCluster", got)
	} else {
		clean("stay label", got.Label)
	}
	if got, ok := PickMsg(missing, nil).(msg.Toast); !ok {
		t.Fatalf("PickMsg = %#v, want a toast", got)
	} else {
		clean("toast", got.Text)
	}
	p.cursor = 1
	_, m = press(p, "ctrl+d")
	if tm, ok := m.(msg.Toast); !ok {
		t.Fatalf("ctrl+d on a kubeconfig row = %#v, want a toast", m)
	} else {
		clean("kubeconfig toast", tm.Text)
	}
}

// The cursor starts on the connected row; else, with any row used, on the most
// recently used one; else, on a first launch, on the default kubeconfig's
// current-context, which is where kubetui with no flags used to connect.
func TestPickerCursorStart(t *testing.T) {
	a, z := pickRow("a-prod", k8s.SourceKubeconfig), pickRow("zz-dev", k8s.SourceKubeconfig)
	pasted := pickRow("zz-dev", k8s.SourcePasted) // the same name in a paste is not it
	pasted.Label = "b-paste / zz-dev"
	load := func(cur *k8s.ClusterRef, current string, rows ...clusters.Row) clusters.Row {
		t.Helper()
		p := NewClusterPicker(style.Default(), cur, cur == nil)
		np, _ := p.Update(ClustersLoaded{Rows: rows, Current: current})
		r, _ := np.(*clusterPickerPage).selected()
		return r
	}
	if got := load(nil, "zz-dev", a, pasted, z); got.Ref != z.Ref {
		t.Errorf("first launch: cursor on %+v, want the current-context zz-dev", got.Ref)
	}
	if got := load(nil, "", a, z); got.Ref != a.Ref {
		t.Errorf("no current-context: cursor on %+v, want the first row", got.Ref)
	}
	used := a
	used.LastUsed = time.Now().Add(-time.Hour)
	if got := load(nil, "zz-dev", z, used); got.Ref != used.Ref {
		t.Errorf("a row used: cursor on %+v, want the most recently used a-prod", got.Ref)
	}
	cur := a.Ref
	if got := load(&cur, "zz-dev", a, z); got.Ref != a.Ref {
		t.Errorf("from :ctx: cursor on %+v, want the connected a-prod", got.Ref)
	}
}

// Banner lines stand above the table: why saved clusters are off, what could
// not be read, and a connect error for a cluster with no row, which the next
// connect clears.
func TestPickerBanner(t *testing.T) {
	a := pickRow("a", k8s.SourceKubeconfig)
	p := NewClusterPicker(style.Default(), nil, true)
	p.Update(ClustersLoaded{Rows: []clusters.Row{a}, Banner: []BannerLine{
		{Text: "saved clusters are off: no home"},
		{Text: "could not read state.yaml\x1b]0;x\a", Warn: true},
	}})
	out := ansi.Strip(p.View(120, 20))
	head := strings.Index(out, "NAME")
	for _, want := range []string{"saved clusters are off: no home", "could not read state.yaml]0;x"} {
		if i := strings.Index(out, want); i < 0 || i > head {
			t.Errorf("no %q above the table:\n%s", want, out)
		}
	}

	typo := k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "typo"}
	p.Update(ClusterConnectFailed{Ref: typo, Err: `load kubeconfig: context "typo" does not exist`})
	out = ansi.Strip(p.View(120, 20))
	want := `✗ connect typo: load kubeconfig: context "typo" does not exist`
	if i := strings.Index(out, want); i < 0 || i > strings.Index(out, "NAME") {
		t.Errorf("the error for a cluster with no row is not above the table:\n%s", out)
	}
	p.SetConnecting(&a.Ref)
	if out = ansi.Strip(p.View(120, 20)); strings.Contains(out, "typo") {
		t.Errorf("the next connect did not clear the error:\n%s", out)
	}
}

// With saved clusters off, the picker says so without a key pressed, and its
// empty state does not offer the paste that is off.
func TestPickerSaysWhySavedClustersAreOff(t *testing.T) {
	p := NewClusterPicker(style.Default(), nil, true)
	p.Update(ClustersLoaded{NoStore: "saved clusters are off: no home", Banner: []BannerLine{{Text: "saved clusters are off: no home"}}})
	out := ansi.Strip(p.View(120, 20))
	if !strings.Contains(out, "saved clusters are off: no home") {
		t.Errorf("no word on why:\n%s", out)
	}
	if strings.Contains(out, "press p") {
		t.Errorf("the empty state offers paste, which is off:\n%s", out)
	}
}

// At launch esc has nowhere to go back to, so the keys (and help) leave it out.
func TestPickerKeysAtLaunch(t *testing.T) {
	for _, atLaunch := range []bool{true, false} {
		hasEsc := false
		for _, k := range NewClusterPicker(style.Default(), nil, atLaunch).Keys() {
			if k.Help().Key == "esc" {
				hasEsc = true
			}
		}
		if hasEsc == atLaunch {
			t.Errorf("at launch = %v: esc listed = %v", atLaunch, hasEsc)
		}
	}
}
