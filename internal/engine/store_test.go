package engine

import (
	"context"
	"errors"
	"fmt"
	"sync"
	"testing"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/types"
	"k8s.io/apimachinery/pkg/watch"
	clientfeatures "k8s.io/client-go/features"
	clientfeaturestesting "k8s.io/client-go/features/testing"
	"k8s.io/client-go/kubernetes/fake"
	"k8s.io/client-go/tools/cache"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// disableWatchList turns off the client-go watch-list streaming feature for a
// test. The fake clientset does not implement watch-list's sendInitialEvents
// bookmark, so the reflector would hang waiting for the initial list; a real
// apiserver either supports it or errors and the reflector falls back to list.
func disableWatchList(t *testing.T) {
	clientfeaturestesting.SetFeatureDuringTest(t, clientfeatures.WatchListClient, false)
}

// collector is a thread-safe snapshot sink for tests.
type collector struct {
	mu    sync.Mutex
	snaps []SnapshotMsg
}

func (c *collector) sink(m tea.Msg) {
	if s, ok := m.(SnapshotMsg); ok {
		c.mu.Lock()
		c.snaps = append(c.snaps, s)
		c.mu.Unlock()
	}
}

func (c *collector) count() int {
	c.mu.Lock()
	defer c.mu.Unlock()
	return len(c.snaps)
}

func (c *collector) lastPhase() (Phase, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.snaps) == 0 {
		return 0, false
	}
	return c.snaps[len(c.snaps)-1].Snap.Phase, true
}

func testPod(name string) *corev1.Pod {
	return &corev1.Pod{
		ObjectMeta: metav1.ObjectMeta{
			Name: name, Namespace: "default", UID: types.UID("uid-" + name), ResourceVersion: "1",
			CreationTimestamp: metav1.Now(),
		},
		Spec:   corev1.PodSpec{Containers: []corev1.Container{{Name: "c"}}},
		Status: corev1.PodStatus{Phase: corev1.PodRunning},
	}
}

func podListWatch(cs *fake.Clientset) *cache.ListWatch {
	// Set all four funcs (both the deprecated and the context-aware variants), as
	// cache.NewListWatchFromClient does in production; the reflector's watch-list
	// path in client-go 0.36 uses the context-aware funcs.
	return &cache.ListWatch{
		ListFunc: func(o metav1.ListOptions) (runtime.Object, error) {
			return cs.CoreV1().Pods("").List(context.Background(), o)
		},
		WatchFunc: func(o metav1.ListOptions) (watch.Interface, error) {
			return cs.CoreV1().Pods("").Watch(context.Background(), o)
		},
		ListWithContextFunc: func(ctx context.Context, o metav1.ListOptions) (runtime.Object, error) {
			return cs.CoreV1().Pods("").List(ctx, o)
		},
		WatchFuncWithContext: func(ctx context.Context, o metav1.ListOptions) (watch.Interface, error) {
			return cs.CoreV1().Pods("").Watch(ctx, o)
		},
	}
}

func waitFor(t *testing.T, cond func() bool, timeout time.Duration) {
	t.Helper()
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if cond() {
			return
		}
		time.Sleep(10 * time.Millisecond)
	}
	t.Fatalf("condition not met within %s", timeout)
}

// Ready path: a healthy list+watch reaches PhaseReady with the projected rows,
// and the sink receives at least one snapshot.
func TestViewStore_ReadyPath(t *testing.T) {
	disableWatchList(t)
	cs := fake.NewSimpleClientset(testPod("a"), testPod("b"), testPod("c"))
	c := &collector{}
	vs := NewViewStore("pods", podListWatch(cs), &corev1.Pod{}, columns.For("pods"), c.sink)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vs.Start(ctx)
	defer vs.Stop()

	waitFor(t, func() bool {
		s := vs.Snapshot()
		return s.Phase == PhaseReady && len(s.Rows) == 3
	}, 3*time.Second)

	// The first snapshot is delivered asynchronously by the coalescer loop (Start
	// no longer flushes synchronously), so wait for the sink rather than checking
	// it immediately.
	waitFor(t, func() bool { return c.count() > 0 }, 2*time.Second)
}

// Stale-not-cleared: a transient watch error marks the view stale but keeps the
// last-good rows; a subsequent event restores Ready. White-box so it is
// deterministic (no reliance on the reflector's timing).
func TestViewStore_StaleNeverClears(t *testing.T) {
	c := &collector{}
	vs := NewViewStore("pods", &cache.ListWatch{}, &corev1.Pod{}, columns.For("pods"), c.sink)
	// Seed the cache and force Ready.
	_ = vs.informer.GetStore().Add(testPod("a"))
	_ = vs.informer.GetStore().Add(testPod("b"))
	vs.mu.Lock()
	vs.phase = PhaseReady
	vs.mu.Unlock()

	if s := vs.Snapshot(); s.Phase != PhaseReady || len(s.Rows) != 2 {
		t.Fatalf("precondition: phase=%v rows=%d, want ready/2", s.Phase, len(s.Rows))
	}

	// Transient failure.
	vs.onWatchError(nil, &transientErr{})
	s := vs.Snapshot()
	if s.Phase != PhaseStale {
		t.Fatalf("phase = %v, want stale", s.Phase)
	}
	if len(s.Rows) != 2 {
		t.Fatalf("rows cleared on transient error: got %d, want 2", len(s.Rows))
	}

	// Recovery.
	vs.onEvent()
	if s := vs.Snapshot(); s.Phase != PhaseReady || len(s.Rows) != 2 {
		t.Fatalf("after recovery phase=%v rows=%d, want ready/2", s.Phase, len(s.Rows))
	}
}

