//go:build !windows

package execshell

import (
	"io"
	"os"
	"testing"
	"time"
)

// TestSessionStdinLeavesTheNextKeyForTheTUI drives the real unix reader over a
// pipe. After stop the read client-go left parked is gone, so a key typed
// afterwards is still there for whoever reads the descriptor next, which in
// the app is Bubble Tea. Before the fix that parked read took it.
func TestSessionStdinLeavesTheNextKeyForTheTUI(t *testing.T) {
	r, w, err := os.Pipe()
	if err != nil {
		t.Fatal(err)
	}
	defer r.Close()
	defer w.Close()

	s := newSessionStdin(r)
	if s.cancel == nil {
		t.Fatal("a descriptor should get a cancelable reader")
	}
	got := make(chan readReturn, 1)
	go func() {
		n, err := s.Read(make([]byte, 8))
		got <- readReturn{n, err}
	}()
	time.Sleep(50 * time.Millisecond) // let the read park

	s.stop()
	if res := await(t, got); res.n != 0 || res.err != io.EOF {
		t.Fatalf("parked Read = (%d, %v), want (0, io.EOF)", res.n, res.err)
	}

	if _, err := w.Write([]byte("j")); err != nil {
		t.Fatal(err)
	}
	next := make(chan string, 1)
	go func() {
		buf := make([]byte, 1)
		n, _ := r.Read(buf)
		next <- string(buf[:n])
	}()
	select {
	case key := <-next:
		if key != "j" {
			t.Fatalf("next reader got %q, want %q", key, "j")
		}
	case <-time.After(5 * time.Second):
		t.Fatal("the key never reached the next reader: the stopped session still took it")
	}
}
