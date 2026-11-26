//go:build !windows

package execshell

import "io"

// bannerNewline ends the "connecting" line. The banner is written while the
// terminal is still in the cooked mode Bubble Tea released it in, where the
// tty's ONLCR already turns LF into CR LF.
const bannerNewline = "\n"

// enableVTOutput does nothing off Windows: a unix terminal interprets escape
// sequences whatever its mode. See console_windows.go.
func enableVTOutput(io.Writer) (restore func()) { return func() {} }
