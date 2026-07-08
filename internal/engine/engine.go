package engine

import (
	"context"
	"fmt"
	"sync"
	"sync/atomic"
)

// FactoryFn builds a ViewStore for a kind scoped to a namespace (empty = all
// namespaces). The engine calls it lazily on first view and again after a
// namespace change. Factories are registered by the wiring layer, which owns the
// clients — the engine itself never imports the Kubernetes client packages.
type FactoryFn func(sink Sink, namespace string) *ViewStore

// Engine owns the set of live ViewStores. Core kinds are lazy-started and kept
// warm for the session; screen-scoped kinds (events, tenants) are stopped when
// their view is left. There is one Engine per Session; disposing the Session's
// context stops every store.
type Engine struct {
	ctx  context.Context
	sink Sink

	mu        sync.Mutex
	factories map[string]FactoryFn
	warm      map[string]bool
	stores    map[string]*ViewStore
	ns        map[string]string

	viewSeq atomic.Uint64
}

// NewEngine creates an engine bound to a Session context and a snapshot sink.
func NewEngine(ctx context.Context, sink Sink) *Engine {
	return &Engine{
		ctx:       ctx,
		sink:      sink,
		factories: map[string]FactoryFn{},
		warm:      map[string]bool{},
		stores:    map[string]*ViewStore{},
		ns:        map[string]string{},
	}
}

// Register wires a kind to its factory. warm=true keeps the store running after
// its view is left (instant re-entry); warm=false makes it screen-scoped.
func (e *Engine) Register(kind string, warm bool, f FactoryFn) {
	e.mu.Lock()
	defer e.mu.Unlock()
	e.factories[kind] = f
	e.warm[kind] = warm
}

// NextViewID returns a fresh monotonic view id. A page takes one on entry and
// stamps it on its store so late snapshots from a previous entry are dropped.
func (e *Engine) NextViewID() uint64 { return e.viewSeq.Add(1) }

// Sink returns the message sink (tea.Program.Send in production). Action
// subsystems that deliver their own messages (logs, port-forward) use it.
func (e *Engine) Sink() Sink { return e.sink }

// Ensure returns the running store for a kind, scoped to namespace, building and
// starting it on first use. If the namespace changed since last time, the old
// store is stopped and a fresh one is started so only that one kind relists.
func (e *Engine) Ensure(kind, namespace string) (*ViewStore, error) {
	e.mu.Lock()
	defer e.mu.Unlock()

	f, ok := e.factories[kind]
	if !ok {
		return nil, fmt.Errorf("engine: no factory registered for kind %q", kind)
	}

	if vs, ok := e.stores[kind]; ok {
		if e.ns[kind] == namespace {
			return vs, nil
		}
		vs.Stop()
		delete(e.stores, kind)
	}

	vs := f(e.sink, namespace)
	e.stores[kind] = vs
	e.ns[kind] = namespace
	vs.Start(e.ctx)
	return vs, nil
}

// Get returns the running store for a kind, or nil if it has not been started.
func (e *Engine) Get(kind string) *ViewStore {
	e.mu.Lock()
	defer e.mu.Unlock()
	return e.stores[kind]
}

// StopKind stops and forgets one kind's store (e.g. an RBAC-forbidden view the
// user leaves, or a screen-scoped kind on page exit).
func (e *Engine) StopKind(kind string) {
	e.mu.Lock()
	vs := e.stores[kind]
	delete(e.stores, kind)
	delete(e.ns, kind)
	e.mu.Unlock()
	if vs != nil {
		vs.Stop()
	}
}

// StopIfScreenScoped stops a kind's store only if it was registered screen-scoped;
// warm kinds are left running. Called when a page is left.
func (e *Engine) StopIfScreenScoped(kind string) {
	e.mu.Lock()
	warm := e.warm[kind]
	e.mu.Unlock()
	if !warm {
		e.StopKind(kind)
	}
}

// PauseAll suspends emission on every live store (TerminalGate handoff).
func (e *Engine) PauseAll() {
	e.mu.Lock()
	stores := make([]*ViewStore, 0, len(e.stores))
	for _, vs := range e.stores {
		stores = append(stores, vs)
	}
	e.mu.Unlock()
	for _, vs := range stores {
		vs.Pause()
	}
}

// ResumeAllAndFlush re-enables emission and pushes one fresh snapshot per store
// (TerminalGate restore).
func (e *Engine) ResumeAllAndFlush() {
	e.mu.Lock()
	stores := make([]*ViewStore, 0, len(e.stores))
	for _, vs := range e.stores {
		stores = append(stores, vs)
	}
	e.mu.Unlock()
	for _, vs := range stores {
		vs.ResumeAndFlush()
	}
}

// StopAll stops every store. Called when a Session is disposed.
func (e *Engine) StopAll() {
	e.mu.Lock()
	stores := e.stores
	e.stores = map[string]*ViewStore{}
	e.ns = map[string]string{}
	e.mu.Unlock()
	for _, vs := range stores {
		vs.Stop()
	}
}
