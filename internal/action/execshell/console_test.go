package execshell

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestConsoleHelpersIgnoreNonConsoles pins that the console-mode helpers leave
// anything that is not a console alone (a buffer, or a file as when stdin or
// stdout is redirected) and that enableVTOutput always hands back a restore func
// that is safe to call. They run on every shell launch, so a nil restore or a
// panic would take the shell down with it. On Windows this exercises the
// GetConsoleMode-fails path; elsewhere the helpers are no-ops by construction.
func TestConsoleHelpersIgnoreNonConsoles(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "stream")
	if err != nil {
		t.Fatal(err)
	}
	defer f.Close()

	for name, w := range map[string]io.Writer{"buffer": &bytes.Buffer{}, "file": f} {
		restore := enableVTOutput(w)
		if restore == nil {
			t.Fatalf("%s: enableVTOutput returned a nil restore func", name)
		}
		restore()
	}
	for _, r := range []io.Reader{&bytes.Buffer{}, f} {
		enableVTInput(r)
	}
}
