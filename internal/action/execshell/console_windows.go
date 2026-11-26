//go:build windows

package execshell

import (
	"io"
	"os"

	"golang.org/x/sys/windows"
)

// bannerNewline ends the "connecting" line. enableVTOutput turns on
// DISABLE_NEWLINE_AUTO_RETURN before the banner is written, and under it a bare
// LF moves down without returning the carriage, so the shell's first prompt
// would start in the middle of the next line.
const bannerNewline = "\r\n"

// enableVTOutput turns on VT processing on the console for the shell session
// and returns a func that puts the original mode back.
//
// Bubble Tea enables VT output for its own frames, but before Run it restores
// the mode it found at startup (bubbletea tty.go restoreInput). Under classic
// conhost that mode has no ENABLE_VIRTUAL_TERMINAL_PROCESSING, so the banner's
// clear-screen and every escape the pod sends (prompt colours, vim, top) would
// print as literal "←[2J" text. kubectl gets this from moby's StdStreams, which
// nothing in kubetui calls.
//
// DISABLE_NEWLINE_AUTO_RETURN makes the console behave like the xterm the pod's
// TERM claims to be: LF is a pure line feed, so a raw-mode program that moves
// the cursor down keeps its column, and wrapping at the last column is
// deferred. Bubble Tea sets the same pair for its own frames. A console that
// rejects the flag still gets VT processing on its own.
//
// The original mode must be back before Run returns. Bubble Tea's
// RestoreTerminal saves the output mode again as the one to restore at exit
// (bubbletea tty_windows.go initInput), so leaving VT on here would leak it into
// the parent shell when kubetui quits.
func enableVTOutput(out io.Writer) (restore func()) {
	f, ok := out.(*os.File)
	if !ok {
		return func() {}
	}
	h := windows.Handle(f.Fd())
	var mode uint32
	if windows.GetConsoleMode(h, &mode) != nil {
		return func() {} // redirected output: there are no modes to set
	}
	vt := mode | windows.ENABLE_VIRTUAL_TERMINAL_PROCESSING
	if windows.SetConsoleMode(h, vt|windows.DISABLE_NEWLINE_AUTO_RETURN) != nil &&
		windows.SetConsoleMode(h, vt) != nil {
		return func() {}
	}
	return func() { _ = windows.SetConsoleMode(h, mode) }
}
