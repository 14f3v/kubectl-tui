package view

import (
	"hash/fnv"
	"image/color"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/14f3v/kubectl-tui/internal/action/logstream"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// maxLogLines bounds the log page's own display buffer; older lines scroll off.
const maxLogLines = 50000

// logStopper is the minimal control surface a logsPage needs over its stream. A
// single-pod Session and a multi-pod Group both satisfy it and both deliver via
// the same LogBatch/LogEnded messages keyed by the page's id, so the page body is
// identical for either source.
type logStopper interface{ Stop() }

// logsPage follows a pod's (or a whole workload's) logs. It receives coalesced
// LogBatch messages, keeps a bounded display buffer, autoscrolls unless the user
// has scrolled up, and surfaces dropped-line counts and stream termination.
type logsPage struct {
	title string
	theme style.Theme
	sess  *k8s.Session

	id        string
	namespace string

	// Exactly one start path is set. startSingle opens one pod/container stream
	// synchronously; resolve lists a workload's pods off the UI thread, after which
	// the group stream starts. grouped enables per-pod tag colouring.
	startSingle func(sink func(tea.Msg)) logStopper
	resolve     func() ([]logstream.PodRef, error)
	grouped     bool

	stream  logStopper
	lines   []string
	dropped int
	ended   bool
	endErr  error
	tagCols map[string]color.Color

	offset     int
	height     int
	autoscroll bool
}

// logRefsResolved delivers the pods a workload's selector expanded to, so the
// group stream can start once the (blocking) list finishes off the UI thread.
type logRefsResolved struct {
	id   string
	refs []logstream.PodRef
	err  error
}

// NewLogsPage builds a logs page for a single pod (optionally a specific container).
func NewLogsPage(sess *k8s.Session, theme style.Theme, namespace, pod, container string) *logsPage {
	title := pod + " · logs"
	if container != "" {
		title = pod + "/" + container + " · logs"
	}
	id := namespace + "/" + pod + "/" + container
	p := &logsPage{
		title:      title,
		theme:      theme,
		sess:       sess,
		id:         id,
		namespace:  namespace,
		autoscroll: true,
	}
	p.startSingle = func(sink func(tea.Msg)) logStopper {
		return logstream.Start(sess.Context(), sess.CS, sink, id, namespace, pod, container)
	}
	return p
}

// NewMultiLogsPage builds a logs page that tails every pod matching a workload's
// selector, merged into one stream with per-pod [tag] prefixes.
func NewMultiLogsPage(sess *k8s.Session, theme style.Theme, title, namespace string, sel *metav1.LabelSelector) *logsPage {
	id := "group:" + namespace + "/" + title
	return &logsPage{
		title:      title,
		theme:      theme,
		sess:       sess,
		id:         id,
		namespace:  namespace,
		grouped:    true,
		autoscroll: true,
		tagCols:    map[string]color.Color{},
		resolve: func() ([]logstream.PodRef, error) {
			return logstream.PodRefsForSelector(sess.Context(), sess.CS, namespace, sel)
		},
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
	if p.startSingle != nil {
		p.stream = p.startSingle(p.sess.Engine.Sink())
		return nil
	}
	// Group: resolve the workload's pods off the UI thread, then start on the result.
	id, resolve := p.id, p.resolve
	return func() tea.Msg {
		refs, err := resolve()
		return logRefsResolved{id: id, refs: refs, err: err}
	}
}

func (p *logsPage) OnLeave() {
	if p.stream != nil {
		p.stream.Stop()
		p.stream = nil
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
	case logRefsResolved:
		if t.id != p.id {
			return p, nil
		}
		if t.err != nil {
			p.ended = true
			p.endErr = t.err
			return p, nil
		}
		p.stream = logstream.StartGroup(p.sess.Context(), p.sess.CS, p.sess.Engine.Sink(), p.id, p.namespace, t.refs)
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
		rendered := make([]string, 0, end-p.offset)
		for _, ln := range p.lines[p.offset:end] {
			rendered = append(rendered, p.renderLine(ln))
		}
		visible = strings.Join(rendered, "\n")
	}
	return visible + "\n" + p.status()
}

// renderLine colours the leading [tag] prefix of a merged log line so each pod is
// visually distinct. Single-pod pages return the line unchanged (their content may
// legitimately start with "[").
func (p *logsPage) renderLine(line string) string {
	if !p.grouped || !strings.HasPrefix(line, "[") {
		return line
	}
	i := strings.Index(line, "] ")
	if i < 0 {
		return line
	}
	tag, rest := line[1:i], line[i+2:]
	return lipgloss.NewStyle().Foreground(p.tagColor(tag)).Render("["+tag+"]") + " " + rest
}

// tagColor assigns each pod tag a stable colour from the theme palette, hashing
// the tag so a pod keeps its colour across renders.
func (p *logsPage) tagColor(tag string) color.Color {
	if c, ok := p.tagCols[tag]; ok {
		return c
	}
	palette := []color.Color{p.theme.Pal.Accent, p.theme.Pal.OK, p.theme.Pal.Warn, p.theme.Pal.Info, p.theme.Pal.Cyan}
	h := fnv.New32a()
	_, _ = h.Write([]byte(tag))
	c := palette[int(h.Sum32())%len(palette)]
	p.tagCols[tag] = c
	return c
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
