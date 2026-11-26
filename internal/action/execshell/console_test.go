package execshell

import (
	"bytes"
	"io"
	"os"
	"testing"
)

// TestEnableVTOutputIgnoresNonConsoles pins that the console-mode helper leaves
// anything that is not a console alone (a buffer, or a file as when stdout is
// redirected) and always hands back a restore func that is safe to call. It runs
// on every shell launch, so a nil restore or a panic would take the shell down
// with it. On Windows this exercises the GetConsoleMode-fails path; elsewhere the
// helper is a no-op by construction.
func TestEnableVTOutputIgnoresNonConsoles(t *testing.T) {
	f, err := os.CreateTemp(t.TempDir(), "out")
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
}
