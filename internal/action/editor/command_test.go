package editor

import (
	"path/filepath"
	"reflect"
	"runtime"
	"testing"
)

// envOf is a getenv over a fixed set of variables; anything else reads as unset.
func envOf(vars map[string]string) func(string) string {
	return func(k string) string { return vars[k] }
}

func TestCommandChoosesEditor(t *testing.T) {
	cases := []struct {
		name string
		vars map[string]string
		goos string
		want []string
	}{
		{name: "unset on linux", goos: "linux", want: []string{"vi"}},
		{name: "unset on darwin", goos: "darwin", want: []string{"vi"}},
		{name: "unset on windows", goos: "windows", want: []string{"notepad"}},
		{name: "KUBE_EDITOR wins", vars: map[string]string{"KUBE_EDITOR": "nano", "EDITOR": "vim"}, goos: "linux", want: []string{"nano"}},
		{name: "EDITOR alone", vars: map[string]string{"EDITOR": "code --wait"}, goos: "windows", want: []string{"code", "--wait"}},
		{name: "blank KUBE_EDITOR falls through to EDITOR", vars: map[string]string{"KUBE_EDITOR": "  ", "EDITOR": "nano"}, goos: "linux", want: []string{"nano"}},
		{name: "both blank use the default", vars: map[string]string{"KUBE_EDITOR": " \t", "EDITOR": " "}, goos: "windows", want: []string{"notepad"}},
		{name: "surrounding whitespace dropped", vars: map[string]string{"EDITOR": "  vim  "}, goos: "linux", want: []string{"vim"}},
	}
	for _, c := range cases {
		t.Run(c.name, func(t *testing.T) {
			if got := command(envOf(c.vars), c.goos); !reflect.DeepEqual(got, c.want) {
				t.Errorf("command(%v, %q) = %q, want %q", c.vars, c.goos, got, c.want)
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
