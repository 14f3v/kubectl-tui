package view

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/cp"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

type cpItem struct{ label, desc string }

// cpMenuPage is the copy-files menu, pushed from a pod row (issue #30). Each item
// collects two whitespace-separated paths and streams the file to or from the
// pod off the UI thread. container is "" for the pod's first container.
type cpMenuPage struct {
	sess      *k8s.Session
	theme     style.Theme
	namespace string
	pod       string
	container string
	items     []cpItem
	cursor    int
}

func newCpMenuPage(sess *k8s.Session, theme style.Theme, namespace, pod, container string) *cpMenuPage {
	return &cpMenuPage{
		sess: sess, theme: theme, namespace: namespace, pod: pod, container: container,
		items: []cpItem{
			{"Upload", "local → pod (localPath remotePath)"},
			{"Download", "pod → local (remotePath localPath)"},
		},
	}
}

func (p *cpMenuPage) Init() tea.Cmd     { return nil }
func (p *cpMenuPage) Title() string     { return p.pod + " · copy" }
func (p *cpMenuPage) Kind() string      { return "" }
func (p *cpMenuPage) Namespace() string { return p.namespace }
func (p *cpMenuPage) Filter() string    { return "" }
func (p *cpMenuPage) SetFilter(string)  {}
func (p *cpMenuPage) OnEnter() tea.Cmd  { return nil }
func (p *cpMenuPage) OnLeave()          {}
func (p *cpMenuPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *cpMenuPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "copy")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *cpMenuPage) Update(m tea.Msg) (Page, tea.Cmd) {
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
		case "Upload":
			return p, p.upload()
		case "Download":
			return p, p.download()
		}
	}
	return p, nil
}

// upload collects "localPath remotePath" and streams the local file into the pod
// off the UI thread.
func (p *cpMenuPage) upload() tea.Cmd {
	pod := p.pod
	return func() tea.Msg {
		return PromptRequest{
			Title: "Upload · " + pod,
			Label: "localPath remotePath",
			Action: func(v string) tea.Msg {
				local, remote, ok := twoFields(v)
				if !ok {
					return msg.Toast{Text: "expected: localPath remotePath", Level: msg.LevelError}
				}
				if err := cp.CopyToPod(p.sess.Context(), p.sess.RestCfg, p.sess.CS, p.namespace, p.pod, p.container, local, remote); err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				return msg.Toast{Text: "uploaded to " + pod, Level: msg.LevelSuccess}
			},
		}
	}
}

// download collects "remotePath localPath" and streams the pod file to the local
// path off the UI thread.
func (p *cpMenuPage) download() tea.Cmd {
	pod := p.pod
	return func() tea.Msg {
		return PromptRequest{
			Title: "Download · " + pod,
			Label: "remotePath localPath",
			Action: func(v string) tea.Msg {
				remote, local, ok := twoFields(v)
				if !ok {
					return msg.Toast{Text: "expected: remotePath localPath", Level: msg.LevelError}
				}
				if err := cp.CopyFromPod(p.sess.Context(), p.sess.RestCfg, p.sess.CS, p.namespace, p.pod, p.container, remote, local); err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				return msg.Toast{Text: "downloaded from " + pod, Level: msg.LevelSuccess}
			},
		}
	}
}

// twoFields splits v on whitespace, returning ok only when there are exactly two
// non-empty fields.
func twoFields(v string) (a, b string, ok bool) {
	f := strings.Fields(v)
	if len(f) != 2 {
		return "", "", false
	}
	return f[0], f[1], true
}

func (p *cpMenuPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  COPY") + "\n")
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
	b.WriteString("\n" + t.Faint.Render("  enter copy · esc back"))
	return b.String()
}
