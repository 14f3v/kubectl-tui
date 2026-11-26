package editor

import (
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// envOf is a getenv over a fixed set of variables; anything else reads as unset.
func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

// noProgram is an isProgram for which no value is an executable, so command
// always goes on to split.
func noProgram(string) bool { return false }

const notepadPP = `C:\Program Files\Notepad++\notepad++.exe`

func TestCommandChoosesEditor(t *testing.T) {
	cases := []struct {
		name      string
		vars      map[string]string
		goos      string
		isProgram func(string) bool
		want      []string
	}{
		{name: "unset on linux", goos: "linux", want: []string{"vi"}},
		{name: "unset on darwin", goos: "darwin", want: []string{"vi"}},
		{name: "unset on windows", goos: "windows", want: []string{"notepad"}},
		{name: "KUBE_EDITOR wins", vars: map[string]string{"KUBE_EDITOR": "nano", "EDITOR": "vim"}, goos: "linux", want: []string{"nano"}},
		{name: "EDITOR alone", vars: map[string]string{"EDITOR": "code --wait"}, goos: "windows", want: []string{"code", "--wait"}},
		{name: "blank KUBE_EDITOR falls through to EDITOR", vars: map[string]string{"KUBE_EDITOR": "  ", "EDITOR": "nano"}, goos: "linux", want: []string{"nano"}},
		{name: "both blank use the default", vars: map[string]string{"KUBE_EDITOR": " \t", "EDITOR": " "}, goos: "windows", want: []string{"notepad"}},
		{name: "surrounding whitespace dropped", vars: map[string]string{"EDITOR": "  vim  "}, goos: "linux", want: []string{"vim"}},
		{name: "empty quotes name no program", vars: map[string]string{"KUBE_EDITOR": `""`, "EDITOR": "nano"}, goos: "windows", want: []string{"nano"}},
		{name: "quoted blank names no program", vars: map[string]string{"EDITOR": `"  " -w`}, goos: "linux", want: []string{"vi"}},
		{
			name: "quoted program path on windows",
			vars: map[string]string{"EDITOR": `"` + notepadPP + `" -multiInst -nosession`}, goos: "windows",
			want: []string{notepadPP, "-multiInst", "-nosession"},
		},
		{
			name: "unquoted program path taken whole when it resolves",
			vars: map[string]string{"EDITOR": notepadPP}, goos: "windows",
			isProgram: func(p string) bool { return p == notepadPP },
			want:      []string{notepadPP},
		},
		{
			name: "unquoted path that does not resolve is split",
			vars: map[string]string{"EDITOR": notepadPP + " -multiInst"}, goos: "windows",
			isProgram: func(p string) bool { return p == notepadPP },
			want:      []string{`C:\Program`, `Files\Notepad++\notepad++.exe`, "-multiInst"},
		},
		{
			// Were it taken whole, the quotes would stay in the program name.
			name: "a value without spaces is never taken whole",
			vars: map[string]string{"EDITOR": `'vim'`}, goos: "linux",
			isProgram: func(string) bool { return true },
			want:      []string{"vim"},
		},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			isProgram := c.isProgram
			if isProgram == nil {
				isProgram = noProgram
			}
			if got := command(envOf(c.vars), c.goos, isProgram); !reflect.DeepEqual(got, c.want) {
				t.Errorf("command(%q, %q) = %q, want %q", c.vars, c.goos, got, c.want)
			}
		})
	}
}

