package view

import (
	"fmt"
	"strconv"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/rollout"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// rolloutRevisionsMsg delivers a workload's revision history, resolved off the UI
// thread. Tagged by workload name so a stale result (page already left) is ignored.
type rolloutRevisionsMsg struct {
	name      string
	revisions []rollout.Revision
	err       error
}

// rolloutHistoryPage lists a workload's rollout revisions; enter rolls back to the
// selected one (confirm-gated). Pushed from the rollout menu's History item.
type rolloutHistoryPage struct {
	sess      *k8s.Session
	theme     style.Theme
	kind      string
	namespace string
	name      string
	readOnly  bool

	loaded    bool
	revisions []rollout.Revision
	errMsg    string
	cursor    int
}

func newRolloutHistoryPage(sess *k8s.Session, theme style.Theme, kind, namespace, name string, readOnly bool) *rolloutHistoryPage {
	return &rolloutHistoryPage{sess: sess, theme: theme, kind: kind, namespace: namespace, name: name, readOnly: readOnly}
}

func (p *rolloutHistoryPage) Init() tea.Cmd     { return nil }
func (p *rolloutHistoryPage) Title() string     { return p.name + " · revisions" }
func (p *rolloutHistoryPage) Kind() string      { return "" }
func (p *rolloutHistoryPage) Namespace() string { return p.namespace }
func (p *rolloutHistoryPage) Filter() string    { return "" }
func (p *rolloutHistoryPage) SetFilter(string)  {}
func (p *rolloutHistoryPage) OnLeave()          {}
func (p *rolloutHistoryPage) Summary() Summary  { return Summary{Total: len(p.revisions)} }

// OnEnter resolves the revision history off the UI thread.
func (p *rolloutHistoryPage) OnEnter() tea.Cmd {
	sess, kind, ns, name := p.sess, p.kind, p.namespace, p.name
	return func() tea.Msg {
		revs, err := rollout.History(sess.Context(), sess.CS, kind, ns, name)
		return rolloutRevisionsMsg{name: name, revisions: revs, err: err}
	}
}

func (p *rolloutHistoryPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "roll back")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *rolloutHistoryPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case rolloutRevisionsMsg:
		if t.name != p.name {
			return p, nil
		}
		p.loaded = true
		if t.err != nil {
			p.errMsg = t.err.Error()
			return p, nil
		}
		p.revisions = t.revisions
		return p, nil
	case tea.KeyPressMsg:
		switch t.String() {
		case "j", "down":
			if p.cursor < len(p.revisions)-1 {
				p.cursor++
			}
		case "k", "up":
			if p.cursor > 0 {
				p.cursor--
			}
		case "enter", "space":
			return p, p.undo()
		}
	}
	return p, nil
}

func (p *rolloutHistoryPage) undo() tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	if p.cursor < 0 || p.cursor >= len(p.revisions) {
		return nil
	}
	rev := p.revisions[p.cursor]
	if rev.Current {
		return toast(fmt.Sprintf("revision %d is already current", rev.Number), msg.LevelInfo)
	}
	sess, kind, ns, name, num := p.sess, p.kind, p.namespace, p.name, rev.Number
	act := func() tea.Msg {
		if err := rollout.Undo(sess.Context(), sess.CS, kind, ns, name, num); err != nil {
			return msg.Toast{Text: fmt.Sprintf("roll back %s to rev %d: %s", name, num, err.Error()), Level: msg.LevelError}
		}
		return msg.Toast{Text: fmt.Sprintf("%s rolled back to revision %d", name, num), Level: msg.LevelSuccess}
	}
	return func() tea.Msg {
		return ConfirmRequest{
			Title:  "Roll back " + name,
			Prompt: fmt.Sprintf("Roll back %s to revision %d?", name, num),
			Danger: true,
			Action: act,
		}
	}
}

func (p *rolloutHistoryPage) View(width, height int) string {
	t := p.theme
	if !p.loaded {
		return t.Faint.Render("  loading revisions…")
	}
	if p.errMsg != "" {
		return t.Faint.Render("  " + trunc(p.errMsg, width-4))
	}
	var b strings.Builder
	b.WriteString(t.ColHeader.Render(pad("  REVISION", 12)+pad("AGE", 8)+"CHANGE-CAUSE") + "\n")
	if len(p.revisions) == 0 {
		b.WriteString(t.Faint.Render("  (no revisions found)") + "\n")
	}
	for i, r := range p.revisions {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		num := strconv.FormatInt(r.Number, 10)
		if r.Current {
			num += "*"
		}
		cause := r.Cause
		if cause == "" {
			cause = "—"
		}
		b.WriteString(marker +
			nameStyle.Render(pad(num, 10)) +
			t.Faint.Render(pad(shortAge(r.CreatedAt), 8)) +
			t.Dim.Render(trunc(cause, width-22)) + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  enter roll back · esc back · * = current"))
	return b.String()
}

// shortAge renders a compact relative age (e.g. 5m, 3h, 2d).
func shortAge(ts time.Time) string {
	if ts.IsZero() {
		return "—"
	}
	d := time.Since(ts)
	switch {
	case d < time.Minute:
		return fmt.Sprintf("%ds", int(d.Seconds()))
	case d < time.Hour:
		return fmt.Sprintf("%dm", int(d.Minutes()))
	case d < 24*time.Hour:
		return fmt.Sprintf("%dh", int(d.Hours()))
	default:
		return fmt.Sprintf("%dd", int(d.Hours()/24))
	}
}
