package engine

import (
	"context"
	"sort"
	"sync"
	"sync/atomic"
	"time"

	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/client-go/tools/cache"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// defaultCoalesce is the maximum snapshot rate. Watch events set a dirty flag;
// the coalescer emits at most one snapshot per interval, so a burst of a thousand
// events becomes one render, not a thousand.
const defaultCoalesce = 150 * time.Millisecond

// ViewStore wraps one informer for one kind. It owns the informer lifecycle, the
// error taxonomy (via SetWatchErrorHandler), and the coalescer that turns watch
// events into pre-projected snapshots. It talks to the UI only through the sink.
type ViewStore struct {
	kind     string
	informer cache.SharedIndexInformer
	proj     columns.Projector
	sink     Sink
	interval time.Duration
	nowFn    func() time.Time

	mu       sync.Mutex
	phase    Phase
	err      *EngineErr
	syncedAt time.Time

	dirty  atomic.Bool
	paused atomic.Bool
	viewID atomic.Uint64

	stopCh   chan struct{}
	stopOnce sync.Once
	started  atomic.Bool
}

// NewViewStore builds a store for one kind from a ListerWatcher (so tests can
// inject a fake-clientset-backed list/watch) and the kind's projector. The
// transform and watch-error handler are installed before the informer runs.
func NewViewStore(kind string, lw cache.ListerWatcher, example runtime.Object, proj columns.Projector, sink Sink) *ViewStore {
	inf := cache.NewSharedIndexInformer(lw, example, 0, cache.Indexers{})
	vs := &ViewStore{
		kind:     kind,
		informer: inf,
		proj:     proj,
		sink:     sink,
		interval: defaultCoalesce,
		nowFn:    time.Now,
		phase:    PhaseLoading,
		stopCh:   make(chan struct{}),
	}
	_ = inf.SetTransform(stripManagedFields)
	_ = inf.SetWatchErrorHandler(vs.onWatchError)
	_, _ = inf.AddEventHandler(cache.ResourceEventHandlerFuncs{
		AddFunc:    func(any) { vs.onEvent() },
		UpdateFunc: func(any, any) { vs.onEvent() },
		DeleteFunc: func(any) { vs.onEvent() },
	})
	return vs
}

// Kind returns the store's kind key.
func (vs *ViewStore) Kind() string { return vs.kind }

// SetViewID stamps subsequent snapshots so the root model can drop snapshots for
// a view the user has navigated away from.
func (vs *ViewStore) SetViewID(id uint64) { vs.viewID.Store(id) }

// Start runs the informer and the coalescer. It is idempotent.
//
// It must NOT flush synchronously: Start is called from a page's OnEnter, which
// runs inside the Bubble Tea Update loop, and the sink (program.Send) blocks on
// an unbuffered channel until Update returns — flushing here self-deadlocks the
// UI. The page paints immediately by reading Snapshot() directly in OnEnter; the
// coalescer loop delivers the first sink-driven snapshot once the cache syncs.
func (vs *ViewStore) Start(ctx context.Context) {
	if !vs.started.CompareAndSwap(false, true) {
		return
	}
	go vs.informer.Run(vs.stopCh)
	go vs.loop(ctx)
}

// Stop halts the informer and the coalescer. The informer's cache is left intact
// so any already-rendered rows persist. Idempotent.
func (vs *ViewStore) Stop() {
	vs.stopOnce.Do(func() { close(vs.stopCh) })
}

// Pause suspends snapshot emission (used while a child process owns the
// terminal). Watch events still accumulate in the informer cache.
func (vs *ViewStore) Pause() { vs.paused.Store(true) }

// ResumeAndFlush re-enables emission and marks the store dirty so the coalescer
// loop pushes a fresh snapshot on its next tick. It does not flush synchronously:
// it is called from Update (after a terminal handoff), where a sink send would
// block on the unbuffered program channel and deadlock the UI.
func (vs *ViewStore) ResumeAndFlush() {
	vs.paused.Store(false)
	vs.dirty.Store(true)
}

// heartbeat forces a re-flush at least this often so the AGE column and the
// last-sync clock keep advancing even when no watch events arrive.
const heartbeat = 2 * time.Second

func (vs *ViewStore) loop(ctx context.Context) {
	t := time.NewTicker(vs.interval)
	defer t.Stop()
	syncedOnce := false
	sinceFlush := time.Duration(0)
	for {
		select {
		case <-ctx.Done():
			return
		case <-vs.stopCh:
			return
		case <-t.C:
			// The Loading→Ready edge fires no event, so nudge it from HasSynced.
			if !syncedOnce && vs.informer.HasSynced() {
				syncedOnce = true
				vs.dirty.Store(true)
			}
			if vs.paused.Load() {
				continue
			}
			sinceFlush += vs.interval
			if sinceFlush >= heartbeat && vs.RowCount() > 0 {
				vs.dirty.Store(true)
			}
			if vs.dirty.Swap(false) {
				vs.flush()
				sinceFlush = 0
			}
		}
	}
}

// RowCount returns the number of objects currently in the informer cache.
func (vs *ViewStore) RowCount() int { return len(vs.informer.GetStore().List()) }

// onEvent records a successful watch delivery. Any event proves the watch is
// healthy, so it clears a prior stale state — unless the store is terminal, which
// is sticky until an explicit restart.
func (vs *ViewStore) onEvent() {
	vs.mu.Lock()
	if vs.phase != PhaseTerminal {
		vs.phase = PhaseReady
		vs.err = nil
	}
	vs.mu.Unlock()
	vs.dirty.Store(true)
}

// onWatchError classifies a reflector failure. Terminal errors stop the informer
// and surface; transient errors mark the view stale but keep the last rows and
// let the reflector's own backoff reconnect.
func (vs *ViewStore) onWatchError(_ *cache.Reflector, err error) {
	ee := Classify("watch", err)
	if ee.Terminal() {
		vs.mu.Lock()
		vs.phase = PhaseTerminal
		vs.err = ee
		vs.mu.Unlock()
		vs.Stop()
		vs.flush()
		return
	}
	vs.mu.Lock()
	// Do not stomp a terminal phase with a late transient error.
	if vs.phase != PhaseTerminal {
		vs.phase = PhaseStale
		vs.err = ee
	}
	vs.mu.Unlock()
	vs.dirty.Store(true)
}

// flush computes and emits one snapshot, respecting the current view id.
func (vs *ViewStore) flush() {
	vs.sink(SnapshotMsg{
		Kind:   vs.kind,
		ViewID: vs.viewID.Load(),
		Snap:   vs.Snapshot(),
	})
}

// Snapshot projects the informer's current cache into a Remote envelope. It is
// safe to call at any time and is used directly by tests. Rows always reflect the
// last-good cache — even in stale or terminal phases — upholding the never-clear
// invariant.
func (vs *ViewStore) Snapshot() Remote[columns.Row] {
	vs.mu.Lock()
	phase, err, syncedAt := vs.phase, vs.err, vs.syncedAt
	vs.mu.Unlock()

	// Upgrade the initial Loading to Ready once the cache has synced, even if no
	// object events fired (e.g. an empty namespace).
	if phase == PhaseLoading && vs.informer.HasSynced() {
		phase = PhaseReady
	}

	now := vs.nowFn()
	objs := vs.informer.GetStore().List()
	rows := make([]columns.Row, 0, len(objs))
	for _, o := range objs {
		if r, ok := vs.proj.Project(o, now); ok {
			rows = append(rows, r)
		}
	}
	// Stable default order by name so snapshots are deterministic before the page
	// applies its own sort.
	sort.Slice(rows, func(i, j int) bool {
		if rows[i].Namespace != rows[j].Namespace {
			return rows[i].Namespace < rows[j].Namespace
		}
		return rows[i].Name < rows[j].Name
	})

	if phase == PhaseReady {
		syncedAt = now
		vs.mu.Lock()
		vs.syncedAt = now
		vs.mu.Unlock()
	}
	return Remote[columns.Row]{Phase: phase, Rows: rows, Err: err, SyncedAt: syncedAt}
}

// HasSynced reports whether the informer's initial list has completed.
func (vs *ViewStore) HasSynced() bool { return vs.informer.HasSynced() }

// Get returns the cached object for a namespace/name, or ok=false if absent.
// Cluster-scoped kinds pass an empty namespace. Used by inspect actions (yaml,
// describe) to read the live object without a fresh API call.
func (vs *ViewStore) Get(namespace, name string) (any, bool) {
	key := name
	if namespace != "" {
		key = namespace + "/" + name
	}
	obj, ok, err := vs.informer.GetStore().GetByKey(key)
	if err != nil || !ok {
		return nil, false
	}
	return obj, true
}
