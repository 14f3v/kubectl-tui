//go:build !windows

package execshell

import "io"

// streamStdin returns the reader the pod's stdin is copied from. Off Windows
// that is the input itself: a raw unix tty delivers Ctrl+Z as the byte 0x1A
// and nothing reads it as end of input. See stdin_windows.go.
func streamStdin(in io.Reader) io.Reader { return in }
