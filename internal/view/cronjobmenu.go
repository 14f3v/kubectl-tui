package view

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/cronjob"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

type cronJobItem struct{ label, desc string }

// cronJobMenuPage is pushed by enter on a CronJob: trigger a run now, or toggle
// suspend. Trigger is confirm-gated (it creates a Job).
type cronJobMenuPage struct {
	sess      *k8s.Session
	theme     style.Theme
	namespace string
	name      string
	readOnly  bool
	items     []cronJobItem
	cursor    int
}

func newCronJobMenuPage(sess *k8s.Session, theme style.Theme, namespace, name string, readOnly bool) *cronJobMenuPage {
	return &cronJobMenuPage{
		sess: sess, theme: theme, namespace: namespace, name: name, readOnly: readOnly,
		items: []cronJobItem{
			{"Trigger", "run now — create a Job from the template"},
			{"Suspend", "stop scheduling (spec.suspend=true)"},
			{"Resume", "resume scheduling"},
		},
	}
}

func (p *cronJobMenuPage) Init() tea.Cmd     { return nil }
func (p *cronJobMenuPage) Title() string     { return p.name + " · cronjob" }
func (p *cronJobMenuPage) Kind() string      { return "" }
func (p *cronJobMenuPage) Namespace() string { return p.namespace }
func (p *cronJobMenuPage) Filter() string    { return "" }
func (p *cronJobMenuPage) SetFilter(string)  {}
func (p *cronJobMenuPage) OnEnter() tea.Cmd  { return nil }
func (p *cronJobMenuPage) OnLeave()          {}
func (p *cronJobMenuPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *cronJobMenuPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "run")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *cronJobMenuPage) Update(m tea.Msg) (Page, tea.Cmd) {
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
	case "enter", "space":
		switch p.items[p.cursor].label {
		case "Trigger":
			return p, p.trigger()
		case "Suspend":
			return p, p.suspend(true)
		case "Resume":
			return p, p.suspend(false)
		}
	}
	return p, nil
}

func (p *cronJobMenuPage) trigger() tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	sess, ns, name := p.sess, p.namespace, p.name
	act := func() tea.Msg {
		job, err := cronjob.TriggerJob(sess.Context(), sess.CS, ns, name)
		if err != nil {
			return msg.Toast{Text: "trigger " + name + ": " + err.Error(), Level: msg.LevelError}
		}
		return msg.Toast{Text: "created job " + job, Level: msg.LevelSuccess}
	}
	return func() tea.Msg {
		return ConfirmRequest{Title: "Trigger " + name, Prompt: "Run " + name + " now?", Action: act}
	}
}

func (p *cronJobMenuPage) suspend(suspend bool) tea.Cmd {
	if p.readOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	sess, ns, name := p.sess, p.namespace, p.name
	verb := "suspended"
	if !suspend {
		verb = "resumed"
	}
	return func() tea.Msg {
		if err := cronjob.SetSuspend(sess.Context(), sess.CS, ns, name, suspend); err != nil {
			return msg.Toast{Text: name + ": " + err.Error(), Level: msg.LevelError}
		}
		return msg.Toast{Text: name + " " + verb, Level: msg.LevelSuccess}
	}
}

func (p *cronJobMenuPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  CRONJOB ACTION") + "\n")
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
