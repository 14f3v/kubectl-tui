package clusters

import (
	"fmt"
	"os"
	"path/filepath"
	"testing"
	"time"

	"github.com/14f3v/kubectl-tui/internal/k8s"
)

func ctx(name, server string) k8s.ContextInfo { return k8s.ContextInfo{Name: name, Server: server} }

func labels(rows []Row) []string {
	var out []string
	for _, r := range rows {
		out = append(out, r.Label)
	}
	return out
}

func eq(a, b []string) bool {
	if len(a) != len(b) {
		return false
	}
	for i := range a {
		if a[i] != b[i] {
			return false
		}
	}
	return true
}

func TestBuildSortsByLastUsed(t *testing.T) {
	now := time.Now()
	rows := Build(Inputs{
		Kubeconfig: []k8s.ContextInfo{ctx("zeta", "https://z"), ctx("alpha", "https://a"), ctx("old", "https://o"), ctx("new", "https://n")},
		Entries: []Entry{
			{Source: k8s.SourceKubeconfig, Context: "old", Server: "https://o", LastUsed: now.Add(-time.Hour)},
			{Source: k8s.SourceKubeconfig, Context: "new", Server: "https://n", LastUsed: now},
		},
	})
	if got, want := labels(rows), []string{"new", "old", "alpha", "zeta"}; !eq(got, want) {
		t.Fatalf("order = %v, want %v", got, want)
	}
}

func TestBuildLabelsMultiContextPaste(t *testing.T) {
	rows := Build(Inputs{
		PastedDir: "/p",
		Pasted:    map[string][]k8s.ContextInfo{"dev-tokyo.yaml": {ctx("admin", "https://x"), ctx("viewer", "https://x")}},
		Entries: []Entry{
			{Source: k8s.SourcePasted, File: "dev-tokyo.yaml", Name: "dev-tokyo", Context: "admin"},
			{Source: k8s.SourcePasted, File: "dev-tokyo.yaml", Name: "dev-tokyo", Context: "viewer"},
		},
	})
	if got, want := labels(rows), []string{"dev-tokyo / admin", "dev-tokyo / viewer"}; !eq(got, want) {
		t.Fatalf("labels = %v, want %v", got, want)
	}
	if rows[0].Ref.Path != filepath.Join("/p", "dev-tokyo.yaml") || rows[0].NeedsReview {
		t.Fatalf("row = %+v", rows[0])
	}
}

