package logstream

import (
	"bufio"
	"context"
	"sync"
	"time"

	tea "charm.land/bubbletea/v2"
	corev1 "k8s.io/api/core/v1"
	"k8s.io/client-go/kubernetes"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/msg"
)

// flushInterval is how often buffered log lines are delivered to the UI as one
// coalesced batch.
const flushInterval = 75 * time.Millisecond

// Session streams one pod/container's logs. The reader goroutine never blocks on
// the UI; the flusher delivers coalesced batches. Stop cancels both.
type Session struct {
	ID   string
	sink func(tea.Msg)
	buf  *buffer

	cancel    context.CancelFunc
	stopFlush chan struct{}
	stopOnce  sync.Once
}

// Start opens a follow log stream and begins delivering batches tagged with id.
func Start(parent context.Context, cs kubernetes.Interface, sink func(tea.Msg), id, namespace, pod, container string) *Session {
	ctx, cancel := context.WithCancel(parent)
	s := &Session{
		ID:        id,
		sink:      sink,
		buf:       newBuffer(DefaultCap),
		cancel:    cancel,
		stopFlush: make(chan struct{}),
	}
	go s.read(ctx, cs, namespace, pod, container)
	go s.flushLoop(ctx)
	return s
}

// Stop cancels the stream and the flusher.
func (s *Session) Stop() { s.cancel() }

func (s *Session) read(ctx context.Context, cs kubernetes.Interface, namespace, pod, container string) {
	tail := int64(500)
	req := cs.CoreV1().Pods(namespace).GetLogs(pod, &corev1.PodLogOptions{
		Follow:     true,
		TailLines:  &tail,
		Timestamps: true,
		Container:  container,
	})
	stream, err := req.Stream(ctx)
	if err != nil {
		s.stopFlushing()
		s.sink(msg.LogEnded{SessionID: s.ID, Err: err})
		return
	}
	defer stream.Close()

	sc := bufio.NewScanner(stream)
	sc.Buffer(make([]byte, 0, 64*1024), 1024*1024) // tolerate long lines
	for sc.Scan() {
		s.buf.append(sc.Text())
	}
	err = sc.Err()
	if ctx.Err() != nil {
		err = nil // a user-initiated cancel is a clean stop, not an error
	}

	s.flushNow() // deliver whatever remains
	s.stopFlushing()
	s.sink(msg.LogEnded{SessionID: s.ID, Err: err})
}

func (s *Session) flushLoop(ctx context.Context) {
	t := time.NewTicker(flushInterval)
	defer t.Stop()
	for {
		select {
		case <-ctx.Done():
			return
		case <-s.stopFlush:
			return
		case <-t.C:
			s.flushNow()
		}
	}
}

func (s *Session) flushNow() {
	lines, dropped := s.buf.drain()
	if len(lines) == 0 && dropped == 0 {
		return
	}
	s.sink(msg.LogBatch{SessionID: s.ID, Lines: lines, Dropped: dropped})
}

func (s *Session) stopFlushing() {
	s.stopOnce.Do(func() { close(s.stopFlush) })
}
