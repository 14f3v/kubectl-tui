package view

import (
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/action/logstream"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/k8s"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/msg"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/style"
)

// maxLogLines bounds the log page's own display buffer; older lines scroll off.
const maxLogLines = 50000

// logsPage follows a pod's logs. It receives coalesced LogBatch messages, keeps a
// bounded display buffer, autoscrolls unless the user has scrolled up, and
// surfaces dropped-line counts and stream termination.
type logsPage struct {
	title string
	theme style.Theme
	sess  *k8s.Session

	id        string
	namespace string
	pod       string
	container string

	session *logstream.Session
	lines   []string
	dropped int
	ended   bool
	endErr  error

	offset     int
	height     int
	autoscroll bool
}

// NewLogsPage builds a logs page for a pod (optionally a specific container).
func NewLogsPage(sess *k8s.Session, theme style.Theme, namespace, pod, container string) *logsPage {
	title := pod + " · logs"
	if container != "" {
		title = pod + "/" + container + " · logs"
	}
	return &logsPage{
		title:      title,
		theme:      theme,
		sess:       sess,
		id:         namespace + "/" + pod + "/" + container,
		namespace:  namespace,
		pod:        pod,
		container:  container,
		autoscroll: true,
	}
}

func (p *logsPage) Init() tea.Cmd     { return nil }
func (p *logsPage) Title() string     { return p.title }
func (p *logsPage) Kind() string      { return "" }
func (p *logsPage) Namespace() string { return p.namespace }
func (p *logsPage) Filter() string    { return "" }
func (p *logsPage) SetFilter(string)  {}

func (p *logsPage) Summary() Summary { return Summary{Total: len(p.lines)} }

func (p *logsPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "scroll")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
		key.NewBinding(key.WithKeys("g", "G"), key.WithHelp("g/G", "top/bottom")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *logsPage) OnEnter() tea.Cmd {
	p.session = logstream.Start(p.sess.Context(), p.sess.CS, p.sess.Engine.Sink(), p.id, p.namespace, p.pod, p.container)
	return nil
}

func (p *logsPage) OnLeave() {
	if p.session != nil {
		p.session.Stop()
		p.session = nil
	}
}

func (p *logsPage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case msg.LogBatch:
		if t.SessionID != p.id {
			return p, nil
		}
		p.dropped += t.Dropped
		p.lines = append(p.lines, t.Lines...)
		if len(p.lines) > maxLogLines {
			p.lines = p.lines[len(p.lines)-maxLogLines:]
		}
		if p.autoscroll {
			p.offset = p.maxOffset()
		}
		return p, nil
	case msg.LogEnded:
		if t.SessionID == p.id {
			p.ended = true
			p.endErr = t.Err
		}
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(t)
	}
	return p, nil
}

func (p *logsPage) handleKey(k tea.KeyPressMsg) (Page, tea.Cmd) {
	switch k.String() {
	case "j", "down":
		p.autoscroll = false
		p.scroll(1)
	case "k", "up":
		p.autoscroll = false
		p.scroll(-1)
	case "pgdown", "ctrl+f":
		p.autoscroll = false
		p.scroll(p.height)
	case "pgup", "ctrl+b":
		p.autoscroll = false
		p.scroll(-p.height)
	case "g", "home":
		p.autoscroll = false
		p.offset = 0
	case "G", "end":
		p.offset = p.maxOffset()
	case "f":
		p.autoscroll = !p.autoscroll
		if p.autoscroll {
			p.offset = p.maxOffset()
		}
	}
	return p, nil
}

func (p *logsPage) View(width, height int) string {
	// Reserve a status line at the bottom for drop/end notices.
	body := height - 1
	if body < 1 {
		body = 1
	}
	p.height = body
	if p.autoscroll {
		p.offset = p.maxOffset()
	} else if p.offset > p.maxOffset() {
		p.offset = p.maxOffset()
	}

	end := p.offset + body
	if end > len(p.lines) {
		end = len(p.lines)
	}
	visible := ""
	if p.offset < end {
		visible = strings.Join(p.lines[p.offset:end], "\n")
	}
	return visible + "\n" + p.status()
}

func (p *logsPage) status() string {
	t := p.theme
	parts := []string{}
	if p.autoscroll {
		parts = append(parts, t.AccentText.Render("following"))
	} else {
		parts = append(parts, t.Faint.Render("paused (f to follow)"))
	}
	if p.dropped > 0 {
		parts = append(parts, t.PinkText.Render("… "+itoaLocal(p.dropped)+" lines dropped"))
	}
	if p.ended {
		if p.endErr != nil {
			parts = append(parts, t.Faint.Render("stream ended: "+p.endErr.Error()))
		} else {
			parts = append(parts, t.Faint.Render("stream ended"))
		}
	}
	return t.Faint.Render("— ") + strings.Join(parts, t.Faint.Render("  ·  "))
}

func (p *logsPage) scroll(delta int) {
	p.offset += delta
	if p.offset < 0 {
		p.offset = 0
	}
	if p.offset > p.maxOffset() {
		p.offset = p.maxOffset()
	}
}

func (p *logsPage) maxOffset() int {
	if p.height <= 0 {
		return 0
	}
	m := len(p.lines) - p.height
	if m < 0 {
		return 0
	}
	return m
}

func itoaLocal(n int) string {
	if n == 0 {
		return "0"
	}
	var b [12]byte
	i := len(b)
	for n > 0 {
		i--
		b[i] = byte('0' + n%10)
		n /= 10
	}
	return string(b[i:])
}
