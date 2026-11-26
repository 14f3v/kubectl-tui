package app

import (
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// threeClusters is a kubeconfig with contexts a, b and c, each on its own
// server; the test fills in the three server URLs.
const threeClusters = `apiVersion: v1
kind: Config
current-context: a
clusters:
- name: ca
  cluster: {server: "%s"}
- name: cb
  cluster: {server: "%s"}
- name: cc
  cluster: {server: "%s"}
users:
- name: u
  user: {token: t}
contexts:
- name: a
  context: {cluster: ca, user: u, namespace: nsa}
- name: b
  context: {cluster: cb, user: u, namespace: nsb}
- name: c
  context: {cluster: cc, user: u, namespace: nsc}
`

// writeKubeconfig is internal/k8s's test helper of the same name.
func writeKubeconfig(t *testing.T, yaml string) string {
	t.Helper()
	p := filepath.Join(t.TempDir(), "config")
	if err := os.WriteFile(p, []byte(yaml), 0o600); err != nil {
		t.Fatal(err)
	}
	return p
}

// apiServer is as much of a Kubernetes API as a bootstrap needs: version answers
// the version probe and everything else is a 404. It returns the server's URL.
func apiServer(t *testing.T, version http.HandlerFunc) string {
	t.Helper()
	srv := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path == "/version" {
			version(w, r)
			return
		}
		http.NotFound(w, r)
	}))
	t.Cleanup(srv.Close)
	return srv.URL
}

// fastServer answers the version probe at once.
func fastServer(t *testing.T) string {
	return apiServer(t, func(w http.ResponseWriter, _ *http.Request) {
		_, _ = w.Write([]byte(`{"gitVersion":"v1.31.0"}`))
	})
}

// connected builds the root model as main does and runs its launch bootstrap for
// context a, so the model starts on a real Session. Context b is on serverB;
// a and c answer at once.
func connected(t *testing.T, serverB string) (m *Model, a *k8s.Session, refB, refC k8s.ClusterRef) {
	t.Helper()
	fast := fastServer(t)
	path := writeKubeconfig(t, fmt.Sprintf(threeClusters, fast, serverB, fast))
	ref := func(name string) k8s.ClusterRef {
		return k8s.ClusterRef{Source: k8s.SourceFile, Path: path, Context: name}
	}
	// The launch opens the port-forwards page, which starts no informer, so it
	// talks to nothing but the version probe. -kind is for the first connect
	// only: a switch opens pods, whose watches the test server answers with 404.
	m = New(Config{Sink: func(tea.Msg) {}, Start: ref("a"), StartKind: "portforwards", StartKindExplicit: true})
	t.Cleanup(func() {
		m.cancelConnect()
		if m.sess != nil {
			m.sess.Dispose()
		}
	})
	deliver(t, m, m.Init())
	if m.sess == nil {
		t.Fatalf("launch did not connect: fatal = %v", m.fatal)
	}
	return m, m.sess, ref("b"), ref("c")
}

// deliver runs cmd as Bubble Tea would and feeds its message to Update,
// returning the command Update answers with.
func deliver(t *testing.T, m *Model, cmd tea.Cmd) tea.Cmd {
	t.Helper()
	if cmd == nil {
		t.Fatal("no command to run")
	}
	_, next := m.Update(cmd())
	return next
}

func disposed(s *k8s.Session) bool { return s.Context().Err() != nil }

func TestSwitchKeepsSessionUntilReady(t *testing.T) {
	m, a, refB, _ := connected(t, fastServer(t))

	_, cmd := m.switchContext(refB)

	if m.sess != a || disposed(a) {
		t.Fatal("the switch dropped the current session before the new one connected")
	}
	if m.connecting == nil || *m.connecting != refB {
		t.Fatalf("connecting = %v, want %v", m.connecting, refB)
	}

	deliver(t, m, cmd)

	if m.sess == nil || m.sess == a || m.sess.Ref != refB {
		t.Fatalf("after b connected the session is %+v, want b's", m.sess)
	}
	if m.ref != refB {
		t.Errorf("ref = %+v, want %+v", m.ref, refB)
	}
	if !disposed(a) {
		t.Error("the previous session was not disposed once b connected")
	}
	if m.connecting != nil {
		t.Errorf("connecting = %v after the connect finished, want nil", m.connecting)
	}
}

// Two quick picks: b's result arrives after c was picked, so it is stale and its
// Session is disposed rather than leaked or swapped in.
func TestStaleSessionReadyIsDisposed(t *testing.T) {
	m, a, refB, refC := connected(t, fastServer(t))
	_, cmdB := m.switchContext(refB)
	m.switchContext(refC)

	ready, ok := cmdB().(msg.SessionReady)
	if !ok {
		t.Fatal("b's bootstrap did not produce a SessionReady")
	}
	m.Update(ready)

	if m.sess != a {
		t.Error("a stale SessionReady replaced the current session")
	}
	if b := ready.Session.(*k8s.Session); !disposed(b) {
		t.Error("the stale session was not disposed")
	}
	if m.connecting == nil || *m.connecting != refC {
		t.Errorf("connecting = %v, want c still in flight", m.connecting)
	}
}

func TestStaleSessionErrorIsIgnored(t *testing.T) {
	m, a, refB, refC := connected(t, fastServer(t))
	refB.Context = "nope"
	_, cmdB := m.switchContext(refB)
	m.switchContext(refC)

	deliver(t, m, cmdB)

	if m.sess != a || m.toast != nil {
		t.Errorf("a stale SessionError acted: session changed = %v, toast = %+v", m.sess != a, m.toast)
	}
	if m.connecting == nil || *m.connecting != refC {
		t.Errorf("connecting = %v, want c still in flight", m.connecting)
	}
}

