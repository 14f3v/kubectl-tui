package view

import (
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	corev1 "k8s.io/api/core/v1"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/action/execshell"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/k8s"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/style"
)

// containerInfo is the decoded per-container state shown in the drill-in.
type containerInfo struct {
	Name     string
	Image    string
	Ready    bool
	Restarts int
	State    string
	class    columns.StatusClass
}

// containersPage lists a pod's containers so logs and shell can target a specific
// one. It is pushed when the user presses enter on a pod.
type containersPage struct {
	sess       *k8s.Session
	theme      style.Theme
	namespace  string
	pod        string
	containers []containerInfo
	cursor     int
}

// newContainersPage builds the drill-in from a cached pod object.
func newContainersPage(sess *k8s.Session, theme style.Theme, pod *corev1.Pod) *containersPage {
	return &containersPage{
		sess:       sess,
		theme:      theme,
		namespace:  pod.Namespace,
		pod:        pod.Name,
		containers: extractContainers(pod),
	}
}

func extractContainers(pod *corev1.Pod) []containerInfo {
	statusByName := map[string]corev1.ContainerStatus{}
	for _, cs := range pod.Status.ContainerStatuses {
		statusByName[cs.Name] = cs
	}
	out := make([]containerInfo, 0, len(pod.Spec.Containers))
	for _, c := range pod.Spec.Containers {
		ci := containerInfo{Name: c.Name, Image: c.Image, State: "Unknown", class: columns.StatusMuted}
		if cs, ok := statusByName[c.Name]; ok {
			ci.Ready = cs.Ready
			ci.Restarts = int(cs.RestartCount)
			ci.State, ci.class = containerState(cs)
		}
		out = append(out, ci)
	}
	return out
}

func containerState(cs corev1.ContainerStatus) (string, columns.StatusClass) {
	switch {
	case cs.State.Running != nil:
		if cs.Ready {
			return "Running", columns.StatusOK
		}
		return "Running (not ready)", columns.StatusWarn
	case cs.State.Waiting != nil:
		return cs.State.Waiting.Reason, columns.StatusWarn
	case cs.State.Terminated != nil:
		r := cs.State.Terminated.Reason
		if r == "Completed" {
			return r, columns.StatusInfo
		}
		return r, columns.StatusError
	}
	return "Unknown", columns.StatusMuted
}

func (p *containersPage) Init() tea.Cmd     { return nil }
func (p *containersPage) Title() string     { return p.pod + " · containers" }
func (p *containersPage) Kind() string      { return "" }
func (p *containersPage) Namespace() string { return p.namespace }
func (p *containersPage) Filter() string    { return "" }
func (p *containersPage) SetFilter(string)  {}
func (p *containersPage) OnEnter() tea.Cmd  { return nil }
func (p *containersPage) OnLeave()          {}
func (p *containersPage) Summary() Summary  { return Summary{Total: len(p.containers)} }

func (p *containersPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("l"), key.WithHelp("l", "logs")),
		key.NewBinding(key.WithKeys("s"), key.WithHelp("s", "shell")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *containersPage) Update(m tea.Msg) (Page, tea.Cmd) {
	k, ok := m.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "j", "down":
		if p.cursor < len(p.containers)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "l":
		if c, ok := p.selected(); ok {
			page := NewLogsPage(p.sess, p.theme, p.namespace, p.pod, c.Name)
			return p, func() tea.Msg { return PushMsg{Page: page} }
		}
	case "s":
		if c, ok := p.selected(); ok {
			cmd := execshell.New(p.sess.RestCfg, p.sess.CS, p.namespace, p.pod, c.Name, nil)
			return p, func() tea.Msg { return ExecRequest{Label: "shell", Command: cmd} }
		}
	}
	return p, nil
}

func (p *containersPage) selected() (containerInfo, bool) {
	if p.cursor < 0 || p.cursor >= len(p.containers) {
		return containerInfo{}, false
	}
	return p.containers[p.cursor], true
}

func (p *containersPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render(pad("  NAME", 24)+pad("READY", 7)+pad("RESTARTS", 10)+pad("STATE", 22)+"IMAGE") + "\n")
	for i, c := range p.containers {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		ready := "false"
		readyColor := t.Pal.Err
		if c.Ready {
			ready, readyColor = "true", t.Pal.OK
		}
		state := lipgloss.NewStyle().Foreground(t.StatusColor(c.class)).Render("● " + c.State)
		b.WriteString(marker +
			nameStyle.Render(pad(c.Name, 22)) +
			lipgloss.NewStyle().Foreground(readyColor).Render(pad(ready, 7)) +
			t.Dim.Render(pad(strconv.Itoa(c.Restarts), 10)) +
			pad(state, 22) +
			t.Faint.Render(trunc(c.Image, width-64)) + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  l logs · s shell · esc back"))
	return b.String()
}
