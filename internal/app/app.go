// Package app is the root Bubble Tea model. It owns the terminal (alt-screen,
// mouse, title), the page stack, the command line and overlays, the global
// keymap, and panic recovery. It routes every message: key input goes to the
// command line or the active page; engine snapshots and action results flow to
// the page or the chrome. All IO happens in commands or engine goroutines, never
// in Update.
package app

import (
	"context"
	"fmt"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/action"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/config"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/k8s"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/metrics"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/msg"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/style"
	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/view"
)

// Version is stamped at build time via -ldflags; "dev" in local builds.
var Version = "dev"

// inputMode is the command-line capture state.
type inputMode int

const (
	modeNone inputMode = iota
	modeCommand
	modeFilter
)

// Config parameterizes the root model.
type Config struct {
	Sink            engine.Sink
	Config          config.Config
	ContextOverride string
	StartKind       string
}

// Model is the root model.
type Model struct {
	cfg   Config
	theme style.Theme

	sink engine.Sink
	gate *action.TerminalGate

	poller     *metrics.Poller
	metricsOK  bool
	clusterCPU float64
	clusterMem float64

	sess    *k8s.Session
	pages   []view.Page // stack; top is active
	fatal   error       // unrecoverable bootstrap error
	booting bool

	width, height int

	mode     inputMode
	inputBuf string

	toast      *msg.Toast
	toastToken int
	showHelp   bool
	confirm    *confirmState

	panicInfo string
}

// confirmState holds an active modal confirm dialog.
type confirmState struct {
	title  string
	prompt string
	danger bool
	action func() tea.Msg
}

// New builds the root model.
func New(cfg Config) *Model {
	theme := style.New(style.AccentPreset(cfg.Config.Accent), cfg.Config.DensityValue())
	if cfg.StartKind == "" {
		cfg.StartKind = "pods"
	}
	return &Model{
		cfg:     cfg,
		theme:   theme,
		sink:    cfg.Sink,
		gate:    action.NewTerminalGate(),
		booting: true,
	}
}

// Init requests the session bootstrap.
func (m *Model) Init() tea.Cmd {
	return bootstrapCmd(m.sink, m.cfg.ContextOverride)
}

// bootstrapCmd builds a Session off the UI goroutine and reports the result.
func bootstrapCmd(sink engine.Sink, ctxOverride string) tea.Cmd {
	return func() tea.Msg {
		sess, err := k8s.NewSession(context.Background(), ctxOverride, sink)
		if err != nil {
			return msg.SessionError{Err: err}
		}
		_ = sess.RefreshServerVersion() // best-effort; empty version is fine
		return msg.SessionReady{Session: sess}
	}
}

// Update is the message router. It recovers from panics in sub-updates so a bug
// in one page cannot crash the whole program.
func (m *Model) Update(message tea.Msg) (next tea.Model, cmd tea.Cmd) {
	defer func() {
		if r := recover(); r != nil {
			m.panicInfo = fmt.Sprintf("%v", r)
			next, cmd = m, nil
		}
	}()

	switch t := message.(type) {
	case tea.WindowSizeMsg:
		m.width, m.height = t.Width, t.Height
		return m, nil

	case tea.KeyPressMsg:
		return m.handleKey(t)

	case msg.SessionReady:
		return m.onSessionReady(t)

	case msg.SessionError:
		m.booting = false
		m.fatal = t.Err
		return m, nil

	case msg.Toast:
		return m.showToast(t)

	case toastExpireMsg:
		if t.token == m.toastToken {
			m.toast = nil
		}
		return m, nil

	case msg.Navigate:
		return m.navigate(t.Kind, t.Namespace)

	case view.PushMsg:
		return m.pushPage(t.Page)

	case view.PopMsg:
		return m.popPage()

	case view.ConfirmRequest:
		m.confirm = &confirmState{title: t.Title, prompt: t.Prompt, danger: t.Danger, action: t.Action}
		return m, nil

	case view.ExecRequest:
		return m.handleExecRequest(t)

	case execDoneMsg:
		return m.onExecDone(t)

	case metrics.Snapshot:
		m.metricsOK = t.Available
		m.clusterCPU = t.ClusterCPUPct
		m.clusterMem = t.ClusterMemPct
		return m, m.routeToPage(t) // the active page overlays per-pod CPU/MEM

	default:
		// Everything else (engine snapshots, action results) goes to the page.
		return m, m.routeToPage(message)
	}
}

// execDoneMsg is delivered when a child process (shell/editor) exits and the
// terminal is restored. after, if set, runs the follow-up (e.g. the edit apply).
type execDoneMsg struct {
	err   error
	after func(err error) tea.Msg
}

