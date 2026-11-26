package view

import (
	"os"
	"path/filepath"
	"runtime"
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/msg"
)

func TestDefaultLogPath(t *testing.T) {
	tmp := filepath.Join("C", "Users", "me", "AppData", "Local", "Temp")
	cases := []struct {
		name, goos, want string
	}{
		{"web-0", "linux", "/tmp/web-0.log"},
		{"logs", "darwin", "/tmp/logs.log"},
		{"con", "linux", "/tmp/con.log"},
		{"web-0", "windows", filepath.Join(tmp, "web-0.log")},
		{"logs", "windows", filepath.Join(tmp, "logs.log")},
		// Reserved device names, even with the extension on Windows 10.
		{"con", "windows", filepath.Join(tmp, "_con.log")},
		{"NUL", "windows", filepath.Join(tmp, "_NUL.log")},
		{"com1", "windows", filepath.Join(tmp, "_com1.log")},
		// Pod names cannot hold these, but the name is sanitized all the same.
		{"a:b", "windows", filepath.Join(tmp, "a_b.log")},
	}
	for _, c := range cases {
		if got := defaultLogPath(c.name, c.goos, tmp); got != c.want {
			t.Errorf("defaultLogPath(%q, %s) = %q, want %q", c.name, c.goos, got, c.want)
		}
	}
}

func TestUnquotePath(t *testing.T) {
	cases := map[string]string{
		`"C:\Users\me\web.log"`:       `C:\Users\me\web.log`,
		`  "C:\My Files\web.log"  `:   `C:\My Files\web.log`,
		`"/tmp/with space.log"`:       "/tmp/with space.log",
		"  /tmp/web.log ":             "/tmp/web.log",
		`C:\Users\O'Brien\web.log`:    `C:\Users\O'Brien\web.log`,
		`"`:                           `"`,
		`""`:                          "",
		`"half`:                       `"half`,
		`C:\"quoted"\in the middle`:   `C:\"quoted"\in the middle`,
		"\t\"C:\\tab\\web.log\"\t":    `C:\tab\web.log`,
		`'C:\single\quoted\web.log'`:  `'C:\single\quoted\web.log'`,
		`"C:\Users\me\web.log"` + " ": `C:\Users\me\web.log`,
	}
	for in, want := range cases {
		if got := unquotePath(in); got != want {
			t.Errorf("unquotePath(%q) = %q, want %q", in, got, want)
		}
	}
}

// TestSaveActionWritesQuotedPath drives the save prompt end to end: it suggests
// the platform default, and a path entered with Explorer's quotes is written
// without them and reported as such.
func TestSaveActionWritesQuotedPath(t *testing.T) {
	p := &logsPage{pod: "web-0", lines: []string{"one", "two"}}
	req, ok := p.saveAction()().(PromptRequest)
	if !ok {
		t.Fatal("saveAction did not ask for a path")
	}
	if want := defaultLogPath("web-0", runtime.GOOS, os.TempDir()); req.Initial != want {
		t.Errorf("suggested %q, want %q", req.Initial, want)
	}

	dst := filepath.Join(t.TempDir(), "with space", "web.log")
	if err := os.MkdirAll(filepath.Dir(dst), 0o755); err != nil {
		t.Fatal(err)
	}
	toast, ok := req.Action(`"` + dst + `"`).(msg.Toast)
	if !ok || toast.Level != msg.LevelSuccess {
		t.Fatalf("save toast = %+v, want success", toast)
	}
	if !strings.HasSuffix(toast.Text, dst) {
		t.Errorf("save toast = %q, want it to name %q without quotes", toast.Text, dst)
	}
	data, err := os.ReadFile(dst)
	if err != nil {
		t.Fatal(err)
	}
	if string(data) != "one\ntwo\n" {
		t.Errorf("saved %q, want the two lines", data)
	}
}
