package view

import (
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/rollout"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// rolloutItem is one selectable rollout action.
type rolloutItem struct {
	label string
	desc  string
}

// rolloutPage is the small menu pushed by pressing r on a rollable workload
// (Deployment/StatefulSet/DaemonSet). It offers a rolling restart (behind a
// confirm) and a rollout status read-out. Undo/history is a planned follow-up.
type rolloutPage struct {
	sess      *k8s.Session
	theme     style.Theme
	kind      string
	namespace string
	name      string
	readOnly  bool
	items     []rolloutItem
	cursor    int
}

func newRolloutPage(sess *k8s.Session, theme style.Theme, kind, namespace, name string, readOnly bool) *rolloutPage {
	items := []rolloutItem{
		{"Restart", "roll all pods with a fresh restartedAt stamp"},
		{"Status", "show how far the current rollout has progressed"},
		{"History", "list revisions and roll back to one"},
	}
	// Pause/Resume only apply to Deployments (only they have spec.paused).
	if rollout.Pausable(kind) {
		items = append(items,
			rolloutItem{"Pause", "hold the rollout (spec.paused=true)"},
			rolloutItem{"Resume", "continue a paused rollout"},
		)
	}
	return &rolloutPage{
		sess:      sess,
		theme:     theme,
		kind:      kind,
		namespace: namespace,
		name:      name,
		readOnly:  readOnly,
		items:     items,
	}
}

func (p *rolloutPage) Init() tea.Cmd     { return nil }
func (p *rolloutPage) Title() string     { return p.name + " · rollout" }
func (p *rolloutPage) Kind() string      { return "" }
func (p *rolloutPage) Namespace() string { return p.namespace }
func (p *rolloutPage) Filter() string    { return "" }
func (p *rolloutPage) SetFilter(string)  {}
func (p *rolloutPage) OnEnter() tea.Cmd  { return nil }
func (p *rolloutPage) OnLeave()          {}
func (p *rolloutPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *rolloutPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *rolloutPage) Update(m tea.Msg) (Page, tea.Cmd) {
	k, ok := m.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "j", "down":
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter", " ":
		switch p.items[p.cursor].label {
		case "Restart":
			return p, p.restart()
		case "Status":
			return p, p.status()
		case "History":
			return p, p.history()
		case "Pause":
			return p, p.setPaused(true)
		case "Resume":
			return p, p.setPaused(false)
		}
	}
	return p, nil
}

// setPaused pauses or resumes the deployment's rollout.
func (p *rolloutPage) setPaused(paused bool) tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	sess, ns, name := p.sess, p.namespace, p.name
	verb := "paused"
	if !paused {
		verb = "resumed"
	}
	return func() tea.Msg {
		if err := rollout.SetPaused(sess.Context(), sess.CS, ns, name, paused); err != nil {
			return msg.Toast{Text: "rollout " + name + ": " + err.Error(), Level: msg.LevelError}
		}
		return msg.Toast{Text: name + " rollout " + verb, Level: msg.LevelSuccess}
	}
}

// history opens the revision list drill-in.
func (p *rolloutPage) history() tea.Cmd {
	page := newRolloutHistoryPage(p.sess, p.theme, p.kind, p.namespace, p.name, p.readOnly)
	return func() tea.Msg { return PushMsg{Page: page} }
}

// restart asks for confirmation, then triggers a rolling restart.
func (p *rolloutPage) restart() tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	sess := p.sess
	kind, ns, name := p.kind, p.namespace, p.name
	act := func() tea.Msg {
		if err := rollout.Restart(sess.Context(), sess.CS, kind, ns, name, time.Now()); err != nil {
			return msg.Toast{Text: "restart " + name + ": " + err.Error(), Level: msg.LevelError}
		}
		return msg.Toast{Text: name + " restart triggered", Level: msg.LevelSuccess}
	}
	return func() tea.Msg {
		return ConfirmRequest{Title: "Restart " + name, Prompt: "Trigger a rolling restart of " + name + "?", Action: act}
	}
}

// status reads the cached (watch-fresh) object and renders its rollout progress.
func (p *rolloutPage) status() tea.Cmd {
	vs := p.sess.Engine.Get(p.kind)
	if vs == nil {
		return toast("no live data for "+p.kind, msg.LevelError)
	}
	obj, ok := vs.Get(p.namespace, p.name)
	if !ok {
		return toast(p.name+" is no longer in the cache", msg.LevelWarn)
	}
	summary, done := rollout.Status(obj)
	state := "in progress"
	if done {
		state = "complete"
	}
	text := p.name + "\n\nrollout: " + state + "\n" + summary
	tv := NewTextView(p.name+" · rollout status", text, p.theme)
	return func() tea.Msg { return PushMsg{Page: tv} }
}

func (p *rolloutPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  ACTION") + "\n")
	for i, it := range p.items {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		b.WriteString(marker +
			nameStyle.Render(pad(it.label, 12)) +
			t.Faint.Render(trunc(it.desc, width-18)) + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  enter run · esc back"))
	return b.String()
}