func TestBuildMarksMissingFile(t *testing.T) {
	rows := Build(Inputs{
		Files:   map[string][]k8s.ContextInfo{"/gone.yaml": nil},
		Entries: []Entry{{Source: k8s.SourceFile, Path: "/gone.yaml", Context: "prod"}},
	})
	if len(rows) != 1 || !rows[0].Missing || rows[0].Label != "prod" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestBuildMarksUnreviewedPaste(t *testing.T) {
	rows := Build(Inputs{
		PastedDir: "/p",
		Pasted:    map[string][]k8s.ContextInfo{"new.yaml": {ctx("a", "https://x")}},
	})
	if len(rows) != 1 || !rows[0].NeedsReview || rows[0].Label != "new" {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestBuildDropsRecordWithoutFile(t *testing.T) {
	rows := Build(Inputs{
		PastedDir: "/p",
		Pasted:    map[string][]k8s.ContextInfo{},
		Entries:   []Entry{{Source: k8s.SourcePasted, File: "gone.yaml", Name: "gone", Context: "a"}},
	})
	if len(rows) != 0 {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestBuildKubeconfigStateByContextAndServer(t *testing.T) {
	now := time.Now()
	rows := Build(Inputs{
		Kubeconfig: []k8s.ContextInfo{ctx("default", "https://one"), ctx("default", "https://two")},
		Entries:    []Entry{{Source: k8s.SourceKubeconfig, Context: "default", Server: "https://two", LastUsed: now}},
	})
	if len(rows) != 2 || rows[0].Server != "https://two" || rows[0].LastUsed.IsZero() || !rows[1].LastUsed.IsZero() {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestBuildInClusterOnlyWithoutKubeconfig(t *testing.T) {
	if rows := Build(Inputs{InCluster: true}); len(rows) != 1 || rows[0].Source != k8s.SourceInCluster || rows[0].Label != "in-cluster" {
		t.Fatalf("rows = %+v", rows)
	}
	if rows := Build(Inputs{InCluster: true, Kubeconfig: []k8s.ContextInfo{ctx("a", "s")}}); len(rows) != 1 || rows[0].Source != k8s.SourceKubeconfig {
		t.Fatalf("rows = %+v", rows)
	}
}

func TestMatch(t *testing.T) {
	rows := []Row{
		{Label: "dev / admin", Ref: k8s.ClusterRef{Context: "admin"}},
		{Label: "prod", Ref: k8s.ClusterRef{Context: "prod"}},
		{Label: "other", Ref: k8s.ClusterRef{Context: "admin"}},
	}
	if got := Match(rows, "prod"); len(got) != 1 {
		t.Fatalf("one: %+v", got)
	}
	if got := Match(rows, "admin"); len(got) != 2 {
		t.Fatalf("several: %+v", got)
	}
	if got := Match(rows, "dev / admin"); len(got) != 1 {
		t.Fatalf("label: %+v", got)
	}
	if got := Match(rows, "nope"); len(got) != 0 {
		t.Fatalf("none: %+v", got)
	}
}

const oneCtx = `apiVersion: v1
kind: Config
current-context: %s
clusters:
- name: c
  cluster: {server: "https://%s"}
contexts:
- name: %s
  context: {cluster: c, user: u}
users:
- name: u
  user: {token: x}
`

func writeCfg(t *testing.T, path, name, server string) {
	t.Helper()
	body := []byte(fmt.Sprintf(oneCtx, name, server, name))
	if err := os.WriteFile(path, body, 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestCollectWithTempDirs(t *testing.T) {
	tmp := t.TempDir()
	kc := filepath.Join(tmp, "kc.yaml")
	writeCfg(t, kc, "kube-ctx", "k.example")
	t.Setenv("KUBECONFIG", kc)

	s, err := Open(filepath.Join(tmp, "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	if _, err := s.SavePaste("dev", []byte(fmt.Sprintf(oneCtx, "dev-ctx", "d.example", "dev-ctx"))); err != nil {
		t.Fatal(err)
	}
	remembered := filepath.Join(tmp, "remembered.yaml")
	writeCfg(t, remembered, "rem-ctx", "r.example")
	gone := filepath.Join(tmp, "gone.yaml")
	for _, e := range []Entry{
		{Source: k8s.SourceFile, Path: remembered, Context: "rem-ctx", LastUsed: time.Now()},
		{Source: k8s.SourceFile, Path: gone, Context: "g"},
		{Source: k8s.SourcePasted, File: "vanished.yaml", Name: "vanished", Context: "v"},
	} {
		e := e
		if err := s.Update(e.Key(), func(x *Entry) { *x = e }); err != nil {
			t.Fatal(err)
		}
	}

	rows, err := Collect(s)
	if err != nil {
		t.Fatal(err)
	}
	by := map[string]Row{}
	for _, r := range rows {
		by[r.Label] = r
	}
	if len(rows) != 4 {
		t.Fatalf("rows = %v", labels(rows))
	}
	if !by["dev"].NeedsReview || by["dev"].Source != k8s.SourcePasted {
		t.Fatalf("dev = %+v", by["dev"])
	}
	if by["rem-ctx"].Source != k8s.SourceFile || by["rem-ctx"].Missing {
		t.Fatalf("rem = %+v", by["rem-ctx"])
	}
	if !by["g"].Missing {
		t.Fatalf("g = %+v", by["g"])
	}
	if by["kube-ctx"].Source != k8s.SourceKubeconfig || by["kube-ctx"].Server != "https://k.example" {
		t.Fatalf("kube = %+v", by["kube-ctx"])
	}
	left, _ := s.Entries()
	for _, e := range left {
		if e.File == "vanished.yaml" {
			t.Fatalf("dropped record still in state")
		}
	}
}

func TestCollectWithoutStore(t *testing.T) {
	kc := filepath.Join(t.TempDir(), "kc.yaml")
	writeCfg(t, kc, "only", "o.example")
	t.Setenv("KUBECONFIG", kc)
	rows, err := Collect(nil)
	if err != nil || len(rows) != 1 || rows[0].Label != "only" {
		t.Fatalf("rows = %+v, err = %v", rows, err)
	}
}