func TestSplitCommand(t *testing.T) {
	cases := []struct {
		name    string
		in      string
		unix    []string // with backslash escapes
		windows []string // backslash is a plain character
	}{
		{name: "bare program", in: "vi", unix: []string{"vi"}, windows: []string{"vi"}},
		{name: "program and flag", in: "code --wait", unix: []string{"code", "--wait"}, windows: []string{"code", "--wait"}},
		{name: "runs of whitespace", in: " nano \t -w  ", unix: []string{"nano", "-w"}, windows: []string{"nano", "-w"}},
		{name: "blank", in: "  ", unix: nil, windows: nil},
		{name: "empty", in: "", unix: nil, windows: nil},
		{
			name:    "double-quoted path with spaces",
			in:      `"` + notepadPP + `" -multiInst -nosession`,
			unix:    []string{notepadPP, "-multiInst", "-nosession"}, // \P is no sh escape
			windows: []string{notepadPP, "-multiInst", "-nosession"},
		},
		{
			name:    "single-quoted path with spaces",
			in:      `'C:\Program Files\Sublime Text\subl.exe' -w`,
			unix:    []string{`C:\Program Files\Sublime Text\subl.exe`, "-w"},
			windows: []string{`C:\Program Files\Sublime Text\subl.exe`, "-w"},
		},
		{name: "single quotes group the words", in: `'subl -w'`, unix: []string{"subl -w"}, windows: []string{"subl -w"}},
		{name: "quoted argument", in: `vim -c "set ft=yaml"`, unix: []string{"vim", "-c", "set ft=yaml"}, windows: []string{"vim", "-c", "set ft=yaml"}},
		{name: "empty single quotes are an argument", in: `emacsclient -a '' -t`, unix: []string{"emacsclient", "-a", "", "-t"}, windows: []string{"emacsclient", "-a", "", "-t"}},
		{name: "empty double quotes are an argument", in: `ed "" x`, unix: []string{"ed", "", "x"}, windows: []string{"ed", "", "x"}},
		{name: "only empty quotes", in: `""`, unix: []string{""}, windows: []string{""}},
		{name: "adjacent pieces join", in: `a"b c"'d e'f`, unix: []string{"ab cd ef"}, windows: []string{"ab cd ef"}},
		{name: "unterminated double quote is literal", in: `vim "two words`, unix: []string{"vim", `"two`, "words"}, windows: []string{"vim", `"two`, "words"}},
		{name: "unterminated single quote is literal", in: `vim 'it`, unix: []string{"vim", "'it"}, windows: []string{"vim", "'it"}},
		{name: "lone quote", in: `"`, unix: []string{`"`}, windows: []string{`"`}},
		{name: "apostrophe in a path", in: `C:\Users\O'Brien\vim.exe -f`, unix: []string{`C:UsersO'Brienvim.exe`, "-f"}, windows: []string{`C:\Users\O'Brien\vim.exe`, "-f"}},
		{name: "escaped space", in: `/opt/My\ Editor/bin/ed --wait`, unix: []string{"/opt/My Editor/bin/ed", "--wait"}, windows: []string{`/opt/My\`, "Editor/bin/ed", "--wait"}},
		{name: "trailing backslash", in: `ed \`, unix: []string{"ed", `\`}, windows: []string{"ed", `\`}},
		{name: "backslash before the closing quote", in: `"C:\dir\" -f`, unix: []string{`"C:dir"`, "-f"}, windows: []string{`C:\dir\`, "-f"}},
		{name: "escaped quote inside double quotes", in: `ed "a\"b"`, unix: []string{"ed", `a"b`}, windows: []string{"ed", `a\b"`}},
		{name: "sh escapes inside double quotes", in: `ed "\\ \$ \x"`, unix: []string{"ed", `\ $ \x`}, windows: []string{"ed", `\\ \$ \x`}},
		{name: "backslash is literal inside single quotes", in: `ed 'a\b\'`, unix: []string{"ed", `a\b\`}, windows: []string{"ed", `a\b\`}},
		{name: "non-ASCII survives", in: `"/home/jürgen/bin/ed" -ü`, unix: []string{"/home/jürgen/bin/ed", "-ü"}, windows: []string{"/home/jürgen/bin/ed", "-ü"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := splitCommand(c.in, true); !reflect.DeepEqual(got, c.unix) {
				t.Errorf("unix: splitCommand(%q) = %q, want %q", c.in, got, c.unix)
			}
			if got := splitCommand(c.in, false); !reflect.DeepEqual(got, c.windows) {
				t.Errorf("windows: splitCommand(%q) = %q, want %q", c.in, got, c.windows)
			}
		})
	}
}

func TestIsAbsProgram(t *testing.T) {
	dir := t.TempDir()
	// A space in the name is the point; the .cmd extension satisfies Windows'
	// PATHEXT lookup and the mode bits satisfy the unix one.
	prog := filepath.Join(dir, "my editor.cmd")
	if err := os.WriteFile(prog, []byte("@echo off\n"), 0o755); err != nil {
		t.Fatal(err)
	}
	cases := []struct {
		name string
		in   string
		want bool
	}{
		{name: "absolute executable with a space", in: prog, want: true},
		{name: "missing file", in: filepath.Join(dir, "nope.cmd"), want: false},
		{name: "directory", in: dir, want: false},
		{name: "relative name", in: "my editor.cmd", want: false},
		{name: "path plus arguments", in: prog + " --wait", want: false},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := isAbsProgram(c.in); got != c.want {
				t.Errorf("isAbsProgram(%q) = %v, want %v", c.in, got, c.want)
			}
		})
	}
}

// A whitespace-only variable used to reach parts[1:] on an empty split and panic
// inside the tea.Cmd, taking the whole TUI down. Process must build a command
// for the file whatever the variables hold.
func TestProcessAppendsTheFile(t *testing.T) {
	path := filepath.Join(t.TempDir(), "kubetui-1.yaml")
	cases := []struct {
		name       string
		kube, edit string
		want       []string
	}{
		{name: "blank KUBE_EDITOR", kube: " ", edit: "nano -w", want: []string{"nano", "-w", path}},
		{name: "nothing set", want: []string{defaultEditor(runtime.GOOS), path}},
		{name: "only whitespace", kube: "\t", edit: "   ", want: []string{defaultEditor(runtime.GOOS), path}},
		{name: "quoted program", kube: `"my editor" --wait`, want: []string{"my editor", "--wait", path}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			t.Setenv("KUBE_EDITOR", c.kube)
			t.Setenv("EDITOR", c.edit)
			if got := Process(path).Args; !reflect.DeepEqual(got, c.want) {
				t.Errorf("Process(%q).Args = %q, want %q", path, got, c.want)
			}
		})
	}
}
