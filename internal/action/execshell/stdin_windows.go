//go:build windows

package execshell

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// newSessionStdin wraps the input the pod's stdin is copied from. A console is
// read through consoleReader so that Ctrl+Z reaches the pod instead of ending
// its input (see stdin.go). TTY.In stays the console's *os.File itself, so
// tty.Safe still recognises it and puts it in raw mode. Anything else, such as
// a pipe or a file, keeps os.File's reads and its real end of input, and
// cannot be interrupted.
func newSessionStdin(in io.Reader) *sessionStdin {
	f, ok := in.(*os.File)
	if !ok {
		return &sessionStdin{r: in}
	}
	var mode uint32
	if windows.GetConsoleMode(windows.Handle(f.Fd()), &mode) != nil {
		return &sessionStdin{r: in}
	}
	return &sessionStdin{
		r: &consoleReader{read: func(buf []uint16) (int, error) {
			var n uint32
			err := windows.ReadConsole(windows.Handle(f.Fd()), &buf[0], uint32(len(buf)), &n, nil)
			return int(n), err
		}},
		// CancelIoEx on the console handle is the call Bubble Tea's own console
		// reader relies on to end its synchronous read (ultraviolet
		// cancelreader_windows.go). Should it not end this ReadConsoleW, stop
		// gives up after stopWait and the parked read costs one key, as it always
		// has, but still writes nothing to the closed stream.
		cancel: func() { _ = windows.CancelIoEx(windows.Handle(f.Fd()), nil) },
	}
}
