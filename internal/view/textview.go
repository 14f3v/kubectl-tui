package view

import (
	"fmt"
	"os/exec"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"

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

// SwitchContextRequest asks the root model to switch to another kubeconfig
// context (rebuilding the Session). Emitted by the context picker.
type SwitchContextRequest struct{ Name string }

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
// An active filter (the "/" search) highlights every match in the visible text.
type TextView struct {
	title   string
	theme   style.Theme
	lines   []string
	offset  int
	height  int
	filter  string
	needle  string         // lowercased substring to match, when not a regex
	re      *regexp.Regexp // compiled matcher when the filter starts with "~"
	matched []int          // line indices matching the active filter
	matchAt int            // index into matched of the current match

	matchStyle lipgloss.Style // every match
	curStyle   lipgloss.Style // the match n/N is currently on
}

// NewTextView builds a text page titled title over content.
func NewTextView(title, content string, theme style.Theme) *TextView {
	return &TextView{
		title:      title,
		theme:      theme,
		lines:      strings.Split(content, "\n"),
		matchStyle: lipgloss.NewStyle().Foreground(theme.Pal.Bg).Background(theme.Pal.Warn),
		curStyle:   lipgloss.NewStyle().Foreground(theme.Pal.Bg).Background(theme.Pal.Accent).Bold(true),
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

// SetFilter treats the filter as an in-page search: it records matching lines
// (for n/N and highlighting) and jumps to the first. A leading "~" makes the term
// a case-insensitive regex (mirroring the resource-table filter); otherwise it is
// a case-insensitive substring. An unparseable regex matches nothing.
func (v *TextView) SetFilter(f string) {
	v.filter = f
	v.matched = v.matched[:0]
	v.matchAt = 0
	v.needle = ""
	v.re = nil
	if f == "" {
		return
	}
	if strings.HasPrefix(f, "~") {
		body := f[1:]
		if body == "" {
			return
		}
		re, err := regexp.Compile("(?i)" + body)
		if err != nil {
			return
		}
		v.re = re
	} else {
		v.needle = strings.ToLower(f)
	}
	for i, ln := range v.lines {
		if len(v.matchRanges(ln)) > 0 {
			v.matched = append(v.matched, i)
		}
	}
	if len(v.matched) > 0 {
		v.scrollTo(v.matched[0])
	}
}

// matchRanges returns the [start,end) byte spans of the active filter's matches in
// a line: regex when set, otherwise a case-insensitive substring scan. Zero-width
// matches are skipped so highlighting never renders an empty span.
func (v *TextView) matchRanges(line string) [][2]int {
	if v.re != nil {
		idx := v.re.FindAllStringIndex(line, -1)
		out := make([][2]int, 0, len(idx))
		for _, r := range idx {
			if r[1] > r[0] {
				out = append(out, [2]int{r[0], r[1]})
			}
		}
		return out
	}
	if v.needle == "" {
		return nil
	}
	lower := strings.ToLower(line)
	var out [][2]int
	for from := 0; ; {
		i := strings.Index(lower[from:], v.needle)
		if i < 0 {
			break
		}
		s := from + i
		out = append(out, [2]int{s, s + len(v.needle)})
		from = s + len(v.needle)
	}
	return out
}

// highlight wraps each match span in a highlight style. The current match's line
// uses a brighter style so n/N is easy to track.
func (v *TextView) highlight(line string, isCurrent bool) string {
	ranges := v.matchRanges(line)
	if len(ranges) == 0 {
		return line
	}
	st := v.matchStyle
	if isCurrent {
		st = v.curStyle
	}
	var b strings.Builder
	last := 0
	for _, r := range ranges {
		if r[0] < last {
			continue // defensive against overlaps
		}
		b.WriteString(line[last:r[0]])
		b.WriteString(st.Render(line[r[0]:r[1]]))
		last = r[1]
	}
	b.WriteString(line[last:])
	return b.String()
}

// matchStatus is the faint bottom row shown while a filter is active.
func (v *TextView) matchStatus() string {
	if len(v.matched) == 0 {
		return v.theme.Faint.Render(fmt.Sprintf("no matches for %q", v.filter))
	}
	return v.theme.Faint.Render(fmt.Sprintf("match %d/%d for %q · n/N jump", v.matchAt+1, len(v.matched), v.filter))
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
	filtering := v.filter != ""
	body := height
	if filtering {
		body = height - 1 // reserve the bottom row for the match status
		if body < 1 {
			body = 1
		}
	}
	v.height = body
	if v.offset > v.maxOffset() {
		v.offset = v.maxOffset()
	}
	if v.offset < 0 {
		v.offset = 0
	}
	end := v.offset + body
	if end > len(v.lines) {
		end = len(v.lines)
	}
	cur := -1
	if len(v.matched) > 0 {
		cur = v.matched[v.matchAt]
	}
	rendered := make([]string, 0, end-v.offset)
	for i := v.offset; i < end; i++ {
		ln := v.lines[i]
		if filtering {
			ln = v.highlight(ln, i == cur)
		}
		rendered = append(rendered, ln)
	}
	out := strings.Join(rendered, "\n")
	if filtering {
		out += "\n" + v.matchStatus()
	}
	return out
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
