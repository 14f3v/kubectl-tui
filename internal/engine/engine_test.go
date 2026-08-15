package engine

import (
	"context"
	"errors"
	"testing"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	apierrors "k8s.io/apimachinery/pkg/api/errors"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime"
	"k8s.io/apimachinery/pkg/runtime/schema"
	"k8s.io/apimachinery/pkg/watch"
	"k8s.io/client-go/tools/cache"

	"github.com/14f3v/kubectl-tui/internal/engine/columns"
)

// quietListWatch is an empty, well-behaved source: Ensure starts the informer, so
// unlike the store tests (which drive onWatchError directly and never Run) the
// ListWatch must actually work.
func quietListWatch() *cache.ListWatch {
	return &cache.ListWatch{
		ListFunc: func(metav1.ListOptions) (runtime.Object, error) {
			return &corev1.PodList{}, nil
		},
		WatchFunc: func(metav1.ListOptions) (watch.Interface, error) {
			return watch.NewFake(), nil
		},
	}
}

func newTestEngine(t *testing.T) *Engine {
	t.Helper()
	disableWatchList(t)
	e := NewEngine(context.Background(), func(tea.Msg) {})
	e.Register("pods", true, func(sink Sink, _ string) *ViewStore {
		return NewViewStore("pods", quietListWatch(), &corev1.Pod{}, columns.For("pods"), sink)
	})
	return e
}

func forbid(vs *ViewStore) {
	vs.onWatchError(nil, apierrors.NewForbidden(schema.GroupResource{Resource: "pods"}, "", errors.New("rbac")))
}

func TestViewStorePhaseAccessor(t *testing.T) {
	vs := NewViewStore("pods", quietListWatch(), &corev1.Pod{}, columns.For("pods"), func(tea.Msg) {})
	if got := vs.Phase(); got != PhaseLoading {
		t.Fatalf("initial Phase() = %v, want loading", got)
	}
	forbid(vs)
	if got := vs.Phase(); got != PhaseTerminal {
		t.Fatalf("Phase() after 403 = %v, want terminal", got)
	}
}

func TestEnsureReplacesTerminalStore(t *testing.T) {
	e := newTestEngine(t)

	first, err := e.Ensure("pods", "")
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	// A terminal watch error stops the store permanently: Stop is sync.Once-guarded
	// and started is a one-way CAS, so it can never run again.
	forbid(first)
	if first.Phase() != PhaseTerminal {
		t.Fatalf("phase = %v, want terminal", first.Phase())
	}

	second, err := e.Ensure("pods", "")
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second == first {
		t.Fatal("Ensure handed back the stopped store; the view can never recover for the rest of the session")
	}
	if p := second.Phase(); p == PhaseTerminal {
		t.Fatalf("replacement store phase = %v, want a fresh store", p)
	}
}

func TestEnsureReusesHealthyStore(t *testing.T) {
	// The replacement must be scoped to terminal stores only — a healthy warm kind
	// has to stay warm, which is the entire point of keeping it running.
	e := newTestEngine(t)

	first, err := e.Ensure("pods", "")
	if err != nil {
		t.Fatalf("first Ensure: %v", err)
	}
	second, err := e.Ensure("pods", "")
	if err != nil {
		t.Fatalf("second Ensure: %v", err)
	}
	if second != first {
		t.Fatal("Ensure rebuilt a healthy store; warm kinds must survive re-entry")
	}
}

func TestViewIDsAreUniqueAcrossEngines(t *testing.T) {
	// View ids are stamped on snapshots so the root model can drop late ones. A
	// per-Engine counter restarts at 0 for every new Session, so a stale snapshot
	// flushed by a disposed Session's store can collide with a live view's id —
	// and onWatchError flushes after Stop, so one is genuinely in flight.
	a, b := newTestEngine(t), newTestEngine(t)
	seen := map[uint64]bool{}
	for i := 0; i < 4; i++ {
		for _, e := range []*Engine{a, b} {
			id := e.NextViewID()
			if seen[id] {
				t.Fatalf("view id %d issued twice across engines", id)
			}
			seen[id] = true
		}
	}
}
