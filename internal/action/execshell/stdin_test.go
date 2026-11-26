package execshell

import (
	"errors"
	"io"
	"testing"
	"unicode/utf16"
)

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
