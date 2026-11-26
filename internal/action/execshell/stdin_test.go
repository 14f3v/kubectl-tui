package execshell

import (
	"errors"
	"io"
	"strings"
	"sync/atomic"
	"testing"
	"time"
	"unicode/utf16"
)

// parkedReader is a Read that blocks until it is released, like a terminal read
// waiting for the next key.
type parkedReader struct {
	entered chan struct{}   // gets a value each time a Read starts
	release chan readResult // what the blocked Read returns
}

type readResult struct {
	data string
	err  error
}

func newParkedReader() *parkedReader {
	return &parkedReader{entered: make(chan struct{}, 8), release: make(chan readResult, 8)}
}

func (r *parkedReader) Read(p []byte) (int, error) {
	r.entered <- struct{}{}
	res := <-r.release
	return copy(p, res.data), res.err
}

type readReturn struct {
	n   int
	err error
}

// parkRead starts a Read on s the way client-go's stdin copy does and waits
// until it is blocked in the reader underneath.
func parkRead(t *testing.T, s *sessionStdin, pr *parkedReader) <-chan readReturn {
	t.Helper()
	got := make(chan readReturn, 1)
	go func() {
		n, err := s.Read(make([]byte, 8))
		got <- readReturn{n, err}
	}()
	select {
	case <-pr.entered:
	case <-time.After(5 * time.Second):
		t.Fatal("Read never reached the underlying reader")
	}
	return got
}

func await(t *testing.T, got <-chan readReturn) readReturn {
	t.Helper()
	select {
	case r := <-got:
		return r
	case <-time.After(5 * time.Second):
		t.Fatal("the parked Read never returned")
		return readReturn{}
	}
}

func TestSessionStdinPassesInputThrough(t *testing.T) {
	s := &sessionStdin{r: strings.NewReader("ls\r\x1a")}
	got, err := io.ReadAll(s)
	if err != nil || string(got) != "ls\r\x1a" {
		t.Fatalf("ReadAll = (%q, %v), want (%q, nil)", got, err, "ls\r\x1a")
	}
}

// TestSessionStdinStopInterruptsParkedRead is the leaked-reader bug: when the
// shell exits, client-go's copy is still blocked in Read. stop must interrupt
// it, wait for it, release the reader, and make it report a clean EOF so the
// copy ends without logging.
func TestSessionStdinStopInterruptsParkedRead(t *testing.T) {
	cases := []struct {
		name     string
		released readResult
	}{
		{"cancelled read", readResult{err: errors.New("read canceled")}},
		// A key that arrives just as the stream ends has no session to go to; it
		// must not be written to the closed stream.
		{"key that raced the stop", readResult{data: "j"}},
	}
	for _, tc := range cases {
		pr := newParkedReader()
		var cancels atomic.Int32
		var closed atomic.Bool
		s := &sessionStdin{
			r: pr,
			cancel: func() {
				if cancels.Add(1) == 1 {
					pr.release <- tc.released
				}
			},
			close: func() error { closed.Store(true); return nil },
		}
		got := parkRead(t, s, pr)

		s.stop()
		if cancels.Load() == 0 {
			t.Fatalf("%s: stop did not cancel the parked read", tc.name)
		}
		if !closed.Load() {
			t.Fatalf("%s: stop did not release the reader once the read returned", tc.name)
		}
		if r := await(t, got); r.n != 0 || r.err != io.EOF {
			t.Fatalf("%s: parked Read = (%d, %v), want (0, io.EOF)", tc.name, r.n, r.err)
		}
		if n, err := s.Read(make([]byte, 8)); n != 0 || err != io.EOF {
			t.Fatalf("%s: Read after stop = (%d, %v), want (0, io.EOF)", tc.name, n, err)
		}
		select {
		case <-pr.entered:
			t.Fatalf("%s: a Read after stop reached the reader", tc.name)
		default:
		}
	}
}

// TestSessionStdinStopRetriesCancel covers a read that has taken its turn but
// not yet reached the blocking call, where the first cancel finds nothing to
// interrupt.
func TestSessionStdinStopRetriesCancel(t *testing.T) {
	pr := newParkedReader()
	var cancels atomic.Int32
	var closed atomic.Bool
	s := &sessionStdin{
		r: pr,
		cancel: func() {
			if cancels.Add(1) == 3 {
				pr.release <- readResult{err: errors.New("read canceled")}
			}
		},
		close: func() error { closed.Store(true); return nil },
	}
	got := parkRead(t, s, pr)
	s.stop()
	if !closed.Load() {
		t.Fatalf("stop gave up after %d cancels; want it to keep trying until the read returns", cancels.Load())
	}
	if r := await(t, got); r.err != io.EOF {
		t.Fatalf("parked Read err = %v, want io.EOF", r.err)
	}
}

// TestSessionStdinStopGivesUpOnStuckRead pins the fallback: a read that cannot
// be interrupted must not hold up the return to the TUI, must not have its
// reader released under it, and must still report EOF, not the key it took,
// when it finally returns.
func TestSessionStdinStopGivesUpOnStuckRead(t *testing.T) {
	for name, cancel := range map[string]func(){"cancel has no effect": func() {}, "no cancel": nil} {
		pr := newParkedReader()
		var closed atomic.Bool
		s := &sessionStdin{r: pr, cancel: cancel, close: func() error { closed.Store(true); return nil }}
		got := parkRead(t, s, pr)

		stopped := make(chan struct{})
		go func() { s.stop(); close(stopped) }()
		select {
		case <-stopped:
		case <-time.After(5 * time.Second):
			t.Fatalf("%s: stop is stuck behind a read it cannot interrupt", name)
		}
		if closed.Load() {
			t.Fatalf("%s: stop released the reader while a read was still blocked in it", name)
		}

		pr.release <- readResult{data: "j"}
		if r := await(t, got); r.n != 0 || r.err != io.EOF {
			t.Fatalf("%s: late Read = (%d, %v), want (0, io.EOF)", name, r.n, r.err)
		}
	}
}

