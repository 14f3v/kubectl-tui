package view

import (
	"fmt"
	"hash/fnv"
	"image/color"
	"strconv"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	corev1 "k8s.io/api/core/v1"
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

	id        string // current stream id (bumped on restart so stale msgs are ignored)
	baseID    string // stable base for id, without the restart generation suffix
	gen       int
	namespace string

	// Single-pod restart params. pod/container are empty for grouped pages.
	pod       string
	container string
	opts      logstream.Options
	wrap      bool

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

// logsApplyOptsMsg carries an option change from a modal prompt (which runs off
// the UI thread) back to the page's Update so the stream restarts on-thread.
type logsApplyOptsMsg struct {
	baseID string
	opts   logstream.Options
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
	return newSinglePodLogs(sess, theme, namespace, pod, container, logstream.Options{})
}

// NewPreviousLogsPage opens the previous (terminated) container's logs.
func NewPreviousLogsPage(sess *k8s.Session, theme style.Theme, namespace, pod, container string) *logsPage {
	return newSinglePodLogs(sess, theme, namespace, pod, container, logstream.Options{Previous: true})
}

func newSinglePodLogs(sess *k8s.Session, theme style.Theme, namespace, pod, container string, opts logstream.Options) *logsPage {
	base := namespace + "/" + pod + "/" + container
	p := &logsPage{
		theme:      theme,
		sess:       sess,
		id:         base,
		baseID:     base,
		namespace:  namespace,
		pod:        pod,
		container:  container,
		opts:       opts,
		autoscroll: true,
	}
	p.title = p.computeTitle()
	// Reads p.id and p.opts at call time so restart picks up the new generation.
	p.startSingle = func(sink func(tea.Msg)) logStopper {
		return logstream.StartWithOptions(sess.Context(), sess.CS, sink, p.id, p.namespace, p.pod, p.container, p.opts)
	}
	return p
}

func (p *logsPage) computeTitle() string {
	t := p.pod
	if p.container != "" {
		t = p.pod + "/" + p.container
	}
	t += " · logs"
	if p.opts.Previous {
		t += " (previous)"
	}
	return t
}

// restart stops the current single-pod stream and reopens it with p.opts. The id
// is bumped so any late messages from the old stream are ignored.
func (p *logsPage) restart() {
	if p.grouped || p.startSingle == nil {
		return
	}
	if p.stream != nil {
		p.stream.Stop()
		p.stream = nil
	}
	p.gen++
	p.id = fmt.Sprintf("%s#%d", p.baseID, p.gen)
	p.lines = nil
	p.dropped = 0
	p.ended = false
	p.endErr = nil
	p.offset = 0
	p.autoscroll = true
	p.title = p.computeTitle()
	p.stream = p.startSingle(p.sess.Engine.Sink())
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
	keys := []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "scroll")),
		key.NewBinding(key.WithKeys("f"), key.WithHelp("f", "follow")),
		key.NewBinding(key.WithKeys("g", "G"), key.WithHelp("g/G", "top/bottom")),
		key.NewBinding(key.WithKeys("w"), key.WithHelp("w", "wrap")),
		key.NewBinding(key.WithKeys("C"), key.WithHelp("C", "save")),
	}
	// Restart-based options only apply to a single pod/container stream.
	if !p.grouped {
		keys = append(keys,
			key.NewBinding(key.WithKeys("p"), key.WithHelp("p", "previous")),
			key.NewBinding(key.WithKeys("c"), key.WithHelp("c", "container")),
			key.NewBinding(key.WithKeys("t"), key.WithHelp("t", "tail")),
		)
	}
	return append(keys, key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")))
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
	case logsApplyOptsMsg:
		if t.baseID == p.baseID {
			p.opts = t.opts
			p.restart()
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
	case "w":
		p.wrap = !p.wrap
	case "C":
		return p, p.saveAction()
	case "p":
		if p.grouped {
			return p, toast("previous logs are single-pod only", msg.LevelInfo)
		}
		p.opts.Previous = !p.opts.Previous
		p.restart()
	case "c":
		if p.grouped {
			return p, toast("container picker is single-pod only", msg.LevelInfo)
		}
		return p, p.containerPicker()
	case "t":
		if p.grouped {
			return p, toast("tail is single-pod only", msg.LevelInfo)
		}
		return p, p.tailPrompt()
	}
	return p, nil
}

// saveAction prompts for a path and writes the current buffer to it.
func (p *logsPage) saveAction() tea.Cmd {
	lines := append([]string(nil), p.lines...) // snapshot; the prompt runs off-thread
	name := strings.ReplaceAll(p.pod, "/", "-")
	if name == "" {
		name = "logs"
	}
	def := "/tmp/" + name + ".log"
	return func() tea.Msg {
		return PromptRequest{
			Title:   "Save logs",
			Label:   "Path",
			Initial: def,
			Action: func(path string) tea.Msg {
				if err := logstream.SaveLines(strings.TrimSpace(path), lines); err != nil {
					return msg.Toast{Text: "save: " + err.Error(), Level: msg.LevelError}
				}
				return msg.Toast{Text: fmt.Sprintf("saved %d lines to %s", len(lines), strings.TrimSpace(path)), Level: msg.LevelSuccess}
			},
		}
	}
}

// tailPrompt asks for a tail-line count and restarts the stream via a msg (the
// prompt Action runs off-thread, so it must not touch page state directly).
func (p *logsPage) tailPrompt() tea.Cmd {
	baseID := p.baseID
	cur := p.opts
	initial := "500"
	if cur.TailLines > 0 {
		initial = strconv.FormatInt(cur.TailLines, 10)
	}
	return func() tea.Msg {
		return PromptRequest{
			Title:   "Tail lines",
			Label:   "Lines",
			Initial: initial,
			Validate: func(s string) error {
				n, err := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
				if err != nil || n < 0 {
					return fmt.Errorf("enter a non-negative whole number")
				}
				return nil
			},
			Action: func(s string) tea.Msg {
				n, _ := strconv.ParseInt(strings.TrimSpace(s), 10, 64)
				newOpts := cur
				newOpts.TailLines = n
				return logsApplyOptsMsg{baseID: baseID, opts: newOpts}
			},
		}
	}
}

// containerPicker pushes the container drill-in for the current pod (from cache).
func (p *logsPage) containerPicker() tea.Cmd {
	vs := p.sess.Engine.Get("pods")
	if vs == nil {
		return toast("no live pod data", msg.LevelError)
	}
	obj, ok := vs.Get(p.namespace, p.pod)
	if !ok {
		return toast(p.pod+" is no longer in the cache", msg.LevelWarn)
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	page := newContainersPage(p.sess, p.theme, pod)
	return func() tea.Msg { return PushMsg{Page: page} }
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
		body := strings.Join(rendered, "\n")
		if p.wrap && width > 0 {
			body = lipgloss.NewStyle().Width(width).Render(body)
		}
		visible = body
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
	if p.opts.Previous {
		parts = append(parts, t.Faint.Render("previous"))
	}
	if p.opts.TailLines > 0 {
		parts = append(parts, t.Faint.Render("tail "+strconv.FormatInt(p.opts.TailLines, 10)))
	}
	if p.wrap {
		parts = append(parts, t.Faint.Render("wrap"))
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
