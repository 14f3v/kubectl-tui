package view

import (
	"os/exec"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/style"
)

// PushMsg asks the root model to push a page onto the stack (drill-in). The root
// intercepts it before routing to the active page.
type PushMsg struct{ Page Page }

// PopMsg asks the root model to pop the top page (back out of a drill-in).
type PopMsg struct{}

// ConfirmRequest asks the root model to show a modal confirm dialog. On confirm
// the root runs Action as a command; on cancel it is discarded. Danger styles the
// dialog for destructive operations.
type ConfirmRequest struct {
	Title  string
	Prompt string
	Danger bool
	Action func() tea.Msg
}

// PromptRequest asks the root model to show a modal single-line text input (e.g.
// the replica count for a scale). On enter the value is validated with Validate
// (nil means always valid); a non-nil error is shown and the prompt stays open.
// On a valid enter the root runs Action(value) as a command and dismisses; esc
// cancels. Initial pre-fills the field.
type PromptRequest struct {
	Title    string
	Label    string
	Initial  string
	Validate func(string) error
	Action   func(string) tea.Msg
}

// ExecRequest asks the root model to hand the terminal to a child process through
// the TerminalGate. Exactly one of Command (an interactive tea.ExecCommand, e.g.
// a pod shell) or Process (an *exec.Cmd, e.g. $EDITOR) is set. After, if present,
// runs once the terminal is restored (e.g. the edit's server-side apply).
type ExecRequest struct {
	Label   string
	Command tea.ExecCommand
	Process *exec.Cmd
	After   func(err error) tea.Msg
}

// TextView is a read-only scrollable text page used for YAML and describe output.
// It renders the visible window of lines; the root chrome fits width and height.
type TextView struct {
	title   string
	theme   style.Theme
	lines   []string
	offset  int
	height  int
	filter  string
	matched []int // line indices matching the active filter
	matchAt int   // index into matched of the current match
}

// NewTextView builds a text page titled title over content.
func NewTextView(title, content string, theme style.Theme) *TextView {
	return &TextView{
		title: title,
		theme: theme,
		lines: strings.Split(content, "\n"),
	}
}

func (v *TextView) Init() tea.Cmd     { return nil }
func (v *TextView) Title() string     { return v.title }
func (v *TextView) Kind() string      { return "" }
func (v *TextView) Namespace() string { return "" }
func (v *TextView) Filter() string    { return v.filter }
func (v *TextView) OnEnter() tea.Cmd  { return nil }
func (v *TextView) OnLeave()          {}
func (v *TextView) Summary() Summary  { return Summary{Total: len(v.lines)} }

func (v *TextView) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "scroll")),
		key.NewBinding(key.WithKeys("g", "G"), key.WithHelp("g/G", "top/bottom")),
		key.NewBinding(key.WithKeys("n", "N"), key.WithHelp("n/N", "next/prev match")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

// SetFilter treats the filter as an in-page search: it jumps to the first
// matching line and records matches for n/N.
func (v *TextView) SetFilter(f string) {
	v.filter = f
	v.matched = v.matched[:0]
	v.matchAt = 0
	if f == "" {
		return
	}
	needle := strings.ToLower(f)
	for i, ln := range v.lines {
		if strings.Contains(strings.ToLower(ln), needle) {
			v.matched = append(v.matched, i)
		}
	}
	if len(v.matched) > 0 {
		v.scrollTo(v.matched[0])
	}
}

func (v *TextView) Update(m tea.Msg) (Page, tea.Cmd) {
	k, ok := m.(tea.KeyPressMsg)
	if !ok {
		return v, nil
	}
	switch k.String() {
	case "j", "down":
		v.scroll(1)
	case "k", "up":
		v.scroll(-1)
	case "pgdown", "ctrl+f":
		v.scroll(v.height)
	case "pgup", "ctrl+b":
		v.scroll(-v.height)
	case "g", "home":
		v.offset = 0
	case "G", "end":
		v.offset = v.maxOffset()
	case "n":
		v.jumpMatch(1)
	case "N":
		v.jumpMatch(-1)
	}
	return v, nil
}

func (v *TextView) View(width, height int) string {
	v.height = height
	if v.offset > v.maxOffset() {
		v.offset = v.maxOffset()
	}
	end := v.offset + height
	if end > len(v.lines) {
		end = len(v.lines)
	}
	visible := v.lines[v.offset:end]
	return strings.Join(visible, "\n")
}

func (v *TextView) scroll(delta int) {
	v.offset += delta
	if v.offset < 0 {
		v.offset = 0
	}
	if v.offset > v.maxOffset() {
		v.offset = v.maxOffset()
	}
}

func (v *TextView) scrollTo(line int) {
	// Center the target line in the viewport when possible.
	v.offset = line - v.height/2
	if v.offset < 0 {
		v.offset = 0
	}
	if v.offset > v.maxOffset() {
		v.offset = v.maxOffset()
	}
}

func (v *TextView) jumpMatch(dir int) {
	if len(v.matched) == 0 {
		return
	}
	v.matchAt = (v.matchAt + dir + len(v.matched)) % len(v.matched)
	v.scrollTo(v.matched[v.matchAt])
}

func (v *TextView) maxOffset() int {
	if v.height <= 0 {
		return 0
	}
	m := len(v.lines) - v.height
	if m < 0 {
		return 0
	}
	return m
}