// Terminal on 403: a forbidden watch error stops the store and is sticky — a
// later event does not resurrect it.
func TestViewStore_TerminalOn403(t *testing.T) {
	c := &collector{}
	vs := NewViewStore("pods", &cache.ListWatch{}, &corev1.Pod{}, columns.For("pods"), c.sink)
	_ = vs.informer.GetStore().Add(testPod("a"))
	vs.mu.Lock()
	vs.phase = PhaseReady
	vs.mu.Unlock()

	forbidden := apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("rbac"))
	vs.onWatchError(nil, forbidden)

	s := vs.Snapshot()
	if s.Phase != PhaseTerminal {
		t.Fatalf("phase = %v, want terminal", s.Phase)
	}
	if s.Err == nil || s.Err.Class != ClassForbidden {
		t.Fatalf("err = %+v, want forbidden", s.Err)
	}
	// Rows are still the last-good cache, not cleared.
	if len(s.Rows) != 1 {
		t.Fatalf("rows = %d, want 1 (last-good retained)", len(s.Rows))
	}
	// Sticky: an event must not clear terminal.
	vs.onEvent()
	if s := vs.Snapshot(); s.Phase != PhaseTerminal {
		t.Fatalf("terminal not sticky: phase = %v", s.Phase)
	}
}

// Coalescing: a burst of many adds collapses into far fewer snapshots than
// events, and the final snapshot reflects every add.
func TestViewStore_Coalesces(t *testing.T) {
	disableWatchList(t)
	cs := fake.NewSimpleClientset()
	c := &collector{}
	vs := NewViewStore("pods", podListWatch(cs), &corev1.Pod{}, columns.For("pods"), c.sink)

	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vs.Start(ctx)
	defer vs.Stop()

	// Wait for the initial (empty) sync.
	waitFor(t, func() bool { return vs.HasSynced() }, 3*time.Second)
	baseline := c.count()

	const n = 40
	for i := 0; i < n; i++ {
		_, err := cs.CoreV1().Pods("default").Create(ctx, testPod(fmt.Sprintf("p%02d", i)), metav1.CreateOptions{})
		if err != nil {
			t.Fatalf("create: %v", err)
		}
	}

	// All 40 objects land in the cache.
	waitFor(t, func() bool {
		return len(vs.Snapshot().Rows) == n
	}, 3*time.Second)

	// Wait for at least one coalesced emission after the burst (the coalescer
	// ticks every 150ms; the burst is far faster).
	waitFor(t, func() bool { return c.count() > baseline }, 2*time.Second)

	burst := c.count() - baseline
	if burst >= n {
		t.Fatalf("no coalescing: %d snapshots for %d events", burst, n)
	}
	if s := vs.Snapshot(); s.Phase != PhaseReady {
		t.Fatalf("final phase = %v, want ready", s.Phase)
	}
	if p, ok := c.lastPhase(); !ok || p != PhaseReady {
		t.Fatalf("last emitted phase = %v (ok=%v), want ready", p, ok)
	}
}

// Regression guard: Start is called from a page's OnEnter, which runs inside the
// Bubble Tea Update loop. A synchronous sink send there blocks on the program's
// unbuffered channel and deadlocks the whole UI (froze at "connecting"). Start
// must only emit snapshots asynchronously from the coalescer loop.
func TestStartDoesNotFlushSynchronously(t *testing.T) {
	c := &collector{}
	vs := NewViewStore("pods", &cache.ListWatch{}, &corev1.Pod{}, columns.For("pods"), c.sink)
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	vs.Start(ctx)
	defer vs.Stop()
	if n := c.count(); n != 0 {
		t.Fatalf("Start sent %d snapshots synchronously; must be async only (deadlock risk)", n)
	}
}

// Regression guard: ResumeAndFlush runs from Update (after a terminal handoff) and
// must not send synchronously either.
func TestResumeAndFlushIsAsync(t *testing.T) {
	c := &collector{}
	vs := NewViewStore("pods", &cache.ListWatch{}, &corev1.Pod{}, columns.For("pods"), c.sink)
	vs.ResumeAndFlush()
	if n := c.count(); n != 0 {
		t.Fatalf("ResumeAndFlush sent %d snapshots synchronously; must only mark dirty", n)
	}
}

// transientErr is a non-terminal error used to drive the stale path.
type transientErr struct{}

func (transientErr) Error() string { return "connection reset by peer" }