func TestSessionStdinStopWithNoReadInFlight(t *testing.T) {
	var closed atomic.Bool
	s := &sessionStdin{
		r:      strings.NewReader("never read"),
		cancel: func() { t.Error("nothing to cancel, yet stop cancelled") },
		close:  func() error { closed.Store(true); return nil },
	}
	s.stop()
	if !closed.Load() {
		t.Fatal("stop did not release the reader")
	}
	if n, err := s.Read(make([]byte, 8)); n != 0 || err != io.EOF {
		t.Fatalf("Read after stop = (%d, %v), want (0, io.EOF)", n, err)
	}
}

// fakeConsole stands in for ReadConsoleW: each call hands back the next chunk
// of UTF-16 code units, and once they run out it reports a read that ended with
// nothing in it, as the console does.
func fakeConsole(t *testing.T, chunks ...[]uint16) func([]uint16) (int, error) {
	return func(buf []uint16) (int, error) {
		if len(chunks) == 0 {
			return 0, nil
		}
		c := chunks[0]
		chunks = chunks[1:]
		if len(c) > len(buf) {
			t.Fatalf("chunk of %d units does not fit a %d-unit read", len(c), len(buf))
		}
		return copy(buf, c), nil
	}
}

func units(s string) []uint16 { return utf16.Encode([]rune(s)) }

// readAll drains r with reads of size n, so short buffers are exercised too.
func readAll(t *testing.T, r io.Reader, n int) (string, error) {
	t.Helper()
	var out []byte
	buf := make([]byte, n)
	for {
		k, err := r.Read(buf)
		out = append(out, buf[:k]...)
		if err != nil {
			return string(out), err
		}
		if k == 0 {
			t.Fatal("Read returned 0 bytes with no error")
		}
	}
}

func TestConsoleReader(t *testing.T) {
	const ctrlZ = "\x1a"
	cases := []struct {
		name   string
		chunks [][]uint16
		want   string
	}{
		{"plain keys", [][]uint16{units("ls -la\r")}, "ls -la\r"},
		// Go's own console read swallows a leading 0x1A and reports EOF.
		{"ctrl-z first in a read", [][]uint16{units(ctrlZ), units("fg\r")}, ctrlZ + "fg\r"},
		// ...and stops short at one mid-buffer, with the EOF on the next read.
		{"ctrl-z mid-read", [][]uint16{units("ab" + ctrlZ + "cd")}, "ab" + ctrlZ + "cd"},
		{"ctrl-z twice in a row", [][]uint16{units(ctrlZ + ctrlZ)}, ctrlZ + ctrlZ},
		{"vt key sequences", [][]uint16{units("\x1b[A\x1b[3~\x7f")}, "\x1b[A\x1b[3~\x7f"},
		{"non-ASCII", [][]uint16{units("é日本")}, "é日本"},
		{"surrogate pair in one read", [][]uint16{units("😀")}, "😀"},
		{"surrogate pair split across reads", [][]uint16{{0xd83d}, {0xde00, 'x'}}, "😀x"},
		{"lone high surrogate before a key", [][]uint16{{0xd83d}, {'x'}}, "�x"},
		{"lone low surrogate", [][]uint16{{0xde00, 'x'}}, "�x"},
	}
	for _, tc := range cases {
		for _, size := range []int{1, 2, 64} {
			r := &consoleReader{read: fakeConsole(t, tc.chunks...)}
			got, err := readAll(t, r, size)
			if !errors.Is(err, io.EOF) {
				t.Fatalf("%s (reads of %d): err = %v, want io.EOF once the console is drained", tc.name, size, err)
			}
			if got != tc.want {
				t.Fatalf("%s (reads of %d): got %q, want %q", tc.name, size, got, tc.want)
			}
		}
	}
}

// TestConsoleReaderEmptyReadIsEOF pins the one case that does end input: a
// ReadConsoleW that succeeds with no characters. It must not be mistaken for a
// Ctrl+Z, or a dead console would flood the pod with them.
func TestConsoleReaderEmptyReadIsEOF(t *testing.T) {
	calls := 0
	r := &consoleReader{read: func([]uint16) (int, error) { calls++; return 0, nil }}
	buf := make([]byte, 8)
	for range 3 {
		if n, err := r.Read(buf); n != 0 || !errors.Is(err, io.EOF) {
			t.Fatalf("Read = (%d, %v), want (0, io.EOF)", n, err)
		}
	}
	if calls != 3 {
		t.Fatalf("ReadConsoleW called %d times, want one per Read", calls)
	}
}

func TestConsoleReaderPassesErrorsThrough(t *testing.T) {
	boom := errors.New("console gone")
	r := &consoleReader{read: func([]uint16) (int, error) { return 0, boom }}
	if n, err := r.Read(make([]byte, 8)); n != 0 || !errors.Is(err, boom) {
		t.Fatalf("Read = (%d, %v), want (0, %v)", n, err, boom)
	}
}

func TestConsoleReaderZeroLengthRead(t *testing.T) {
	r := &consoleReader{read: func([]uint16) (int, error) {
		t.Fatal("a zero-length Read must not touch the console")
		return 0, nil
	}}
	if n, err := r.Read(nil); n != 0 || err != nil {
		t.Fatalf("Read(nil) = (%d, %v), want (0, nil)", n, err)
	}
}
