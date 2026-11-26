package clusters

import (
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/k8s"
)

func readState(t *testing.T, s *Store) string {
	t.Helper()
	b, err := os.ReadFile(filepath.Join(s.Dir(), "state.yaml"))
	if err != nil {
		t.Fatal(err)
	}
	return string(b)
}

func TestLoadStateMissingIsEmpty(t *testing.T) {
	s := openTemp(t)
	st, err := s.LoadState()
	if err != nil || len(st.Clusters) != 0 {
		t.Fatalf("got %+v, %v", st, err)
	}
}

func TestLoadStateMalformedIsSetAside(t *testing.T) {
	s := openTemp(t)
	p := filepath.Join(s.Dir(), "state.yaml")
	if err := os.WriteFile(p, []byte("{{{"), 0o600); err != nil {
		t.Fatal(err)
	}
	st, err := s.LoadState()
	if err != nil || len(st.Clusters) != 0 {
		t.Fatalf("got %+v, %v", st, err)
	}
	bad, err := os.ReadFile(p + ".bad")
	if err != nil || string(bad) != "{{{" {
		t.Fatalf("state.yaml.bad = %q, %v", bad, err)
	}
	if _, err := os.Stat(p); !os.IsNotExist(err) {
		t.Fatalf("state.yaml should be gone, stat err = %v", err)
	}
}

func TestUpdateCreatesAndModifies(t *testing.T) {
	s := openTemp(t)
	k := Key{Source: k8s.SourceFile, Path: "/a", Context: "c"}
	if err := s.Update(k, func(e *Entry) { e.Namespace = "payments" }); err != nil {
		t.Fatal(err)
	}
	if err := s.Update(k, func(e *Entry) { e.Kind = "pods" }); err != nil {
		t.Fatal(err)
	}
	es, err := s.Entries()
	if err != nil || len(es) != 1 {
		t.Fatalf("got %+v, %v", es, err)
	}
	e := es[0]
	if e.Source != k8s.SourceFile || e.Path != "/a" || e.Context != "c" || e.Namespace != "payments" || e.Kind != "pods" {
		t.Fatalf("entry = %+v", e)
	}
	if !strings.Contains(readState(t, s), "version: 1") {
		t.Fatalf("no version in %q", readState(t, s))
	}
}

func TestUpdateKeepsOtherWritersEntries(t *testing.T) {
	a := openTemp(t)
	b, err := Open(a.Dir())
	if err != nil {
		t.Fatal(err)
	}
	ka := Key{Source: k8s.SourceFile, Path: "/a", Context: "c"}
	kb := Key{Source: k8s.SourcePasted, File: "x.yaml", Context: "c"}
	if err := a.Update(ka, func(e *Entry) { e.Namespace = "one" }); err != nil {
		t.Fatal(err)
	}
	if err := b.Update(kb, func(e *Entry) { e.Namespace = "two" }); err != nil {
		t.Fatal(err)
	}
	if err := a.Update(ka, func(e *Entry) { e.Kind = "pods" }); err != nil {
		t.Fatal(err)
	}
	es, _ := a.Entries()
	if len(es) != 2 {
		t.Fatalf("got %+v", es)
	}
}

func TestKubeconfigKeyIncludesServer(t *testing.T) {
	s := openTemp(t)
	ka := Key{Source: k8s.SourceKubeconfig, Context: "default", Server: "https://a"}
	kb := Key{Source: k8s.SourceKubeconfig, Context: "default", Server: "https://b"}
	_ = s.Update(ka, func(e *Entry) { e.Namespace = "a" })
	_ = s.Update(kb, func(e *Entry) { e.Namespace = "b" })
	es, _ := s.Entries()
	if len(es) != 2 || es[0].Key() != ka || es[1].Key() != kb {
		t.Fatalf("got %+v", es)
	}
}

