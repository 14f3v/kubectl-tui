//go:build !windows

package execshell

import (
	"io"

	"github.com/muesli/cancelreader"
)

// newSessionStdin wraps the input the pod's stdin is copied from in the same
// cancelable reader Bubble Tea reads its own input with (kqueue, epoll or
// select on the descriptor plus a wake-up pipe), so stop can interrupt the
// read client-go leaves parked when the shell exits. The bytes are those of
// the input itself: a raw unix tty delivers Ctrl+Z as 0x1A and nothing reads
// it as end of input, so none of the Windows console handling applies.
func newSessionStdin(in io.Reader) *sessionStdin {
	if _, ok := in.(cancelreader.File); !ok {
		return &sessionStdin{r: in} // no descriptor to wait on
	}
	cr, err := cancelreader.NewReader(in)
	if err != nil {
		return &sessionStdin{r: in} // e.g. epoll refuses a regular file: read it as before
	}
	return &sessionStdin{r: cr, cancel: func() { cr.Cancel() }, close: cr.Close}
}
