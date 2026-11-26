package view

import (
	"slices"
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/action/cp"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func TestSplitPaths(t *testing.T) {
	cases := []struct {
		in      string
		want    []string
		wantErr bool
	}{
		{in: `C:\Users\me\a.txt /tmp/a`, want: []string{`C:\Users\me\a.txt`, "/tmp/a"}},
		{in: `"C:\Users\First Last\out" /tmp/out`, want: []string{`C:\Users\First Last\out`, "/tmp/out"}},
		{in: `/var/log/app.log "C:\Users\First Last\out"`, want: []string{"/var/log/app.log", `C:\Users\First Last\out`}},
		// Explorer's "Copy as path" quotes even a path without spaces.
		{in: `"C:\x\a.txt" /tmp/a.txt`, want: []string{`C:\x\a.txt`, "/tmp/a.txt"}},
		{in: `"C:\Users\x\OneDrive - Contoso\Desktop\a.yaml" /tmp/a.yaml`, want: []string{`C:\Users\x\OneDrive - Contoso\Desktop\a.yaml`, "/tmp/a.yaml"}},
		{in: `"\\srv\share\a b.txt" /tmp/a`, want: []string{`\\srv\share\a b.txt`, "/tmp/a"}},
		// A quoted span inside a path, as cmd.exe allows.
		{in: `C:\"My Files"\a.txt /tmp/a`, want: []string{`C:\My Files\a.txt`, "/tmp/a"}},
		// Apostrophes are ordinary characters.
		{in: `C:\Users\O'Brien\a.txt /tmp/a`, want: []string{`C:\Users\O'Brien\a.txt`, "/tmp/a"}},
		// A container path with spaces can be quoted too.
		{in: `"/data/my file.txt" ./out`, want: []string{"/data/my file.txt", "./out"}},
		{in: "  a\t\tb  ", want: []string{"a", "b"}},
		{in: `"" /tmp/a`, want: []string{"", "/tmp/a"}},
		{in: "", want: nil},
		{in: `C:\Users\John Doe\a.txt /tmp/a`, want: []string{`C:\Users\John`, `Doe\a.txt`, "/tmp/a"}},
		{in: `"C:\x /tmp/a`, wantErr: true},
	}
	for _, c := range cases {
		got, err := splitPaths(c.in)
		if c.wantErr {
			if err == nil {
				t.Errorf("splitPaths(%q) = %q, want an error", c.in, got)
			}
			continue
		}
		if err != nil || !slices.Equal(got, c.want) {
			t.Errorf("splitPaths(%q) = %q, %v; want %q", c.in, got, err, c.want)
		}
	}
}

func TestTwoPaths(t *testing.T) {
	a, b, err := twoPaths(`"C:\Users\First Last\a.txt" /tmp/a`, "localPath remotePath")
	if err != nil || a != `C:\Users\First Last\a.txt` || b != "/tmp/a" {
		t.Errorf("twoPaths = %q, %q, %v", a, b, err)
	}
	for _, in := range []string{
		"",
		"/tmp/a",
		"a b c",
		`C:\Users\John Doe\a.txt /tmp/a`, // unquoted space: three fields
		`"" /tmp/a`,
		`"C:\x /tmp/a`,
	} {
		if _, _, err := twoPaths(in, "localPath remotePath"); err == nil || !strings.Contains(err.Error(), "localPath remotePath") {
			t.Errorf("twoPaths(%q) err = %v, want one naming the expected form", in, err)
		}
	}
}

// TestCpPromptsValidate checks both prompts refuse a line that does not parse up
// front, which keeps the prompt open with the text to fix instead of closing it
// with an error toast.
func TestCpPromptsValidate(t *testing.T) {
	p := newCpMenuPage(nil, style.Default(), "demo", "web", "app")
	for name, cmd := range map[string]func() PromptRequest{
		"upload":   func() PromptRequest { return p.upload()().(PromptRequest) },
		"download": func() PromptRequest { return p.download()().(PromptRequest) },
	} {
		req := cmd()
		if req.Validate == nil {
			t.Fatalf("%s prompt has no Validate", name)
		}
		if err := req.Validate(`C:\Users\John Doe\a.txt /tmp/a`); err == nil {
			t.Errorf("%s accepted an unquoted path with a space", name)
		}
		if err := req.Validate(`"C:\Users\John Doe\a.txt" /tmp/a`); err != nil {
			t.Errorf("%s rejected a quoted path: %v", name, err)
		}
	}
}

func TestDownloadToast(t *testing.T) {
	plain := downloadToast("web", cp.Result{})
	if plain.Text != "downloaded from web" || plain.Level != msg.LevelSuccess {
		t.Errorf("plain download toast = %+v", plain)
	}
	renamed := downloadToast("web", cp.Result{Renamed: 3})
	if !strings.Contains(renamed.Text, "3 renamed") || renamed.Level != msg.LevelSuccess {
		t.Errorf("renamed download toast = %+v, want it to count the renames", renamed)
	}
	clash := downloadToast("web", cp.Result{Renamed: 1, Overwritten: []string{"src/makefile", "src/a_b"}})
	if !strings.Contains(clash.Text, "2 overwritten") || !strings.Contains(clash.Text, "src/makefile") || clash.Level != msg.LevelWarn {
		t.Errorf("clash download toast = %+v, want a warning naming the first clash", clash)
	}
	if i, j := strings.Index(clash.Text, "overwritten"), strings.Index(clash.Text, "renamed"); i < 0 || j < 0 || i > j {
		t.Errorf("clash download toast = %q, want the loss before the renames", clash.Text)
	}
}

func TestUploadToast(t *testing.T) {
	plain := uploadToast("web", cp.Result{})
	if plain.Text != "uploaded to web" || plain.Level != msg.LevelSuccess {
		t.Errorf("plain upload toast = %+v", plain)
	}
	one := uploadToast("web", cp.Result{Skipped: []string{"link"}})
	if one.Text != "uploaded to web · skipped 1 (not a regular file): link" || one.Level != msg.LevelWarn {
		t.Errorf("one-skip upload toast = %+v", one)
	}
	many := uploadToast("web", cp.Result{Skipped: []string{"a", "b", "c", "d", "e"}})
	if !strings.Contains(many.Text, "skipped 5") || !strings.HasSuffix(many.Text, "a, b, c, …") {
		t.Errorf("many-skip upload toast = %q, want the count and the first three names", many.Text)
	}
}