// handleExecRequest hands the terminal to a child process through the gate,
// pausing the engine coalescer while the child owns the terminal.
func (m *Model) handleExecRequest(req view.ExecRequest) (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	if !m.gate.Acquire() {
		return m, func() tea.Msg {
			return msg.Toast{Text: "another terminal session is active", Level: msg.LevelWarn}
		}
	}
	m.sess.Engine.PauseAll()
	after := req.After
	cb := func(err error) tea.Msg { return execDoneMsg{err: err, after: after} }

	switch {
	case req.Command != nil:
		return m, tea.Exec(req.Command, cb)
	case req.Process != nil:
		return m, tea.ExecProcess(req.Process, cb)
	default:
		m.gate.Release()
		m.sess.Engine.ResumeAllAndFlush()
		return m, nil
	}
}

// onExecDone restores TUI ownership, resumes the engine, and runs any follow-up.
func (m *Model) onExecDone(t execDoneMsg) (tea.Model, tea.Cmd) {
	m.gate.Release()
	if m.sess != nil {
		m.sess.Engine.ResumeAllAndFlush()
	}
	if t.after != nil {
		after, err := t.after, t.err
		return m, func() tea.Msg { return after(err) }
	}
	if t.err != nil {
		return m, func() tea.Msg {
			return msg.Toast{Text: "session ended: " + t.err.Error(), Level: msg.LevelWarn}
		}
	}
	return m, nil
}

// pushPage drills into a new page, keeping the parent on the stack.
func (m *Model) pushPage(p view.Page) (tea.Model, tea.Cmd) {
	if p == nil {
		return m, nil
	}
	m.pages = append(m.pages, p)
	m.mode = modeNone
	m.inputBuf = ""
	return m, tea.Batch(p.Init(), p.OnEnter())
}

// popPage backs out of a drill-in. The base page is never popped.
func (m *Model) popPage() (tea.Model, tea.Cmd) {
	if len(m.pages) <= 1 {
		return m, nil
	}
	top := m.pages[len(m.pages)-1]
	top.OnLeave()
	m.pages = m.pages[:len(m.pages)-1]
	return m, nil
}

// routeToPage forwards a message to the active page and installs the returned
// page back on the stack.
func (m *Model) routeToPage(message tea.Msg) tea.Cmd {
	p := m.active()
	if p == nil {
		return nil
	}
	np, cmd := p.Update(message)
	m.pages[len(m.pages)-1] = np
	return cmd
}

func (m *Model) onSessionReady(t msg.SessionReady) (tea.Model, tea.Cmd) {
	sess, ok := t.Session.(*k8s.Session)
	if !ok {
		m.fatal = fmt.Errorf("internal: session type mismatch")
		m.booting = false
		return m, nil
	}
	m.sess = sess
	m.booting = false
	m.poller = metrics.NewPoller(sess.CS, sess.Metrics, sess.Disco, m.sink)
	go m.poller.Run(sess.Context())
	return m.navigate(m.cfg.StartKind, sess.Identity.Namespace)
}

// navigate replaces the page stack with a fresh page for kind, scoped to
// namespace ("" = all namespaces).
func (m *Model) navigate(kind, namespace string) (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	page, ok := view.NewPage(kind, view.Deps{
		Session:   m.sess,
		Theme:     m.theme,
		Namespace: namespace,
		ReadOnly:  m.cfg.Config.ReadOnly,
		TierLabel: m.cfg.Config.TierLabel,
	})
	if !ok {
		return m, func() tea.Msg {
			return msg.Toast{Text: "unknown resource: " + kind, Level: msg.LevelError}
		}
	}
	for _, p := range m.pages {
		p.OnLeave()
	}
	m.pages = []view.Page{page}
	m.mode = modeNone
	m.inputBuf = ""
	if m.poller != nil {
		m.poller.SetNamespace(namespace)
	}
	return m, tea.Batch(page.Init(), page.OnEnter())
}

// switchContext disposes the current Session and bootstraps one for the named
// kubeconfig context. All informers, log streams, and forwards die with the old
// Session's context.
func (m *Model) switchContext(name string) (tea.Model, tea.Cmd) {
	for _, p := range m.pages {
		p.OnLeave()
	}
	m.pages = nil
	if m.sess != nil {
		m.sess.Dispose()
		m.sess = nil
	}
	m.booting = true
	m.mode = modeNone
	m.inputBuf = ""
	m.cfg.ContextOverride = name
	return m, bootstrapCmd(m.sink, name)
}

// jumpNamespace switches the active page to a namespace: "0" is all namespaces,
// digits 1-9 select configured favorite namespaces.
func (m *Model) jumpNamespace(d string) (tea.Model, tea.Cmd) {
	p := m.active()
	if p == nil {
		return m, nil
	}
	if d == "0" {
		return m.navigate(p.Kind(), "")
	}
	idx := int(d[0] - '1')
	favs := m.cfg.Config.Favorites
	if idx < 0 || idx >= len(favs) {
		return m, func() tea.Msg {
			return msg.Toast{Text: "no favorite namespace in slot " + d, Level: msg.LevelInfo}
		}
	}
	return m.navigate(p.Kind(), favs[idx])
}

func (m *Model) active() view.Page {
	if len(m.pages) == 0 {
		return nil
	}
	return m.pages[len(m.pages)-1]
}