func TestKeyFor(t *testing.T) {
	s := openTemp(t)
	pasted := k8s.ClusterRef{Source: k8s.SourcePasted, Path: s.PastePath("dev.yaml"), Context: "admin"}
	file := k8s.ClusterRef{Source: k8s.SourceFile, Path: "/w/ci.kubeconfig", Context: "ci"}
	kc := k8s.ClusterRef{Source: k8s.SourceKubeconfig, Context: "default"}
	ic := k8s.ClusterRef{Source: k8s.SourceInCluster}
	cases := []struct {
		ref   k8s.ClusterRef
		entry Entry
	}{
		{pasted, Entry{Source: k8s.SourcePasted, File: "dev.yaml", Name: "dev", Context: "admin", Server: "ignored", Path: "ignored"}},
		{file, Entry{Source: k8s.SourceFile, Path: "/w/ci.kubeconfig", Context: "ci", File: "ignored", Server: "ignored"}},
		{kc, Entry{Source: k8s.SourceKubeconfig, Context: "default", Server: "https://a", Path: "ignored"}},
		{ic, Entry{Source: k8s.SourceInCluster}},
	}
	for _, c := range cases {
		if got, want := KeyFor(c.ref, "https://a"), c.entry.Key(); got != want {
			t.Errorf("%s: KeyFor = %+v, Entry.Key = %+v", c.ref.Source, got, want)
		}
	}
}

func TestRemove(t *testing.T) {
	s := openTemp(t)
	k1 := Key{Source: k8s.SourceFile, Path: "/a", Context: "c"}
	k2 := Key{Source: k8s.SourceFile, Path: "/b", Context: "c"}
	_ = s.Update(k1, func(e *Entry) {})
	_ = s.Update(k2, func(e *Entry) {})
	if err := s.Remove(k1); err != nil {
		t.Fatal(err)
	}
	es, _ := s.Entries()
	if len(es) != 1 || es[0].Key() != k2 {
		t.Fatalf("got %+v", es)
	}
	if err := s.Remove(k1); err != nil {
		t.Fatalf("removing an absent key: %v", err)
	}
}

func TestStateKeepsUnknownEntries(t *testing.T) {
	s := openTemp(t)
	in := `version: 2
clusters:
  - source: future
    x: 1
  - source: file
    path: /a
    context: c
    newField: keep-me
`
	if err := os.WriteFile(filepath.Join(s.Dir(), "state.yaml"), []byte(in), 0o600); err != nil {
		t.Fatal(err)
	}
	es, err := s.Entries()
	if err != nil || len(es) != 1 || es[0].Source != k8s.SourceFile {
		t.Fatalf("got %+v, %v", es, err)
	}
	if err := s.Update(es[0].Key(), func(e *Entry) { e.Kind = "pods" }); err != nil {
		t.Fatal(err)
	}
	out := readState(t, s)
	for _, want := range []string{"source: future", "x: 1", "version: 2", "kind: pods", "newField: keep-me"} {
		if !strings.Contains(out, want) {
			t.Errorf("state.yaml lost %q:\n%s", want, out)
		}
	}
	if err := s.Remove(es[0].Key()); err != nil {
		t.Fatal(err)
	}
	if out := readState(t, s); !strings.Contains(out, "source: future") || strings.Contains(out, "/a") {
		t.Errorf("after Remove:\n%s", out)
	}
}

func TestStateHoldsNoCredentials(t *testing.T) {
	s := openTemp(t)
	secrets := []string{"s3cr3t-token", "LS0tLS1CRUdJTi", "client-key-data"}
	_ = s.Update(Key{Source: k8s.SourcePasted, File: "x.yaml", Context: "c"}, func(e *Entry) {
		e.Name = "x"
		e.Namespace = "ns"
		e.ApprovedExec = []string{"aws"}
	})
	out := readState(t, s)
	for _, sec := range secrets {
		if strings.Contains(out, sec) {
			t.Errorf("state.yaml contains %q", sec)
		}
	}
	if !strings.Contains(out, "approvedExec") {
		t.Errorf("missing approvedExec:\n%s", out)
	}
}
