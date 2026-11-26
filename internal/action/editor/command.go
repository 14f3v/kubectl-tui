package editor

import (
	"os/exec"
	"path/filepath"
	"strings"
)

// editorEnvs are the variables that name the editor, in the order kubectl reads
// them (pkg/cmd/util/editor): KUBE_EDITOR first, so kubetui and `kubectl edit`
// can share one editor that differs from the EDITOR the rest of the shell uses.
var editorEnvs = []string{"KUBE_EDITOR", "EDITOR"}

// spaces are the characters that separate words in an editor command line: the
// ASCII whitespace that strings.Fields splits on too.
const spaces = " \t\n\v\f\r"

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
// platform default. A variable that is unset, holds only whitespace or names no
// program is passed over, not run. A stray space is easy to leave behind —
// cmd.exe's `set EDITOR= ` keeps it, and `set EDITOR=""` keeps the quotes — and
// it must neither crash the split nor hide a usable EDITOR behind a blank
// KUBE_EDITOR. The result always names a program.
//
// isProgram reports whether a value is the path of an executable; Process passes
// isAbsProgram. It lets an unquoted path with spaces — what
// `$env:EDITOR = "C:\Program Files\Notepad++\notepad++.exe"` leaves behind — run
// as the one program it names instead of being cut at "C:\Program". Only a value
// that resolves whole is taken that way, so "code --wait" still splits.
func command(getenv func(string) string, goos string, isProgram func(string) bool) []string {
	for _, env := range editorEnvs {
		ed := strings.TrimSpace(getenv(env))
		if ed == "" {
			continue
		}
		if strings.ContainsAny(ed, spaces) && isProgram(ed) {
			return []string{ed}
		}
		if argv := splitCommand(ed, goos != "windows"); len(argv) > 0 && strings.TrimSpace(argv[0]) != "" {
			return argv
		}
	}
	return []string{defaultEditor(goos)}
}

// isAbsProgram reports whether p is the absolute path of an executable file. A
// relative value is left to the split, so a bare "vim -f" is never looked up on
// PATH as a single name.
func isAbsProgram(p string) bool {
	if !filepath.IsAbs(p) {
		return false
	}
	_, err := exec.LookPath(p)
	return err == nil
}

// splitCommand splits an editor command line into words the way a shell would,
// without running one. kubectl hands a value with quotes to $SHELL or `cmd /C`
// instead. Splitting it here keeps one set of rules on every platform, lets a
// mistyped editor fail with exec's plain "executable file not found", and stays
// clear of cmd.exe's %VAR% expansion and quoting quirks. The rules are a small
// subset of what users of either kind of shell already know:
//
//   - unquoted whitespace separates words;
//   - single or double quotes group what they enclose into one word and are
//     dropped, so `"C:\Program Files\Notepad++\notepad++.exe" -multiInst` is two
//     words; pieces with no space between them join (a"b c"d is one word), and
//     a pair of quotes with nothing inside is an empty word;
//   - a quote with no partner is an ordinary character, so the apostrophe in
//     C:\Users\O'Brien\vim.exe survives;
//   - with escapes (every platform but Windows), a backslash outside quotes
//     makes the next character literal, and inside double quotes it escapes only
//     " \ $ and `, as in sh; inside single quotes nothing is special. Without
//     escapes (Windows) a backslash is a path separator and nothing else, so
//     "C:\dir\" keeps its trailing backslash.
//
// There is no variable expansion, globbing or other shell syntax.
func splitCommand(s string, escapes bool) []string {
	var (
		words  []string
		word   strings.Builder
		inWord bool // word holds a word, possibly an empty one from ""
	)
	for i := 0; i < len(s); i++ {
		switch c := s[i]; {
		case strings.IndexByte(spaces, c) >= 0:
			if inWord {
				words = append(words, word.String())
				word.Reset()
				inWord = false
			}
		case c == '\\' && escapes && i+1 < len(s):
			i++
			word.WriteByte(s[i])
			inWord = true
		case c == '"' || c == '\'':
			inWord = true
			end := closingQuote(s, i, escapes)
			if end < 0 {
				word.WriteByte(c)
				break
			}
			for j := i + 1; j < end; j++ {
				if c == '"' && escapes && s[j] == '\\' && j+1 < end && strings.IndexByte("\"\\$`", s[j+1]) >= 0 {
					j++
				}
				word.WriteByte(s[j])
			}
			i = end
		default:
			word.WriteByte(c)
			inWord = true
		}
	}
	if inWord {
		words = append(words, word.String())
	}
	return words
}

// closingQuote returns the index of the quote that closes the one at s[open], or
// -1 if nothing closes it. With escapes, a backslash inside double quotes keeps
// the character after it — an escaped " included — from closing the quote.
func closingQuote(s string, open int, escapes bool) int {
	q := s[open]
	for j := open + 1; j < len(s); j++ {
		switch {
		case s[j] == '\\' && q == '"' && escapes:
			j++
		case s[j] == q:
			return j
		}
	}
	return -1
}
