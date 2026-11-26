//go:build windows

package execshell

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// streamStdin returns the reader the pod's stdin is copied from. A console is
// read through consoleReader so that Ctrl+Z reaches the pod instead of ending
// its input (see stdin.go). TTY.In stays the console's *os.File itself, so
// tty.Safe still recognises it and puts it in raw mode. Anything else, such as
// a pipe or a file, keeps os.File's reads and its real end of input.
func streamStdin(in io.Reader) io.Reader {
	f, ok := in.(*os.File)
	if !ok {
		return in
	}
	var mode uint32
	if windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) != nil {
		return in
	}
	return &consoleReader{read: func(buf []uint16) (int, error) {
		var n uint32
		err := windows.ReadConsole(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), &n, nil)
		return int(n), err
	}}
}
