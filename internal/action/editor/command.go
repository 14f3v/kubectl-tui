package editor

import "strings"

// editorEnvs are the variables that name the editor, in the order kubectl reads
// them (pkg/cmd/util/editor): KUBE_EDITOR first, so kubetui and `kubectl edit`
// can share one editor that differs from the EDITOR the rest of the shell uses.
var editorEnvs = []string{"KUBE_EDITOR", "EDITOR"}

// defaultEditor is the editor used when no variable names one. Windows ships no
// vi, so there it is notepad — the same fallback kubectl uses.
func defaultEditor(goos string) string {
	if goos == "windows" {
		return "notepad"
	}
	return "vi"
}

// command picks the editor's program and arguments from the environment (read
// through getenv) for the platform goos: KUBE_EDITOR, then EDITOR, then the
// platform default. A variable that is unset or holds only whitespace is passed
// over, not run. A stray space is easy to leave behind — cmd.exe's `set EDITOR= `
// keeps it — and it must neither crash the split nor hide a usable EDITOR behind
// a blank KUBE_EDITOR. The result always names a program.
func command(getenv func(string) string, goos string) []string {
	for _, env := range editorEnvs {
		if argv := strings.Fields(getenv(env)); len(argv) > 0 {
			return argv
		}
	}
	return []string{defaultEditor(goos)}
}
