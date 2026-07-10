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
	"os"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action"
	"github.com/14f3v/kubectl-tui/internal/action/apply"
	"github.com/14f3v/kubectl-tui/internal/action/dynbrowse"
	"github.com/14f3v/kubectl-tui/internal/action/editor"
	"github.com/14f3v/kubectl-tui/internal/config"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/metrics"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
	"github.com/14f3v/kubectl-tui/internal/view"
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
	KubeconfigPath  string
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
	cmdSel   int // selected index in the command palette (command mode)

	toast      *msg.Toast
	toastToken int
	showHelp   bool
	confirm    *confirmState
	prompt     *promptState

	panicInfo string
}

// confirmState holds an active modal confirm dialog.
type confirmState struct {
	title  string
	prompt string
	danger bool
	action func() tea.Msg
}

// promptState holds an active modal text-input dialog (e.g. scale replicas). buf
// is the live input; errMsg shows the last validation failure until resolved.
type promptState struct {
	title    string
	label    string
	buf      string
	errMsg   string
	validate func(string) error
	action   func(string) tea.Msg
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
	return bootstrapCmd(m.sink, m.cfg.KubeconfigPath, m.cfg.ContextOverride)
}

// bootstrapCmd builds a Session off the UI goroutine and reports the result.
func bootstrapCmd(sink engine.Sink, kubeconfigPath, ctxOverride string) tea.Cmd {
	return func() tea.Msg {
		sess, err := k8s.NewSession(context.Background(), kubeconfigPath, ctxOverride, sink)
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

	case view.PromptRequest:
		m.prompt = &promptState{title: t.Title, label: t.Label, buf: t.Initial, validate: t.Validate, action: t.Action}
		return m, nil

	case view.SwitchContextRequest:
		return m.switchContext(t.Name)

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
	return m, bootstrapCmd(m.sink, m.cfg.KubeconfigPath, name)
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

	// A modal text-input prompt captures all input until resolved.
	if m.prompt != nil {
		return m.handlePromptKey(k)
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
		m.cmdSel = 0
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

// handlePromptKey drives an active text-input prompt: enter validates and runs
// the action (staying open on a validation error), esc cancels, and printable
// keys edit the single-line buffer.
func (m *Model) handlePromptKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	switch k.String() {
	case "enter":
		if m.prompt.validate != nil {
			if err := m.prompt.validate(m.prompt.buf); err != nil {
				m.prompt.errMsg = err.Error()
				return m, nil
			}
		}
		action := m.prompt.action
		val := m.prompt.buf
		m.prompt = nil
		if action != nil {
			return m, func() tea.Msg { return action(val) }
		}
		return m, nil
	case "esc":
		m.prompt = nil
		return m, nil
	case "backspace":
		if n := len(m.prompt.buf); n > 0 {
			r := []rune(m.prompt.buf)
			m.prompt.buf = string(r[:len(r)-1])
		}
		m.prompt.errMsg = ""
		return m, nil
	case "ctrl+u":
		m.prompt.buf = ""
		m.prompt.errMsg = ""
		return m, nil
	default:
		if s := k.String(); len([]rune(s)) == 1 {
			m.prompt.buf += s
			m.prompt.errMsg = ""
		}
		return m, nil
	}
}

// handleInputKey drives the command line while typing after ":" or "/". In
// command mode it also drives the command palette (↑/↓ select, Tab complete).
func (m *Model) handleInputKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	// Palette navigation is only meaningful in command mode.
	if m.mode == modeCommand {
		switch k.String() {
		case "up", "ctrl+p":
			if m.cmdSel > 0 {
				m.cmdSel--
			}
			return m, nil
		case "down", "ctrl+n":
			if m.cmdSel < len(m.commandMatches())-1 {
				m.cmdSel++
			}
			return m, nil
		case "tab":
			if matches := m.commandMatches(); len(matches) > 0 && m.cmdSel < len(matches) {
				m.inputBuf = matches[m.cmdSel].Name
				m.cmdSel = 0
			}
			return m, nil
		}
	}

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
		// Resolve the highlighted palette command BEFORE clearing the buffer, since
		// selectedCommand() filters by m.inputBuf.
		selected, hasSel := "", false
		if mode == modeCommand {
			selected, hasSel = m.selectedCommand()
		}
		m.mode = modeNone
		m.inputBuf = ""
		if mode == modeCommand {
			if hasSel {
				return m.runCommand(selected)
			}
			return m.runCommand(buf)
		}
		// Filter is already applied live; enter just commits it.
		return m, nil
	case "backspace":
		if len(m.inputBuf) > 0 {
			m.inputBuf = m.inputBuf[:len(m.inputBuf)-1]
		}
		m.cmdSel = 0
		if m.mode == modeFilter {
			if p := m.active(); p != nil {
				p.SetFilter(m.inputBuf)
			}
		}
		return m, nil
	default:
		if s := k.String(); len([]rune(s)) == 1 {
			m.inputBuf += s
			m.cmdSel = 0
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
		// Not a first-class kind — try to resolve the bare plural via discovery and
		// open it in the Table browser (e.g. :endpoints, :componentstatuses).
		return m.openResource(cmd.kind, cmd.namespace)
	case "ctx":
		if cmd.arg == "" {
			if m.sess == nil {
				return m, nil
			}
			return m.pushPage(view.NewContextPicker(m.sess, m.theme))
		}
		return m.switchContext(cmd.arg)
	case "apply":
		return m.applyCommand()
	case "crds":
		if m.sess == nil {
			return m, nil
		}
		return m.pushPage(view.NewCRDList(m.sess, m.theme, m.activeNamespace(), m.cfg.Config.ReadOnly))
	case "crdopen":
		return m.openCRD(cmd.arg)
	default:
		return m, nil
	}
}

// activeNamespace is the namespace scope of the active page ("" = all namespaces).
func (m *Model) activeNamespace() string {
	if p := m.active(); p != nil {
		return p.Namespace()
	}
	return ""
}

// openResource resolves a bare plural that isn't a first-class kind via discovery
// and opens it in the Table browser, or toasts if nothing matches. This is what
// makes core-group built-ins like :endpoints reachable by name.
func (m *Model) openResource(plural, namespace string) (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	sess, theme, ro := m.sess, m.theme, m.cfg.Config.ReadOnly
	ns := namespace
	if ns == "" {
		ns = m.activeNamespace()
	}
	return m, func() tea.Msg {
		info, err := dynbrowse.ResolveResource(sess.Context(), sess.Disco, plural)
		if err != nil {
			return msg.Toast{Text: "unknown resource: " + plural, Level: msg.LevelError}
		}
		title := info.Kind
		if info.Group != "" {
			title = info.Kind + " (" + info.Group + ")"
		}
		return view.PushMsg{Page: view.NewCRDBrowse(sess, theme, title, info.GVR(), info.Namespaced, ns, ro)}
	}
}

// openCRD resolves a fully-qualified <plural>.<group> to its served resource
// (off the UI thread) and opens the Table-protocol browse page.
func (m *Model) openCRD(token string) (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	i := strings.Index(token, ".")
	if i <= 0 || i >= len(token)-1 {
		return m, toastCmd("usage: :<plural>.<group> (e.g. certificates.cert-manager.io)", msg.LevelInfo)
	}
	plural, group := token[:i], token[i+1:]
	sess, theme, ns, ro := m.sess, m.theme, m.activeNamespace(), m.cfg.Config.ReadOnly
	return m, func() tea.Msg {
		info, err := dynbrowse.ResolvePluralGroup(sess.Context(), sess.Disco, plural, group)
		if err != nil {
			return msg.Toast{Text: err.Error(), Level: msg.LevelError}
		}
		title := info.Kind + " (" + info.Group + ")"
		return view.PushMsg{Page: view.NewCRDBrowse(sess, theme, title, info.GVR(), info.Namespaced, ns, ro)}
	}
}

// applyCommand opens $EDITOR on a blank manifest and server-side applies whatever
// the user saves (multi-doc supported). GVR is resolved from each document via a
// discovery-backed REST mapper, so it works for any kind — including CRDs.
func (m *Model) applyCommand() (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	if m.cfg.Config.ReadOnly {
		return m, toastCmd("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	ns := ""
	if p := m.active(); p != nil {
		ns = p.Namespace()
	}
	path, _, err := editor.WriteTemp(applyTemplate(ns))
	if err != nil {
		return m, toastCmd("apply: "+err.Error(), msg.LevelError)
	}
	sess, theme, ro := m.sess, m.theme, m.cfg.Config.ReadOnly
	after := func(execErr error) tea.Msg {
		defer os.Remove(path)
		if execErr != nil {
			return msg.Toast{Text: "editor: " + execErr.Error(), Level: msg.LevelError}
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return msg.Toast{Text: "apply: " + rerr.Error(), Level: msg.LevelError}
		}
		// Preview the change with a read-only dry-run before applying: compute the
		// diff and open the preview page, where `a` confirms the apply. If nothing
		// would change, skip the page and say so.
		mapper := apply.NewMapper(sess.Disco)
		results := apply.Diff(sess.Context(), sess.Dyn, mapper, data, "kubetui", ns)
		if !diffHasContent(results) {
			return msg.Toast{Text: "no changes to apply", Level: msg.LevelInfo}
		}
		return view.PushMsg{Page: view.NewDiffApply(sess, theme, ns, data, results, ro)}
	}
	return m, func() tea.Msg {
		return view.ExecRequest{Label: "apply", Process: editor.Process(path), After: after}
	}
}

// applyTemplate is the starter buffer for :apply, with guidance in comments (which
// are dropped before applying, so saving unchanged applies nothing).
func applyTemplate(ns string) string {
	if ns == "" {
		ns = "default"
	}
	return "# Write one or more Kubernetes manifests below, then save & quit to apply.\n" +
		"# Separate documents with '---'. Applied server-side (field manager: kubetui).\n" +
		"# Example:\n" +
		"# apiVersion: v1\n" +
		"# kind: ConfigMap\n" +
		"# metadata:\n" +
		"#   name: example\n" +
		"#   namespace: " + ns + "\n" +
		"# data:\n" +
		"#   key: value\n"
}

// diffHasContent reports whether a dry-run diff is worth previewing: any document
// that would change or that failed to diff. All-empty, all-nil-error means the
// apply is a no-op, so the caller can skip the preview page.
func diffHasContent(results []apply.DiffResult) bool {
	for _, r := range results {
		if r.Err != nil || strings.TrimSpace(r.Diff) != "" {
			return true
		}
	}
	return false
}

// toastCmd is a small helper for returning a toast as a command.
func toastCmd(text string, level msg.Level) tea.Cmd {
	return func() tea.Msg { return msg.Toast{Text: text, Level: level} }
}
