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

func TestPickerEnterOnConnectedRowToasts(t *testing.T) {
	a := pickRow("a", k8s.SourceKubeconfig)
	cur := a.Ref
	p := loadedPicker(t, &cur, a)
	_, m := press(p, "enter")
	if tm, ok := m.(msg.Toast); !ok || !strings.Contains(tm.Text, "already on a") {
		t.Fatalf("enter = %#v, want an already-on toast", m)
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
