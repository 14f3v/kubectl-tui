package execshell

import (
	"io"
	"sync"
	"time"
	"unicode/utf16"
	"unicode/utf8"
)

// sessionStdin is the pod's stdin for one session. client-go copies it to the
// pod from a goroutine that StreamWithContext never waits for (remotecommand
// v2.go copyStdin), so when the shell exits that goroutine is still parked in
// Read. Bubble Tea takes the terminal back at once, and the first key pressed
// in the TUI then goes to the parked read instead: it is lost, and writing it
// to the closed stream logs "Copying stdin failed" over the screen. That
// happens on every platform, not only Windows.
//
// stop retires the reader as soon as the stream returns. It interrupts a
// parked read and waits for it, so the key stays in the terminal for Bubble
// Tea, and from then on every Read reports io.EOF, so client-go's copy ends
// without logging anything.
type sessionStdin struct {
	r      io.Reader
	cancel func()       // interrupts a Read blocked in r; nil if nothing can
	close  func() error // releases what r holds once no Read is using it; may be nil

	mu      sync.Mutex
	stopped bool
	reading chan struct{} // closed when the Read in flight returns; nil when there is none
}

func (s *sessionStdin) Read(p []byte) (int, error) {
	s.mu.Lock()
	if s.stopped {
		s.mu.Unlock()
		return 0, io.EOF
	}
	done := make(chan struct{})
	s.reading = done
	s.mu.Unlock()

	n, err := s.r.Read(p)

	s.mu.Lock()
	defer s.mu.Unlock()
	s.reading = nil
	close(done)
	if s.stopped {
		// The session is over. Whatever this read brought back, a cancellation
		// or a key that raced the stop, has nowhere left to go.
		return 0, io.EOF
	}
	return n, err
}

// stopWait bounds how long stop waits for a parked read to give up. An
// interrupted read returns at once; one that cannot be interrupted is left
// behind, as before, rather than hold up the return to the TUI.
const stopWait = 100 * time.Millisecond

// stop ends the session's input. See sessionStdin.
func (s *sessionStdin) stop() {
	s.mu.Lock()
	s.stopped = true
	reading := s.reading
	s.mu.Unlock()
	if reading != nil && !s.interrupt(reading) {
		return // still parked in r, so r has to stay open under it
	}
	if s.close != nil {
		_ = s.close()
	}
}

// interrupt cancels the parked read and reports whether it returned within
// stopWait. The cancel is repeated because the read may not have reached its
// blocking call yet, and a cancel that gets there first can find nothing to
// interrupt.
func (s *sessionStdin) interrupt(reading <-chan struct{}) bool {
	if s.cancel == nil {
		return false
	}
	deadline := time.NewTimer(stopWait)
	defer deadline.Stop()
	retry := time.NewTicker(stopWait / 10)
	defer retry.Stop()
	for {
		s.cancel()
		select {
		case <-reading:
			return true
		case <-deadline.C:
			return false
		case <-retry.C:
		}
	}
}

// consoleReader reads keystrokes from a Windows console as UTF-8, the way
// os.File.Read does, except that Ctrl+Z is passed on as the key it is.
//
// Go's console read (internal/poll fd_windows.go readConsole) treats 0x1A as
// end of input. When it is first in line the read consumes it and returns
// (0, nil), which the os package reports as io.EOF; when it is mid-buffer that
// read just stops short and the next one is the EOF. client-go's stdin copy
// ends on that EOF and closes the pod's stdin for good (remotecommand v2.go
// copyStdin), so a single Ctrl+Z, to suspend vim or out of the habit of undo,
// left the shell deaf for the rest of the session. A raw-mode console has no
// end of input of its own: Ctrl+Z is just 0x1A, which the pod's tty turns
// into SIGTSTP.
//
// Mapping os.File.Read's EOF back to 0x1A is not enough. A ReadConsoleW that
// succeeds with no characters, a case Go's loop allows for, comes out as the
// same EOF, and turning that into 0x1A would send the pod a Ctrl+Z nobody
// typed, or an endless stream of them from a console that keeps returning
// nothing. So this calls ReadConsoleW itself and does the UTF-16 decoding Go
// would have done.
type consoleReader struct {
	// read is ReadConsoleW: it fills buf with UTF-16 code units and returns how
	// many it wrote.
	read func(buf []uint16) (int, error)

	units   [consoleReadUnits]uint16
	held    int    // 1 while units[0] is a high surrogate whose other half is still unread
	decoded []byte // backing store for pending
	pending []byte // decoded input not yet returned
}

// consoleReadUnits bounds one ReadConsoleW. Large reads fail somewhere near 16K
// units (fd_windows.go readConsole), and a keystroke or a paste chunk fits in
// far less.
const consoleReadUnits = 1024

func (r *consoleReader) Read(p []byte) (int, error) {
	if len(p) == 0 {
		return 0, nil
	}
	for len(r.pending) == 0 {
		n, err := r.read(r.units[r.held:])
		if err != nil {
			return 0, err
		}
		if n == 0 {
			// The console ended the read with nothing in it: the only end of
			// input a raw console has (see above).
			return 0, io.EOF
		}
		units := r.units[:r.held+n]
		var carry uint16
		if last := units[len(units)-1]; 0xd800 <= last && last < 0xdc00 {
			// A high surrogate whose low half has not arrived yet: hold it for the
			// next read rather than decode half a character.
			carry, units = last, units[:len(units)-1]
		}
		r.decoded = r.decoded[:0]
		for _, c := range utf16.Decode(units) {
			r.decoded = utf8.AppendRune(r.decoded, c)
		}
		r.pending = r.decoded
		r.held = 0
		if carry != 0 {
			r.units[0], r.held = carry, 1
		}
	}
	n := copy(p, r.pending)
	r.pending = r.pending[n:]
	return n, nil
}
