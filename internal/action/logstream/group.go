package logstream

import (
	"bufio"
	"context"
	"sync"
	"sync/atomic"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/14f3v/kubectl-tui/internal/msg"
)

// Group tails many pods of a workload concurrently and merges them into ONE log
// stream, reusing the same bounded-buffer + flusher + msg pipeline as a single
// Session. Every reader appends into the shared buffer; the flusher drains it on
// the same cadence and delivers one coalesced LogBatch per tick under a single id.
// A single pod's failure is isolated to its own tagged line and never tears down
// the group — the merged stream ends only once every reader has finished.
type Group struct {
	ID   string
	sink func(tea.Msg)
	buf  *buffer

	cancel    context.CancelFunc
	stopFlush chan struct{}
	stopOnce  sync.Once

	// remaining counts readers still running; the last one to finish drives the
	// single terminal flush + LogEnded so the group ends exactly once.
	remaining atomic.Int32
}

// StartGroup opens a follow log stream for every PodRef and begins delivering
// merged, tagged batches under id. It mirrors Session's shape (one reader goroutine
// per source plus one flusher) so downstream UI handling is identical to a single
// session. If pods is empty the group ends immediately with a clean LogEnded.
func StartGroup(parent context.Context, cs kubernetes.Interface, sink func(tea.Msg), id, namespace string, pods []PodRef) *Group {
	ctx, cancel := context.WithCancel(parent)
	g := &Group{
		ID:        id,
		sink:      sink,
		buf:       newBuffer(DefaultCap),
		cancel:    cancel,
		stopFlush: make(chan struct{}),
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

// Stop cancels every reader and the flusher. It is idempotent: repeated calls (and
// a Stop racing with natural completion) close stopFlush at most once.
func (g *Group) Stop() {
	g.cancel()
	g.stopFlushing()
}

// read tails one container. An open error is surfaced as a single tagged line so the
// user sees which source failed, and this reader simply finishes; the rest keep going.
func (g *Group) read(ctx context.Context, cs kubernetes.Interface, namespace string, ref PodRef) {
	defer g.readerDone()

	tail := int64(500)
	req := cs.CoreV1().Pods(namespace).GetLogs(ref.Pod, &corev1.PodLogOptions{
		Follow:     true,
		TailLines:  &tail,
		Timestamps: true,
		Container:  ref.Container,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		g.buf.append(taggedLine(ref.Tag, "stream error: "+err.Error()))
		return
	}
	defer stream.Close()

	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // tolerate long lines
	for sc.Scan() {
		g.buf.append(taggedLine(ref.Tag, sc.Text()))
	}
}

// readerDone marks one reader finished and, when it is the last, performs the single
// terminal flush, stops the flusher, and ends the merged stream exactly once.
func (g *Group) readerDone() {
	if g.remaining.Add(-1) != 0 {
		return
	}
	g.flushNow() // deliver whatever remains before ending
	g.stopFlushing()
	g.sink(msg.LogEnded{SessionID: g.ID})
}

func (g *Group) flushLoop(ctx context.Context) {
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-g.stopFlush:
			return
		case <-t.C:
			g.flushNow()
		}
	}
}

func (g *Group) flushNow() {
	lines, dropped := g.buf.drain()
	if len(lines) == 0 && dropped == 0 {
		return
	}
	g.sink(msg.LogBatch{SessionID: g.ID, Lines: lines, Dropped: dropped})
}

func (g *Group) stopFlushing() {
	g.stopOnce.Do(func() { close(g.stopFlush) })
}