func TestFailedSwitchLeavesCurrentSession(t *testing.T) {
	m, a, refB, _ := connected(t, fastServer(t))
	refA := m.ref
	missing := refB
	missing.Context = "nope"

	_, cmd := m.switchContext(missing)
	deliver(t, m, cmd)

	if m.sess != a || disposed(a) {
		t.Fatal("a failed switch took the current session down")
	}
	if m.ref != refA {
		t.Errorf("ref = %+v after a failed switch, want %+v", m.ref, refA)
	}
	if m.fatal != nil {
		t.Errorf("a failed switch is fatal: %v", m.fatal)
	}
	if m.toast == nil || m.toast.Level != msg.LevelError || !strings.HasPrefix(m.toast.Text, "connect nope: ") {
		t.Errorf("toast = %+v, want an error starting %q", m.toast, "connect nope: ")
	}
	if m.connecting != nil {
		t.Errorf("connecting = %v after the connect failed, want nil", m.connecting)
	}
}

// b's version probe hangs; picking c must cancel it rather than leave it running
// until the probe's own 8s timeout.
func TestNewPickCancelsConnectInFlight(t *testing.T) {
	probed := make(chan struct{}, 1)
	slow := apiServer(t, func(_ http.ResponseWriter, r *http.Request) {
		select {
		case probed <- struct{}{}:
		default:
		}
		<-r.Context().Done() // answer only once the client gives up
	})
	m, _, refB, refC := connected(t, slow)

	_, cmdB := m.switchContext(refB)
	if m.bootCancel == nil {
		t.Fatal("the connect to b has no cancel func")
	}
	result := make(chan tea.Msg, 1)
	go func() { result <- cmdB() }()
	select {
	case <-probed:
	case <-time.After(5 * time.Second):
		t.Fatal("b's version probe never reached its server")
	}

	m.switchContext(refC)

	select {
	case res := <-result:
		m.Update(res) // stale now, so b's Session is disposed
		if r, ok := res.(msg.SessionReady); ok && !disposed(r.Session.(*k8s.Session)) {
			t.Error("the cancelled connect's session was not disposed")
		}
	case <-time.After(4 * time.Second):
		t.Fatal("picking c did not cancel b's version probe")
	}
}

func TestSwitchResetsMetricsGauges(t *testing.T) {
	m, _, refB, _ := connected(t, fastServer(t))
	m.metricsOK, m.clusterCPU, m.clusterMem = true, 42, 17

	_, cmd := m.switchContext(refB)
	if !m.metricsOK || m.clusterCPU != 42 || m.clusterMem != 17 {
		t.Error("the gauges were reset while a was still the current cluster")
	}
	deliver(t, m, cmd)

	if m.metricsOK || m.clusterCPU != 0 || m.clusterMem != 0 {
		t.Errorf("gauges after the switch = %v/%v/%v, want false/0/0", m.metricsOK, m.clusterCPU, m.clusterMem)
	}
}

func TestSwitchWithForwardsAsksFirst(t *testing.T) {
	m, a, refB, _ := connected(t, fastServer(t))
	a.Forwards.Start("default", "web", []string{"8080:80"})

	_, cmd := m.Update(view.SwitchContextRequest{Ref: refB})
	if m.connecting != nil {
		t.Fatal("the switch started before the user agreed to close the forwards")
	}
	req, ok := cmd().(view.ConfirmRequest)
	if !ok {
		t.Fatal("a switch with a forward open did not ask first")
	}
	if want := "Switching closes 1 port-forwards."; req.Prompt != want {
		t.Errorf("prompt = %q, want %q", req.Prompt, want)
	}

	// Saying yes starts the connect; the forward stays open until it lands.
	m.Update(req)
	_, yes := m.handleKey(tea.KeyPressMsg{Code: 'y', Text: "y"})
	connect := deliver(t, m, yes)
	if m.connecting == nil || *m.connecting != refB {
		t.Fatalf("connecting = %v after yes, want %v", m.connecting, refB)
	}
	if a.Forwards.Count() != 1 {
		t.Error("the forward closed before the new cluster connected")
	}
	deliver(t, m, connect)
	if m.sess.Ref != refB || a.Forwards.Count() != 0 {
		t.Errorf("after connecting: on %+v with %d forwards left on a", m.sess.Ref, a.Forwards.Count())
	}
}

// reconnect asks in its own words, and a yes connects without asking again.
func TestReconnectWithForwardsAsksOnce(t *testing.T) {
	m, a, _, _ := connected(t, fastServer(t))
	a.Forwards.Start("default", "web", []string{"8080:80"})

	_, cmd := m.reconnect()
	req, ok := cmd().(view.ConfirmRequest)
	if !ok || !strings.HasPrefix(req.Prompt, "re-authenticate and rebuild the session?") {
		t.Fatalf("reconnect asked %+v, want its own prompt", req)
	}
	_, connect := m.Update(req.Action())
	if m.connecting == nil || *m.connecting != a.Ref {
		t.Fatalf("connecting = %v after yes, want %v", m.connecting, a.Ref)
	}
	res := connect()
	if _, asked := res.(view.ConfirmRequest); asked {
		t.Fatal("reconnect asked a second time")
	}
	m.Update(res)
	if m.sess == a || !disposed(a) {
		t.Error("the reconnect did not replace the session")
	}
}
