package app

import (
	"fmt"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
)

// stateModel builds the root model from cfg, with store s (nil for none), set
// to connect to context a of a file holding contexts a, b and c, whose
// namespaces are nsa, nsb and nsc. It returns the model and the refs of that
// file. Nothing connects until Init runs, so a test can fill state.yaml first.
func stateModel(t *testing.T, s *clusters.Store, cfg Config) (*Model, func(string) k8s.ClusterRef) {
	t.Helper()
	fast := fastServer(t)
	path := writeKubeconfig(t, fmt.Sprintf(threeClusters, fast, fast, fast))
	ref := func(name string) k8s.ClusterRef {
		return k8s.ClusterRef{Source: k8s.SourceFile, Path: path, Context: name}
	}
	cfg.Sink, cfg.Start, cfg.Store = func(tea.Msg) {}, ref("a"), s
	m := New(cfg)
	t.Cleanup(func() {
		m.cancelConnect()
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	return m, ref
}

// switchTo connects m to ref as a pick does and returns what Update answered
// the SessionReady with.
func switchTo(t *testing.T, m *Model, ref k8s.ClusterRef) tea.Cmd {
	t.Helper()
	_, connect := m.switchContext(ref)
	next := deliver(t, m, connect)
	if m.sess == nil || m.sess.Ref != ref {
		t.Fatalf("not connected to %+v", ref)
	}
	return next
}

// rememberAt records that an earlier run left ref on kind in ns at time at.
func rememberAt(t *testing.T, s *clusters.Store, ref k8s.ClusterRef, kind, ns string, at time.Time) {
	t.Helper()
	if err := s.Update(clusters.KeyFor(ref, ""), func(e *clusters.Entry) {
		e.Kind, e.Namespace, e.LastUsed = kind, ns, at
	}); err != nil {
		t.Fatal(err)
	}
}

// entryOf returns state.yaml's entry for a file ref, if it has one.
func entryOf(t *testing.T, s *clusters.Store, ref k8s.ClusterRef) (clusters.Entry, bool) {
	t.Helper()
	entries, err := s.Entries()
	if err != nil {
		t.Fatal(err)
	}
	for _, e := range entries {
		if e.Key() == clusters.KeyFor(ref, "") {
			return e, true
		}
	}
	return clusters.Entry{}, false
}

// onPage fails the test unless the active page is kind in namespace ns.
func onPage(t *testing.T, m *Model, kind, ns string) {
	t.Helper()
	p := m.active()
	if p == nil {
		t.Fatalf("no page; want %s in %q", kind, ns)
	}
	if p.Kind() != kind || p.Namespace() != ns {
		t.Fatalf("on %s in %q; want %s in %q", p.Kind(), p.Namespace(), kind, ns)
	}
}

// start runs cmd, and each command of a batch, each on its own goroutine, as
// Bubble Tea does, and does not wait for them: a debounce tick sleeps.
func start(cmd tea.Cmd) {
	if cmd == nil {
		return
	}
	go func() {
		if batch, ok := cmd().(tea.BatchMsg); ok {
			for _, c := range batch {
				start(c)
			}
		}
	}()
}

// eventually fails the test unless cond holds within a few seconds.
func eventually(t *testing.T, what string, cond func() bool) {
	t.Helper()
	deadline := time.Now().Add(5 * time.Second)
	for !cond() {
		if time.Now().After(deadline) {
			t.Fatalf("%s never happened", what)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

// saveTicks runs each command, and each command of a batch, each on its own
// goroutine, and once all have returned gives back the debounce ticks among
// their messages.
func saveTicks(t *testing.T, cmds ...tea.Cmd) []stateSaveMsg {
	t.Helper()
	var (
		wg   sync.WaitGroup
		mu   sync.Mutex
		got  []stateSaveMsg
		run  func(tea.Cmd)
		done = make(chan struct{})
	)
	run = func(c tea.Cmd) {
		if c == nil {
			return
		}
		wg.Add(1)
		go func() {
			defer wg.Done()
			switch res := c().(type) {
			case tea.BatchMsg:
				for _, c := range res {
					run(c)
				}
			case stateSaveMsg:
				mu.Lock()
				got = append(got, res)
				mu.Unlock()
			}
		}()
	}
	for _, c := range cmds {
		run(c)
	}
	go func() { wg.Wait(); close(done) }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		t.Fatal("the commands did not return")
	}
	return got
}

// Every view or namespace change is recorded, but written only once the
// changes stop: two in a row make one write, of the second.
func TestNavigateRecordsPositionDebounced(t *testing.T) {
	s := savedStore(t, "")
	m, ref := stateModel(t, s, Config{})
	deliver(t, m, m.Init()) // the connect's own write is left unrun

	_, first := m.navigate("deployments", "payments")
	_, second := m.navigate("svc", "web") // an alias is recorded by its kind
	if _, ok := entryOf(t, s, ref("a")); ok {
		t.Fatal("a view change was written at once; it waits for the debounce")
	}

	ticks := saveTicks(t, first, second)
	if len(ticks) != 2 {
		t.Fatalf("%d debounce ticks, want one per change", len(ticks))
	}
	var writes []tea.Cmd
	for _, tick := range ticks {
		if _, w := m.Update(tick); w != nil {
			writes = append(writes, w)
		}
	}
	if len(writes) != 1 {
		t.Fatalf("%d writes for two quick changes, want 1", len(writes))
	}
	writes[0]()

	e, ok := entryOf(t, s, ref("a"))
	if !ok || e.Kind != "services" || e.Namespace != "web" {
		t.Errorf("entry = %+v (found %v), want services in web", e, ok)
	}
	if e.LastUsed.IsZero() {
		t.Error("the write did not record when the cluster was used")
	}
}

// A cluster opens where it was left, all namespaces ("") included. One that
// state.yaml knows but that was never connected to opens on pods, in its
// context's namespace.
func TestRestoreRememberedPosition(t *testing.T) {
	s := savedStore(t, "")
	m, ref := stateModel(t, s, Config{})
	long := time.Now().Add(-time.Hour).Truncate(time.Second)
	rememberAt(t, s, ref("a"), "deployments", "payments", long)
	rememberAt(t, s, ref("c"), "services", "", long)
	// Loading b's file with f remembers it without using it.
	if err := s.Update(clusters.KeyFor(ref("b"), ""), func(e *clusters.Entry) { e.Server = "https://b" }); err != nil {
		t.Fatal(err)
	}

	connect := deliver(t, m, m.Init())
	onPage(t, m, "deployments", "payments")
	// The connect is written at once, with when it happened.
	start(connect)
	eventually(t, "recording a's last use", func() bool {
		e, _ := entryOf(t, s, ref("a"))
		return e.LastUsed.After(long)
	})
	if e, _ := entryOf(t, s, ref("a")); e.Kind != "deployments" || e.Namespace != "payments" {
		t.Errorf("recording the connect changed a's position: %+v", e)
	}

	switchTo(t, m, ref("b"))
	onPage(t, m, "pods", "nsb")

	switchTo(t, m, ref("c"))
	onPage(t, m, "services", "")
}

// A switch away writes where the user was at once, so coming back finds it.
func TestSwitchBackReturnsToPosition(t *testing.T) {
	s := savedStore(t, "")
	m, ref := stateModel(t, s, Config{})
	deliver(t, m, m.Init())
	onPage(t, m, "pods", "nsa")
	m.navigate("deployments", "payments")

	start(switchTo(t, m, ref("b")))
	eventually(t, "writing a's position", func() bool {
		e, _ := entryOf(t, s, ref("a"))
		return e.Kind == "deployments" && e.Namespace == "payments"
	})

	switchTo(t, m, ref("a"))
	onPage(t, m, "deployments", "payments")
}

// -kind picks the first cluster's view, over the remembered one, and leaves its
// namespace alone. Later connects restore, else open on pods.
func TestExplicitKindWinsOnFirstReadyOnly(t *testing.T) {
	s := savedStore(t, "")
	m, ref := stateModel(t, s, Config{StartKind: "svc", StartKindExplicit: true})
	long := time.Now().Add(-time.Hour)
	rememberAt(t, s, ref("a"), "deployments", "payments", long)
	rememberAt(t, s, ref("b"), "deployments", "web", long)

	deliver(t, m, m.Init())
	onPage(t, m, "services", "payments")

	switchTo(t, m, ref("b"))
	onPage(t, m, "deployments", "web")

	switchTo(t, m, ref("c")) // never used
	onPage(t, m, "pods", "nsc")
}

// A remembered view this build no longer has (or never had) falls back to pods;
// the namespace is still restored.
func TestUnknownRememberedKindFallsBackToPods(t *testing.T) {
	s := savedStore(t, "")
	m, ref := stateModel(t, s, Config{})
	rememberAt(t, s, ref("a"), "widgets", "payments", time.Now().Add(-time.Hour))

	deliver(t, m, m.Init())
	onPage(t, m, "pods", "payments")

	if kind, ns := m.startPosition(nil); kind != "pods" || ns != "nsa" {
		t.Errorf("startPosition(nil) = %s in %q, want pods in the context's nsa", kind, ns)
	}
}

// r on the expired-credentials banner rebuilds the Session and comes back to
// where the user was, before that position has reached state.yaml, and with
// no state.yaml at all.
func TestReconnectReturnsToPosition(t *testing.T) {
	for name, s := range map[string]func(*testing.T) *clusters.Store{
		"store":    func(t *testing.T) *clusters.Store { return savedStore(t, "") },
		"no store": func(*testing.T) *clusters.Store { return nil },
	} {
		t.Run(name, func(t *testing.T) {
			m, _ := stateModel(t, s(t), Config{})
			deliver(t, m, m.Init())
			before := m.sess
			m.navigate("deployments", "payments")

			_, connect := m.reconnect()
			deliver(t, m, connect)

			if m.sess == before {
				t.Fatal("the reconnect kept the old session")
			}
			onPage(t, m, "deployments", "payments")
		})
	}
}

// q inside the debounce still saves where the user was: main flushes once the
// program has stopped.
func TestFlushStateWritesPending(t *testing.T) {
	s := savedStore(t, "")
	m, ref := stateModel(t, s, Config{})
	deliver(t, m, m.Init()) // the connect's own write is left unrun
	m.navigate("deployments", "payments")

	if err := m.FlushState(); err != nil {
		t.Fatal(err)
	}
	e, ok := entryOf(t, s, ref("a"))
	if !ok || e.Kind != "deployments" || e.Namespace != "payments" || e.LastUsed.IsZero() {
		t.Fatalf("entry = %+v (found %v), want deployments in payments, used", e, ok)
	}

	// Nothing is left for the debounce to write.
	if _, w := m.Update(stateSaveMsg{token: m.saveToken}); w != nil {
		t.Error("the debounce writes again after the flush")
	}
	rememberAt(t, s, ref("a"), "services", "web", e.LastUsed)
	if err := m.FlushState(); err != nil {
		t.Fatal(err)
	}
	if e, _ := entryOf(t, s, ref("a")); e.Kind != "services" {
		t.Errorf("a second flush wrote %+v again", e)
	}
}

// Without a store nothing is recorded to disk: no write and no debounce tick.
func TestNoStoreNoWrites(t *testing.T) {
	m, _ := stateModel(t, nil, Config{StartKind: "portforwards", StartKindExplicit: true})

	if next := deliver(t, m, m.Init()); next != nil {
		t.Errorf("the connect answered with a command (%T) with no store to write", next())
	}
	if _, cmd := m.navigate("portforwards", ""); cmd != nil {
		t.Errorf("a view change answered with a command (%T) with no store to write", cmd())
	}
	if _, w := m.Update(stateSaveMsg{token: m.saveToken}); w != nil {
		t.Error("a debounce tick wrote with no store")
	}
	if err := m.FlushState(); err != nil {
		t.Errorf("FlushState with no store: %v", err)
	}
}
