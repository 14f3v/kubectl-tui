package logstream

import (
	"context"
	"os"
	"strings"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/14f3v/kubectl-tui/internal/msg"
)

// defaultTail is the number of trailing lines to fetch when the caller does not
// ask for a specific tail. It matches the historical hard-coded value so a
// zero-valued Options{} reproduces the pre-options behavior exactly.
const defaultTail = int64(500)

// Options captures the tunable log-fetch knobs a user can pick when opening a
// stream, mapped 1:1 onto the fields of corev1.PodLogOptions we care about. It is
// intentionally a plain value type with a zero value (Options{}) that reproduces
// the package's original defaults, so existing callers keep their behavior.
type Options struct {
	// Previous requests the logs of the container's previous, terminated instance
	// (kubectl logs -p). Because that container is gone, its logs cannot be
	// followed, so setting Previous forces a one-shot (non-follow) read.
	Previous bool
	// SinceSeconds, when non-nil, limits the stream to lines newer than this many
	// seconds ago. It is ignored for previous-container logs, which are a fixed,
	// already-terminated log the API server returns in full.
	SinceSeconds *int64
	// TailLines is how many trailing lines to fetch before streaming new ones. Zero
	// means "unset" and falls back to defaultTail so Options{} behaves like the old
	// hard-coded tail.
	TailLines int64
}

// podLogOptions renders these Options into the corev1.PodLogOptions the API
// expects for the given container. Timestamps are always on (the UI relies on
// them). Follow is enabled only when we are NOT reading previous logs, since a
// terminated container's log is complete and cannot be followed. SinceSeconds is
// likewise only meaningful for the live stream, so it is dropped for previous
// logs to keep the request self-consistent.
func (o Options) podLogOptions(container string) *corev1.PodLogOptions {
	tail := o.TailLines
	if tail == 0 {
		tail = defaultTail
	}
	opts := &corev1.PodLogOptions{
		Container:  container,
		Timestamps: true,
		TailLines:  &tail,
		Follow:     !o.Previous,
		Previous:   o.Previous,
	}
	if !o.Previous {
		opts.SinceSeconds = o.SinceSeconds
	}
	return opts
}

// StartWithOptions opens a log stream for one pod/container using the given
// Options and begins delivering batches tagged with id. Start is the zero-Options
// special case of this function; both share the same reader/flusher machinery.
func StartWithOptions(parent context.Context, cs kubernetes.Interface, sink func(tea.Msg), id, namespace, pod, container string, opts Options) *Session {
	ctx, cancel := context.WithCancel(parent)
	s := &Session{
		ID:        id,
		sink:      sink,
		buf:       newBuffer(DefaultCap),
		cancel:    cancel,
		stopFlush: make(chan struct{}),
		opts:      opts,
	}
	go s.read(ctx, cs, namespace, pod, container)
	go s.flushLoop(ctx)
	return s
}

// StartGroupWithOptions opens a merged log stream for every PodRef using the given
// Options. StartGroup is the zero-Options special case; the two share the same
// merge/flush machinery so downstream UI handling is identical.
func StartGroupWithOptions(parent context.Context, cs kubernetes.Interface, sink func(tea.Msg), id, namespace string, pods []PodRef, opts Options) *Group {
	ctx, cancel := context.WithCancel(parent)
	g := &Group{
		ID:        id,
		sink:      sink,
		buf:       newBuffer(DefaultCap),
		cancel:    cancel,
		stopFlush: make(chan struct{}),
		opts:      opts,
	}
	g.remaining.Store(int32(len(pods)))

	go g.flushLoop(ctx)

	// With no sources there is no reader to drive the terminal flush, so end here.
	if len(pods) == 0 {
		g.stopFlushing()
		g.sink(msg.LogEnded{SessionID: g.ID})
		return g
	}

	for _, ref := range pods {
		go g.read(ctx, cs, namespace, ref)
	}
	return g
}

// SaveLines writes the given log lines to path, one per line with a trailing
// newline, at 0o644. It is the "save buffer to file" action: the UI hands it the
// lines it currently has and a destination, and this performs the whole write in
// one call. An empty slice still produces a single trailing newline, matching how
// the buffer's textual form is defined (join + newline).
func SaveLines(path string, lines []string) error {
	data := strings.Join(lines, "\n") + "\n"
	return os.WriteFile(path, []byte(data), 0o644)
}
