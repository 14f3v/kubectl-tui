// Package portfwd manages pod port-forwards for the session. Each forward is a
// small state machine (Starting → Active → Broken/Closed); the manager outlives
// individual views and is torn down with the Session. There is no auto-reconnect:
// a broken forward is surfaced for the user to restart or delete.
package portfwd

import (
	"bytes"
	"fmt"
	"net/http"
	"sync"

	tea "charm.land/bubbletea/v2"
	"k8s.io/client-go/kubernetes"
	"k8s.io/client-go/rest"
	"k8s.io/client-go/tools/portforward"
	"k8s.io/client-go/transport/spdy"

	"github.com/14f3v/kubectl-tui/internal/msg"
)

// State is a forward's lifecycle state.
type State int

const (
	// Starting: dialing and establishing streams.
	Starting State = iota
	// Active: ready and forwarding.
	Active
	// Broken: the forward failed after starting.
	Broken
	// Closed: stopped by the user or session teardown.
	Closed
)

// String renders a state for the UI.
func (s State) String() string {
	switch s {
	case Starting:
		return "starting"
	case Active:
		return "active"
	case Broken:
		return "broken"
	case Closed:
		return "closed"
	}
	return "unknown"
}

// Forward is one port-forward session (value-copied for the UI via List).
type Forward struct {
	ID        string
	Namespace string
	Pod       string
	Ports     []string
	State     State
	Err       error
	Local     []portforward.ForwardedPort

	stopCh    chan struct{}
	closeOnce sync.Once
}

// Manager owns all forwards for a Session.
type Manager struct {
	cfg  *rest.Config
	cs   kubernetes.Interface
	sink func(tea.Msg)

	mu       sync.Mutex
	forwards map[string]*Forward
	seq      int
}

// NewManager builds a manager bound to a Session's config, clients, and sink.
func NewManager(cfg *rest.Config, cs kubernetes.Interface, sink func(tea.Msg)) *Manager {
	return &Manager{cfg: cfg, cs: cs, sink: sink, forwards: map[string]*Forward{}}
}

// Start begins forwarding ports (each "local:remote" or ":remote") to a pod and
// returns the tracked forward. The heavy work runs in a goroutine.
func (m *Manager) Start(namespace, pod string, ports []string) *Forward {
	m.mu.Lock()
	m.seq++
	id := fmt.Sprintf("%s/%s#%d", namespace, pod, m.seq)
	fw := &Forward{ID: id, Namespace: namespace, Pod: pod, Ports: ports, State: Starting, stopCh: make(chan struct{})}
	m.forwards[id] = fw
	m.mu.Unlock()

	go m.run(fw)
	m.notify()
	return fw
}

func (m *Manager) run(fw *Forward) {
	rt, upgrader, err := spdy.RoundTripperFor(m.cfg)
	if err != nil {
		m.fail(fw, err)
		return
	}
	u := m.cs.CoreV1().RESTClient().Post().
		Resource("pods").Namespace(fw.Namespace).Name(fw.Pod).SubResource("portforward").URL()
	dialer := spdy.NewDialer(upgrader, &http.Client{Transport: rt}, "POST", u)

	readyCh := make(chan struct{})
	var out, errOut bytes.Buffer
	pf, err := portforward.New(dialer, fw.Ports, fw.stopCh, readyCh, &out, &errOut)
	if err != nil {
		m.fail(fw, err)
		return
	}

	go func() {
		<-readyCh
		ports, _ := pf.GetPorts()
		m.mu.Lock()
		if fw.State == Starting {
			fw.State = Active
			fw.Local = ports
		}
		m.mu.Unlock()
		m.notify()
	}()

	err = pf.ForwardPorts() // blocks until stopCh is closed or an error occurs
	m.mu.Lock()
	if fw.State != Closed {
		if err != nil {
			fw.State, fw.Err = Broken, err
		} else {
			fw.State = Closed
		}
	}
	m.mu.Unlock()
	m.notify()
}

func (m *Manager) fail(fw *Forward, err error) {
	m.mu.Lock()
	fw.State, fw.Err = Broken, err
	m.mu.Unlock()
	m.notify()
}

// Stop closes a forward by id.
func (m *Manager) Stop(id string) {
	m.mu.Lock()
	fw := m.forwards[id]
	m.mu.Unlock()
	if fw == nil {
		return
	}
	m.mu.Lock()
	fw.State = Closed
	m.mu.Unlock()
	fw.closeOnce.Do(func() { close(fw.stopCh) })
	m.notify()
}

// Remove stops and forgets a forward.
func (m *Manager) Remove(id string) {
	m.Stop(id)
	m.mu.Lock()
	delete(m.forwards, id)
	m.mu.Unlock()
	m.notify()
}

// StopAll closes every forward (Session teardown).
func (m *Manager) StopAll() {
	m.mu.Lock()
	list := make([]*Forward, 0, len(m.forwards))
	for _, fw := range m.forwards {
		list = append(list, fw)
	}
	m.forwards = map[string]*Forward{}
	m.mu.Unlock()
	for _, fw := range list {
		fw.closeOnce.Do(func() { close(fw.stopCh) })
	}
}

// Count returns the number of tracked forwards (for the footer chip).
func (m *Manager) Count() int {
	m.mu.Lock()
	defer m.mu.Unlock()
	return len(m.forwards)
}

// Info is a lock-free snapshot of a Forward for the UI (Forward itself holds a
// sync.Once and must not be copied).
type Info struct {
	ID        string
	Namespace string
	Pod       string
	Ports     []string
	State     State
	Err       error
	Local     []portforward.ForwardedPort
}

// List returns a snapshot of the forwards.
func (m *Manager) List() []Info {
	m.mu.Lock()
	defer m.mu.Unlock()
	out := make([]Info, 0, len(m.forwards))
	for _, fw := range m.forwards {
		out = append(out, Info{
			ID: fw.ID, Namespace: fw.Namespace, Pod: fw.Pod, Ports: fw.Ports,
			State: fw.State, Err: fw.Err, Local: fw.Local,
		})
	}
	return out
}

// notify triggers a repaint of the :pf view. It dispatches on a goroutine because
// several callers (Start/Stop/Remove) run inside the Bubble Tea Update loop, where
// a direct sink send would block on the program's unbuffered channel and deadlock
// the UI. PFChanged is an idempotent repaint signal, so async delivery is safe.
func (m *Manager) notify() {
	if m.sink != nil {
		go m.sink(msg.PFChanged{})
	}
}