func (m *Model) showToast(t msg.Toast) (tea.Model, tea.Cmd) {
	m.toast = &t
	m.toastToken++
	tok := m.toastToken
	return m, tea.Tick(3*time.Second, func(time.Time) tea.Msg {
		return toastExpireMsg{token: tok}
	})
}

// toastExpireMsg dismisses a toast if it is still the current one.
type toastExpireMsg struct{ token int }

// Global keybindings.
var (
	keyQuit    = key.NewBinding(key.WithKeys("q"), key.WithHelp("q", "quit"))
	keyForceQ  = key.NewBinding(key.WithKeys("ctrl+c"))
	keyCommand = key.NewBinding(key.WithKeys(":"), key.WithHelp(":", "cmd"))
	keyFilter  = key.NewBinding(key.WithKeys("/"), key.WithHelp("/", "filter"))
	keyHelp    = key.NewBinding(key.WithKeys("?"), key.WithHelp("?", "help"))
	keyEsc     = key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))
)

func (m *Model) handleKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Panic screen and fatal error: only quit keys respond.
	if m.panicInfo != "" || m.fatal != nil {
		if key.Matches(k, keyQuit, keyForceQ) {
			return m, tea.Quit
		}
		if m.panicInfo != "" && k.String() == "r" {
			m.panicInfo = ""
		}
		return m, nil
	}

	// A modal confirm dialog captures all input until resolved.
	if m.confirm != nil {
		return m.handleConfirmKey(k)
	}

	// Command-line capture takes precedence over everything else.
	if m.mode != modeNone {
		return m.handleInputKey(k)
	}

	switch {
	case key.Matches(k, keyForceQ):
		return m, tea.Quit
	case m.showHelp:
		// Any key closes help.
		m.showHelp = false
		return m, nil
	case key.Matches(k, keyQuit):
		return m, tea.Quit
	case key.Matches(k, keyHelp):
		m.showHelp = true
		return m, nil
	case key.Matches(k, keyCommand):
		m.mode = modeCommand
		m.inputBuf = ""
		return m, nil
	case key.Matches(k, keyFilter):
		m.mode = modeFilter
		m.inputBuf = ""
		return m, nil
	case key.Matches(k, keyEsc):
		// A drill-in pops; otherwise esc clears an active filter.
		if len(m.pages) > 1 {
			return m.popPage()
		}
		if p := m.active(); p != nil && p.Filter() != "" {
			p.SetFilter("")
		}
		return m, nil
	case isDigitKey(k):
		return m.jumpNamespace(k.String())
	default:
		return m, m.routeToPage(k)
	}
}

func isDigitKey(k tea.KeyPressMsg) bool {
	s := k.String()
	return len(s) == 1 && s[0] >= '0' && s[0] <= '9'
}

// handleConfirmKey resolves an active confirm dialog: y/enter runs the action,
// anything else cancels.
func (m *Model) handleConfirmKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	action := m.confirm.action
	switch k.String() {
	case "y", "Y", "enter":
		m.confirm = nil
		if action != nil {
			return m, func() tea.Msg { return action() }
		}
		return m, nil
	default: // n, N, esc, or any other key cancels
		m.confirm = nil
		return m, nil
	}
}

// handleInputKey drives the command line while typing after ":" or "/".
func (m *Model) handleInputKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "esc":
		if m.mode == modeFilter {
			if p := m.active(); p != nil {
				p.SetFilter("")
			}
		}
		m.mode = modeNone
		m.inputBuf = ""
		return m, nil
	case "enter":
		buf := m.inputBuf
		mode := m.mode
		m.mode = modeNone
		m.inputBuf = ""
		if mode == modeCommand {
			return m.runCommand(buf)
		}
		// Filter is already applied live; enter just commits it.
		return m, nil
	case "backspace":
		if len(m.inputBuf) > 0 {
			m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
		}
		if m.mode == modeFilter {
			if p := m.active(); p != nil {
				p.SetFilter(m.inputBuf)
			}
		}
		return m, nil
	default:
		if s := k.String(); len([]rune(s)) == 1 {
			m.inputBuf += s
			if m.mode == modeFilter {
				if p := m.active(); p != nil {
					p.SetFilter(m.inputBuf)
				}
			}
		}
		return m, nil
	}
}

// runCommand parses a ":" command and dispatches it.
func (m *Model) runCommand(buf string) (tea.Model, tea.Cmd) {
	cmd := parseCommand(buf)
	switch cmd.verb {
	case "quit":
		return m, tea.Quit
	case "nav":
		if kind, ok := view.ResolveKind(cmd.kind); ok {
			return m.navigate(kind, cmd.namespace)
		}
		return m, func() tea.Msg {
			return msg.Toast{Text: "unknown resource: " + cmd.kind, Level: msg.LevelError}
		}
	case "ctx":
		if cmd.arg == "" {
			return m, func() tea.Msg {
				return msg.Toast{Text: "usage: :ctx <context-name>", Level: msg.LevelInfo}
			}
		}
		return m.switchContext(cmd.arg)
	default:
		return m, nil
	}
}
