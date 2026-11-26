package clusters

import (
	"fmt"
	"os"
	"path/filepath"
	"runtime"
	"strings"
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
	// A typed name holds no control characters: it matches the name as drawn.
	raw := []Row{{Label: "x\rprod", Ref: k8s.ClusterRef{Context: "x\rprod"}}}
	if got := Match(raw, "xprod"); len(got) != 1 {
		t.Fatalf("drawn name: %+v", got)
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

	l := Collect(s)
	if len(l.Warnings) != 0 {
		t.Fatalf("warnings = %v", l.Warnings)
	}
	if l.Current != "kube-ctx" {
		t.Fatalf("current = %q, want the kubeconfig's current-context", l.Current)
	}
	rows := l.Rows
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
	l := Collect(nil)
	if len(l.Warnings) != 0 || len(l.Rows) != 1 || l.Rows[0].Label != "only" || l.Current != "only" {
		t.Fatalf("listing = %+v", l)
	}
}

// unreadable makes path unreadable for the test, and readable again after it.
// Permission bits are not enforced on Windows or for root.
func unreadable(t *testing.T, path string) {
	t.Helper()
	if runtime.GOOS == "windows" || os.Geteuid() == 0 {
		t.Skip("permission bits do not stop reads here")
	}
	info, err := os.Stat(path)
	if err != nil {
		t.Fatal(err)
	}
	if err := os.Chmod(path, 0o000); err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = os.Chmod(path, info.Mode().Perm()) })
}

// collectFixture is a store with one pasted file, recorded, and a default
// kubeconfig with one context.
func collectFixture(t *testing.T) *Store {
	t.Helper()
	tmp := t.TempDir()
	kc := filepath.Join(tmp, "kc.yaml")
	writeCfg(t, kc, "kube-ctx", "k.example")
	t.Setenv("KUBECONFIG", kc)
	s, err := Open(filepath.Join(tmp, "cfg"))
	if err != nil {
		t.Fatal(err)
	}
	file, err := s.SavePaste("dev", []byte(fmt.Sprintf(oneCtx, "dev-ctx", "d.example", "dev-ctx")))
	if err != nil {
		t.Fatal(err)
	}
	k := makeKey(k8s.SourcePasted, file, "", "dev-ctx", "")
	if err := s.Update(k, func(e *Entry) { e.Name = "dev" }); err != nil {
		t.Fatal(err)
	}
	return s
}

// An unreadable state.yaml is not an error: everything else is listed, the
// pasted file needs review again (nothing is approved by losing the state),
// and a warning says why. Update still refuses to write over it.
func TestCollectUnreadableState(t *testing.T) {
	s := collectFixture(t)
	unreadable(t, s.statePath())

	l := Collect(s)
	by := map[k8s.Source]Row{}
	for _, r := range l.Rows {
		by[r.Source] = r
	}
	if len(l.Rows) != 2 || by[k8s.SourceKubeconfig].Label != "kube-ctx" || !by[k8s.SourcePasted].NeedsReview {
		t.Fatalf("rows = %+v, want the kubeconfig's and the paste needing review", l.Rows)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "state.yaml") {
		t.Fatalf("warnings = %v, want one about state.yaml", l.Warnings)
	}
	if err := s.Update(Key{Source: k8s.SourceKubeconfig, Context: "x"}, func(*Entry) {}); err == nil {
		t.Fatal("Update wrote over a state.yaml it could not read")
	}
}

// An unreadable clusters/ lists no pasted rows and says so, and does not drop
// the records of the files it could not see.
func TestCollectUnreadableClustersDir(t *testing.T) {
	s := collectFixture(t)
	unreadable(t, s.ClustersDir())

	l := Collect(s)
	if len(l.Rows) != 1 || l.Rows[0].Source != k8s.SourceKubeconfig {
		t.Fatalf("rows = %+v, want only the kubeconfig's", l.Rows)
	}
	if len(l.Warnings) != 1 || !strings.Contains(l.Warnings[0], "pasted kubeconfigs") {
		t.Fatalf("warnings = %v, want one about the pasted kubeconfigs", l.Warnings)
	}
	if entries, err := s.Entries(); err != nil || len(entries) != 1 {
		t.Fatalf("entries = %+v (%v): the paste's record was dropped", entries, err)
	}
}

// A default kubeconfig that does not load lists no rows of its own and says
// why, through SafeLoadError: client-go's duplicate-name error prints tokens.
func TestCollectBadKubeconfig(t *testing.T) {
	kc := filepath.Join(t.TempDir(), "kc.yaml")
	dup := "apiVersion: v1\nkind: Config\nusers:\n- {name: u, user: {token: tok-SECRET-1}}\n- {name: u, user: {token: tok-SECRET-2}}\n"
	if err := os.WriteFile(kc, []byte(dup), 0o600); err != nil {
		t.Fatal(err)
	}
	t.Setenv("KUBECONFIG", kc)
	t.Setenv("KUBERNETES_SERVICE_HOST", "")
	l := Collect(nil)
	if len(l.Rows) != 0 || len(l.Warnings) != 1 {
		t.Fatalf("listing = %+v, want no rows and one warning", l)
	}
	if w := l.Warnings[0]; !strings.Contains(w, "kubeconfig") || strings.Contains(w, "tok-SECRET") {
		t.Fatalf("warning = %q, want one about the kubeconfig without its tokens", w)
	}
}
