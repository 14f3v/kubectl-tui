// Package app is the root Bubble Tea model. It owns the terminal (alt-screen,
// title), the page stack, the command line and overlays, the global keymap, and
// panic recovery. It routes every message: key and paste input go to the command
// line or the active page; engine snapshots and action results flow to the page
// or the chrome. All IO happens in commands or engine goroutines, never in
// Update.
package app

import (
	"context"
	"errors"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"strings"
	"time"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action"
	"github.com/14f3v/kubectl-tui/internal/action/apply"
	"github.com/14f3v/kubectl-tui/internal/action/dynbrowse"
	"github.com/14f3v/kubectl-tui/internal/action/editor"
	"github.com/14f3v/kubectl-tui/internal/action/explain"
	"github.com/14f3v/kubectl-tui/internal/clusters"
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

// Launch is how kubetui starts.
type Launch int

const (
	// LaunchDirect connects to Config.Start at once: a flag named the cluster.
	LaunchDirect Launch = iota
	// LaunchPicker opens the cluster picker; nothing connects until a pick.
	LaunchPicker
)

// Config parameterizes the root model.
type Config struct {
	Sink   engine.Sink
	Config config.Config
	Launch Launch
	// Start is the cluster a direct launch connects to. Its context is pinned by
	// the caller, so a reconnect never follows the file's current-context
	// elsewhere.
	Start     k8s.ClusterRef
	StartKind string
	// StartKindExplicit is true when the user chose StartKind (-kind), even as
	// "pods", rather than leaving it to the default.
	StartKindExplicit bool
	// Store keeps pasted kubeconfigs and state.yaml; nil when it could not be
	// opened, with StoreErr saying why.
	Store    *clusters.Store
	StoreErr string
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

	store *clusters.Store // nil: paste, load-file and saved state are off

	sess  *k8s.Session
	ref   k8s.ClusterRef // the cluster the Session connects to; at a direct launch, the one being connected to
	pages []view.Page    // stack; top is active
	fatal error          // an internal error the UI cannot recover from

	// A switch builds the new Session while the current one keeps running, and
	// swaps it in only once it is ready. Each bootstrap carries a token; a result
	// whose token is not the one in flight is stale and its Session is disposed.
	bootToken  int                // increments per bootstrap
	bootCancel context.CancelFunc // cancels the in-flight version probe
	connecting *k8s.ClusterRef    // non-nil while a bootstrap is in flight

	// Where the user is in the current cluster, kept for state.yaml. at changes
	// with every view or namespace change; unsaved says it has not been written
	// since. It is written saveDelay after the last change (saveToken
	// debounces, like toastToken), at once when a connect replaces the
	// cluster, and by FlushState on exit. A failed write of the cluster on
	// screen marks it unsaved again, so the next change, a switch or the exit
	// flush tries it again.
	at        position
	unsaved   bool
	saveToken int
	// saveFailShown is set once a failed state.yaml write has been toasted:
	// every view change retries it, and one toast per run is enough.
	saveFailShown bool
	// startKindUsed is set once the first connect has opened its page: an
	// explicit -kind applies to that one only.
	startKindUsed bool

	width, height int

	mode     inputMode
	inputBuf string
	cmdSel   int // selected index in the command palette (command mode)

	// Filter-mode suggestions (dropdown of candidate values for the last term).
	suggest       []string // candidate values for the current term
	suggestPrefix string   // head + !/col: affixes to re-prepend to a chosen value
	suggestSel    int      // highlighted suggestion, or -1 for none

	toast      *msg.Toast
	toastToken int
	showHelp   bool
	confirm    *confirmState
	prompt     *promptState
	paste      *pasteBox // the paste-a-kubeconfig box; nil when closed

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
// prefilled is true while buf still holds the request's Initial value untouched.
type promptState struct {
	title     string
	label     string
	buf       string
	prefilled bool
	errMsg    string
	validate  func(string) error
	action    func(string) tea.Msg
}

// New builds the root model.
func New(cfg Config) *Model {
	theme := style.New(style.AccentPreset(cfg.Config.Accent), cfg.Config.DensityValue())
	if cfg.StartKind == "" {
		cfg.StartKind = "pods"
	}
	return &Model{
		cfg:   cfg,
		theme: theme,
		sink:  cfg.Sink,
		store: cfg.Store,
		ref:   cfg.Start,
		gate:  action.NewTerminalGate(),
	}
}

// Init starts the launch: a direct launch connects at once, a picker launch
// opens the cluster picker and lists the clusters.
func (m *Model) Init() tea.Cmd {
	if m.cfg.Launch == LaunchPicker {
		return m.launchPicker(nil)
	}
	return m.connect(m.ref)
}

// launchPicker puts the cluster picker on the empty stack, as the screen to pick
// the first cluster from, and lists the clusters with the cursor on sel, if set.
// No row is marked connected: there is no Session yet.
func (m *Model) launchPicker(sel *k8s.ClusterRef) tea.Cmd {
	_, push := m.pushPage(view.NewClusterPicker(m.theme, nil, true))
	s := m.store
	return tea.Batch(push, func() tea.Msg {
		rows, err := clusters.Collect(s)
		return launchRows{loaded: view.ClustersLoaded{Rows: rows, Err: err, Select: sel}}
	})
}

// launchRows is the launch picker's first ClustersLoaded. Only these rows may
// open the paste box: a later reload with none (after removing the last one,
// say) leaves the user where they are.
type launchRows struct{ loaded view.ClustersLoaded }

// onLaunchRows hands the launch picker its rows. With none to pick and a store
// to save into, it opens the paste box: on a machine with no kubeconfig that is
// the one thing to do, and it replaces the old "could not connect" dead end.
func (m *Model) onLaunchRows(l view.ClustersLoaded) (tea.Model, tea.Cmd) {
	l.NoStore = m.noStoreReason()
	route := m.routeToPage(l)
	switch {
	case len(l.Rows) > 0, l.Err != nil, m.store == nil, m.picker() == nil:
		return m, route
	case m.paste != nil, m.prompt != nil, m.confirm != nil:
		return m, route // the user is already doing something else
	}
	mdl, open := m.openPasteBox("")
	return mdl, tea.Batch(route, open)
}

// sessionOptions are the Session settings that come from the config rather than
// from the cluster being connected to.
func (m *Model) sessionOptions() k8s.Options {
	return k8s.Options{ReadOnly: m.cfg.Config.ReadOnly, Sink: m.sink}
}

// connect starts a bootstrap for ref, cancelling any already in flight. The
// current Session, if there is one, keeps running until the result arrives.
func (m *Model) connect(ref k8s.ClusterRef) tea.Cmd {
	m.cancelConnect()
	m.bootToken++
	ctx, cancel := context.WithCancel(context.Background())
	m.bootCancel, m.connecting = cancel, &ref
	if p := m.picker(); p != nil {
		p.SetConnecting(&ref)
	}
	return bootstrapCmd(ctx, m.bootToken, ref, m.sessionOptions(), m.store)
}

// cancelConnect cancels the bootstrap in flight, if any, and clears connecting.
// Its result, still on its way, is then stale (see isCurrent) and is disposed on
// arrival.
func (m *Model) cancelConnect() {
	if m.bootCancel != nil {
		m.bootCancel()
	}
	m.bootCancel, m.connecting = nil, nil
	if p := m.picker(); p != nil {
		p.SetConnecting(nil)
	}
}

// isCurrent reports whether a bootstrap result belongs to the bootstrap in
// flight, rather than to one a later pick or a cancel replaced.
func (m *Model) isCurrent(token int) bool {
	return m.connecting != nil && token == m.bootToken
}

// bootstrapCmd builds a Session for ref off the UI goroutine and reports the
// result tagged with token. The Session's lifetime is its own (its parent is
// Background, so it outlives this command); ctx bounds only the version probe,
// which is the one slow step, so a newer pick can cut it short.
//
// For a pasted ref the store supplies the login commands the user approved when
// saving it, and for every ref what state.yaml remembers of it (the view and
// namespace to restore); both reads are file IO, so they run here and not in
// Update. The entry is read last, as late as it can be.
func bootstrapCmd(ctx context.Context, token int, ref k8s.ClusterRef, opts k8s.Options, store *clusters.Store) tea.Cmd {
	return func() tea.Msg {
		if ref.Source == k8s.SourcePasted && store != nil {
			opts.ApprovedExec = approvedExec(store, ref)
		}
		sess, err := k8s.NewSession(context.Background(), ref, opts)
		if err != nil {
			return msg.SessionError{Err: err, Token: token, Ref: ref}
		}
		_ = sess.RefreshServerVersion(ctx) // best-effort; empty version is fine
		ready := msg.SessionReady{Session: sess, Token: token}
		if store != nil {
			if e := savedEntry(store, clusters.KeyFor(ref, sess.Identity.Server)); e != nil {
				ready.Saved = e
			}
		}
		return ready
	}
}

// savedEntry returns state.yaml's entry for key, or nil when it has none or
// cannot be read: the cluster then opens as one never used.
func savedEntry(store *clusters.Store, key clusters.Key) *clusters.Entry {
	entries, _ := store.Entries()
	for _, e := range entries {
		if e.Key() == key {
			return &e
		}
	}
	return nil
}

// approvedExec returns the login commands approved for a pasted ref. An
// unreadable store or an unknown entry yields none, which denies every command.
func approvedExec(store *clusters.Store, ref k8s.ClusterRef) []string {
	entries, _ := store.Entries()
	for _, e := range entries {
		if e.Source == k8s.SourcePasted && e.File == filepath.Base(ref.Path) {
			return e.ApprovedExec
		}
	}
	return nil
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

	case tea.PasteMsg:
		return m.handlePaste(t)

	case msg.SessionReady:
		if !m.isCurrent(t.Token) {
			// A later pick or a cancel replaced this bootstrap; nothing else holds
			// its Session, so dispose it rather than leak it.
			if sess, ok := t.Session.(*k8s.Session); ok {
				sess.Dispose()
			}
			return m, nil
		}
		return m.onSessionReady(t)

	case msg.SessionError:
		if !m.isCurrent(t.Token) {
			return m, nil
		}
		return m.onSessionError(t)

	case switchConfirmed:
		return m, m.connect(t.ref)

	case msg.Toast:
		return m.showToast(t)

	case toastExpireMsg:
		if t.token == m.toastToken {
			m.toast = nil
		}
		return m, nil

	case stateSaveMsg:
		if t.token != m.saveToken || !m.unsaved || m.store == nil {
			return m, nil // a later change re-armed it, or it is written already
		}
		m.unsaved = false
		return m, saveCmd(m.store, m.at)

	case stateSaveFailed:
		return m.onSaveFailed(t)

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
		m.prompt = &promptState{title: t.Title, label: t.Label, buf: t.Initial, prefilled: t.Initial != "", validate: t.Validate, action: t.Action}
		return m, nil

	case view.SwitchContextRequest:
		return m.switchContext(t.Ref)

	case view.ClustersLoaded:
		t.NoStore = m.noStoreReason()
		return m, m.routeToPage(t)

	case launchRows:
		return m.onLaunchRows(t.loaded)

	case ctxLookup:
		return m.onCtxLookup(t)

	case view.AddKubeconfigFile:
		return m.addKubeconfigFile(t.Path)

	case view.ForgetCluster:
		return m.forgetCluster(t.Row)

	case view.OpenPasteBox:
		return m.openPasteBox("")

	case view.ReviewCluster:
		return m.reviewCluster(t.Row)

	case pasteChecked:
		return m.onPasteChecked(t)

	case pasteNamed:
		return m.onPasteNamed(t)

	case pasteDiscarded:
		m.paste = nil
		return m, nil

	case pasteSaved:
		return m.onPasteSaved(t)

	case clustersChanged:
		reload := loadClustersCmd(m.store)
		if t.err == nil {
			return m, reload
		}
		_, tick := m.showToast(msg.Toast{Text: t.err.Error(), Level: msg.LevelError})
		return m, tea.Batch(tick, reload)

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

// loadClustersCmd reads every cluster source off the UI thread. With no store,
// only the default kubeconfig (and in-cluster) is listed.
func loadClustersCmd(s *clusters.Store) tea.Cmd {
	return func() tea.Msg {
		rows, err := clusters.Collect(s)
		return view.ClustersLoaded{Rows: rows, Err: err}
	}
}

// clusterPicker is the cluster picker page as the root model drives it. The
// picker only asks; the root tells it which row is connecting.
type clusterPicker interface {
	view.Page
	SetConnecting(ref *k8s.ClusterRef)
}

// beforeConnect reports whether the first cluster is still being picked or
// connected to: there is no Session yet, and the screen holds the launch picker
// or, while a direct launch connects, nothing. Then only the picker's keys, ?
// and q work, and there is no header or breadcrumb to draw.
func (m *Model) beforeConnect() bool {
	return m.sess == nil && (m.active() == nil || m.picker() != nil)
}

// picker returns the cluster picker when it is the active page, else nil.
func (m *Model) picker() clusterPicker {
	p, _ := m.active().(clusterPicker)
	return p
}

// openPicker pushes the cluster picker over the current page, unless it is
// already the active one; esc pops it. A connect already in flight shows on
// its row. Its rows arrive by a ClustersLoaded the caller sends.
func (m *Model) openPicker() (tea.Model, tea.Cmd) {
	if m.picker() != nil {
		return m, nil
	}
	cur := m.ref
	p := view.NewClusterPicker(m.theme, &cur, false)
	if m.connecting != nil {
		ref := *m.connecting
		p.SetConnecting(&ref)
	}
	return m.pushPage(p)
}

// noStoreReason says why paste, load-file and the saved state are off, or ""
// when the store is open.
func (m *Model) noStoreReason() string {
	if m.store != nil {
		return ""
	}
	why := m.cfg.StoreErr
	if why == "" {
		why = clusters.ErrNoConfigDir.Error()
	}
	return "saved clusters are off: " + why
}

// ctxLookup is every cluster row, listed to resolve ":ctx <name>".
type ctxLookup struct {
	name string
	rows []clusters.Row
	err  error
}

// lookupClusterCmd lists every cluster off the UI thread, for ":ctx <name>".
func lookupClusterCmd(s *clusters.Store, name string) tea.Cmd {
	return func() tea.Msg {
		rows, err := clusters.Collect(s)
		return ctxLookup{name: name, rows: rows, err: err}
	}
}

// onCtxLookup resolves ":ctx <name>" against every cluster's display name. One
// match does what enter on its row would (view.PickMsg, whose answer comes back
// through Update, port-forward confirm included); several open the picker on the
// first; none says so.
func (m *Model) onCtxLookup(t ctxLookup) (tea.Model, tea.Cmd) {
	if t.err != nil {
		return m.showToast(msg.Toast{Text: "list clusters: " + t.err.Error(), Level: msg.LevelError})
	}
	matches := clusters.Match(t.rows, t.name)
	switch len(matches) {
	case 0:
		return m.showToast(msg.Toast{Text: fmt.Sprintf("no cluster named %q", t.name), Level: msg.LevelWarn})
	case 1:
		var cur *k8s.ClusterRef
		if m.sess != nil {
			cur = &m.ref
		}
		// Decided here, not in the Cmd, so the Cmd never reads m.ref.
		out := view.PickMsg(matches[0], cur)
		return m, func() tea.Msg { return out }
	}
	mdl, push := m.openPicker()
	first := matches[0].Ref
	loaded := view.ClustersLoaded{Rows: t.rows, NoStore: m.noStoreReason(), Select: &first}
	return mdl, tea.Batch(push, m.routeToPage(loaded))
}

// clustersChanged is the result of a Cmd that changed the saved clusters. The
// picker reloads its rows either way; an error is also toasted.
type clustersChanged struct{ err error }

// addKubeconfigFile remembers the kubeconfig file the user named with f in the
// picker.
func (m *Model) addKubeconfigFile(path string) (tea.Model, tea.Cmd) {
	switch {
	case strings.TrimSpace(path) == "":
		return m, nil
	case m.store == nil:
		return m.showToast(msg.Toast{Text: m.noStoreReason(), Level: msg.LevelWarn})
	}
	return m, addFileCmd(m.store, path)
}

// addFileCmd remembers a kubeconfig file exactly as --kubeconfig does: one file
// entry per context, by absolute path, so the entry still works when kubetui
// starts in another folder. The file is not copied, so later edits to it are
// picked up. It must load as a kubeconfig with at least one context.
func addFileCmd(s *clusters.Store, path string) tea.Cmd {
	return func() tea.Msg {
		abs, err := filepath.Abs(path)
		if err != nil {
			return clustersChanged{err: err}
		}
		ctxs, _, err := k8s.ListContexts(abs)
		switch {
		case errors.Is(err, fs.ErrNotExist):
			return clustersChanged{err: fmt.Errorf("%s: no such file", abs)}
		case err != nil: // client-go's error names the file
			return clustersChanged{err: fmt.Errorf("%w: %s", clusters.ErrNotKubeconfig, clusters.SafeLoadError(err))}
		case len(ctxs) == 0:
			return clustersChanged{err: fmt.Errorf("%s: %w: it has no contexts", abs, clusters.ErrNotKubeconfig)}
		}
		for _, c := range ctxs {
			ref := k8s.ClusterRef{Source: k8s.SourceFile, Path: abs, Context: c.Name}
			server := c.Server // shown on the row if the file goes missing
			if err := s.Update(clusters.KeyFor(ref, ""), func(e *clusters.Entry) { e.Server = server }); err != nil {
				return clustersChanged{err: err}
			}
		}
		return clustersChanged{}
	}
}

// forgetCluster removes a picker row's source, unless the connected cluster (or
// the one connecting) comes from it: deleting a pasted file under a live Session
// would break it, and an oidc token refresh would write the file back
// unreviewed. Rows from the default kubeconfig are never removed.
func (m *Model) forgetCluster(r clusters.Row) (tea.Model, tea.Cmd) {
	sameSource := func(ref k8s.ClusterRef) bool { return ref.Source == r.Ref.Source && ref.Path == r.Ref.Path }
	switch {
	case r.Source != k8s.SourcePasted && r.Source != k8s.SourceFile:
		return m.showToast(msg.Toast{Text: clusters.Sanitize(r.Label) + " comes from your kubeconfig; kubetui doesn't edit it", Level: msg.LevelInfo})
	case m.store == nil:
		return m.showToast(msg.Toast{Text: m.noStoreReason(), Level: msg.LevelWarn})
	case (m.sess != nil && sameSource(m.ref)) || (m.connecting != nil && sameSource(*m.connecting)):
		return m.showToast(msg.Toast{Text: "disconnect first: switch to another cluster", Level: msg.LevelWarn})
	}
	return m, forgetCmd(m.store, r)
}

// forgetCmd removes what a row came from. A pasted file is deleted along with
// the record of every context in it. A loaded file is forgotten, every context
// of it, and the file itself is left alone. A record left behind by a failed
// write is harmless: the picker drops records whose pasted file is gone.
func forgetCmd(s *clusters.Store, r clusters.Row) tea.Cmd {
	return func() tea.Msg {
		entries, err := s.Entries()
		if err != nil {
			return clustersChanged{err: err}
		}
		ours := func(e clusters.Entry) bool { return e.Source == k8s.SourceFile && e.Path == r.Ref.Path }
		if r.Source == k8s.SourcePasted {
			if err := s.DeletePaste(r.Key.File); err != nil && !errors.Is(err, fs.ErrNotExist) {
				return clustersChanged{err: err}
			}
			ours = func(e clusters.Entry) bool { return e.Source == k8s.SourcePasted && e.File == r.Key.File }
		}
		for _, e := range entries {
			if !ours(e) {
				continue
			}
			if err := s.Remove(e.Key()); err != nil {
				return clustersChanged{err: err}
			}
		}
		return clustersChanged{}
	}
}

// popPage backs out of a drill-in. The base page is never popped.
func (m *Model) popPage() (tea.Model, tea.Cmd) {
	if len(m.pages) <= 1 {
		return m, nil
	}
	top := m.pages[len(m.pages)-1]
	if _, ok := top.(clusterPicker); ok {
		// Closing the picker stays on the current cluster: the connect it started
		// is cut short, and its result is disposed when it arrives.
		m.cancelConnect()
	}
	top.OnLeave()
	m.pages = m.pages[:len(m.pages)-1]
	// A page that drives its own refresh tick lost that chain while it was buried:
	// ticks go to the top page only, so its own message was delivered to the
	// drill-in and dropped. Give it a chance to re-arm now that it is visible.
	if r, ok := m.active().(view.Resumable); ok {
		return m, r.OnResume()
	}
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

// onSessionReady swaps in the Session the current bootstrap built. The previous
// one, kept running through the connect, is disposed now, which closes its
// port-forwards; its pages and dialogs go with it, and late snapshots from its
// engine carry view ids no new page holds, so they change nothing.
func (m *Model) onSessionReady(t msg.SessionReady) (tea.Model, tea.Cmd) {
	m.cancelConnect() // the probe is done; this releases its context
	sess, ok := t.Session.(*k8s.Session)
	if !ok {
		m.fatal = fmt.Errorf("internal: session type mismatch")
		return m, nil
	}
	for _, p := range m.pages {
		p.OnLeave()
	}
	m.pages = nil
	m.confirm, m.prompt = nil, nil // they would act on the disposed Session
	if m.sess != nil {
		m.sess.Dispose()
	}
	m.metricsOK, m.clusterCPU, m.clusterMem = false, 0, 0
	m.sess, m.ref = sess, sess.Ref
	m.poller = metrics.NewPoller(sess.CS, sess.Metrics, sess.Disco, m.sink)
	go m.poller.Run(sess.Context())

	// Open where the user left this cluster. Every connect comes through here:
	// the first, a switch, and the reconnect behind the auth banner's r.
	key := clusters.KeyFor(sess.Ref, sess.Identity.Server)
	saved, _ := t.Saved.(*clusters.Entry)
	kind, ns := m.startPosition(m.remembered(key, saved))
	m.startKindUsed = true
	var writes []position
	if m.unsaved && m.at.key != key {
		writes = append(writes, m.at) // where the user was in the cluster left behind
	}
	m.at = position{key: key, server: sess.Identity.Server}
	mdl, nav := m.navigate(kind, ns)
	if m.store == nil {
		return mdl, nav
	}
	// Write the connect now rather than after the debounce, so the picker's
	// last-used order has it: its time, and the position it opened on.
	m.unsaved = false
	return mdl, tea.Batch(nav, saveCmd(m.store, append(writes, m.at)...))
}

// position is where the user is in one cluster, as state.yaml records it.
type position struct {
	key    clusters.Key
	server string // a file's server is kept for its row when the file goes missing
	// kind and ns are the last navigate's; kind is "" until a page opened.
	kind, ns string
}

// saveDelay is how long the view must stay put before its position is written.
const saveDelay = 500 * time.Millisecond

// stateSaveMsg is the debounce tick of recordPosition. Only the tick of the
// latest change, the one whose token is current, writes.
type stateSaveMsg struct{ token int }

// stateSaveFailed reports a write of saveCmd that failed: the last one, which
// is the cluster on screen's when that one failed.
type stateSaveFailed struct {
	key clusters.Key
	err error
}

// recordPosition notes kind (canonical) and namespace ns as where the user is
// in the current cluster and returns the debounce tick that writes it. With no
// store the position is still kept in memory, which a reconnect returns to,
// and there is nothing to write.
func (m *Model) recordPosition(kind, ns string) tea.Cmd {
	m.at.kind, m.at.ns = kind, ns
	if m.store == nil {
		return nil
	}
	m.unsaved = true
	m.saveToken++
	tok := m.saveToken
	return tea.Tick(saveDelay, func(time.Time) tea.Msg { return stateSaveMsg{token: tok} })
}

// FlushState writes the position recorded since the last write, or whose
// write failed. main calls it once the program has stopped, so a quit inside
// the debounce still saves where the user was, and prints the error when
// state.yaml still cannot be written.
func (m *Model) FlushState() error {
	if m.store == nil || !m.unsaved {
		return nil
	}
	if err := writePosition(m.store, m.at); err != nil {
		return err
	}
	m.unsaved = false
	return nil
}

// onSaveFailed handles a state.yaml write that failed. The first failure of the
// run is toasted; later ones would repeat it on every view change. When the
// failed write was the cluster on screen's, its position is pending again, so
// FlushState retries it at exit and reports the error if it fails there too.
// A failure for a cluster since left behind has nothing left to retry.
func (m *Model) onSaveFailed(t stateSaveFailed) (tea.Model, tea.Cmd) {
	if t.key == m.at.key {
		m.unsaved = true
	}
	if m.saveFailShown {
		return m, nil
	}
	m.saveFailShown = true
	return m.showToast(msg.Toast{Text: "saving state.yaml: " + t.err.Error(), Level: msg.LevelWarn})
}

// remembered is the record to restore the cluster key names from: the entry
// the bootstrap read, except on a reconnect to the cluster on screen, where the
// position in memory is newer than any state.yaml may hold yet (its write can
// still be on its way, or debounced).
func (m *Model) remembered(key clusters.Key, saved *clusters.Entry) *clusters.Entry {
	if key != m.at.key || m.at.kind == "" {
		return saved
	}
	// LastUsed marks it used, so its namespace is restored too: it is the
	// cluster on screen.
	return &clusters.Entry{Kind: m.at.kind, Namespace: m.at.ns, LastUsed: time.Now()}
}

// startPosition is the view and namespace a cluster opens on, given what
// state.yaml remembers of it (e may be nil). The view is an explicit -kind on
// the run's first connect, else the remembered one while it is still a
// first-class kind, else pods. The namespace is the remembered one once the
// cluster has been used ("" there is all namespaces, a choice like any other),
// else the context's, else all namespaces.
func (m *Model) startPosition(e *clusters.Entry) (kind, ns string) {
	kind = "pods"
	switch {
	case m.cfg.StartKindExplicit && !m.startKindUsed:
		kind = m.cfg.StartKind
	case e != nil:
		if k, ok := view.ResolveKind(e.Kind); ok {
			kind = k
		}
	}
	switch {
	case e != nil && !e.LastUsed.IsZero():
		ns = e.Namespace
	case m.sess != nil:
		ns = m.sess.Identity.Namespace
	}
	return kind, ns
}

// saveCmd writes positions to state.yaml off the UI thread, in order. Each is
// tried even when an earlier one failed. It answers stateSaveFailed for the
// last that failed, or nothing when all were written.
func saveCmd(s *clusters.Store, ps ...position) tea.Cmd {
	return func() tea.Msg {
		var failed tea.Msg
		for _, p := range ps {
			if err := writePosition(s, p); err != nil {
				failed = stateSaveFailed{key: p.key, err: err}
			}
		}
		return failed
	}
}

// writePosition records that the cluster p names was just used, and on which
// view and namespace. A position with no page yet (an unknown -kind opened
// none) keeps the remembered view and namespace.
func writePosition(s *clusters.Store, p position) error {
	return s.Update(p.key, func(e *clusters.Entry) {
		e.LastUsed = time.Now()
		if p.key.Source == k8s.SourceFile {
			e.Server = p.server
		}
		if p.kind != "" {
			e.Kind, e.Namespace = p.kind, p.ns
		}
	})
}

// onSessionError reports a failed bootstrap; it is never a dead end. A switch
// that fails leaves the current Session as it was. The picker, when open, stays
// and shows why on the row that failed, whether or not a Session is up.
// Otherwise a toast says why, and a direct launch, which has no Session to stay
// on, opens the launch picker on that row: the flags named a cluster that does
// not connect, and the picker lists every one that might.
//
// The error is shown through clusters.SafeLoadError: client-go prints a
// kubeconfig's duplicate names with the whole list, tokens included, and the
// error quotes raw names from the file.
func (m *Model) onSessionError(t msg.SessionError) (tea.Model, tea.Cmd) {
	m.cancelConnect()
	ref, _ := t.Ref.(k8s.ClusterRef)
	why := clusters.SafeLoadError(t.Err)
	failed := view.ClusterConnectFailed{Ref: ref, Err: why}
	if m.picker() != nil {
		return m, m.routeToPage(failed)
	}
	name := clusters.Sanitize(ref.Context)
	if name == "" {
		name = string(ref.Source) // an in-cluster ref names no context
		if ref.Path != "" {
			name = clusters.Sanitize(filepath.Base(ref.Path)) // a file with no current-context to pin
		}
	}
	_, tick := m.showToast(msg.Toast{Text: "connect " + name + ": " + why, Level: msg.LevelError})
	if m.sess != nil {
		return m, tick
	}
	// The toast stays the only word on it when the ref has no row, such as a
	// -context the kubeconfig does not have.
	open := m.launchPicker(&ref)
	route := m.routeToPage(failed) // to the picker just opened
	return m, tea.Batch(tick, open, route)
}

// navigate replaces the page stack with a fresh page for kind (an alias such as
// -kind deploy works too), scoped to namespace ("" = all namespaces). Every
// change of base view or namespace comes through here, so this is where the
// position is recorded, by its canonical kind: several base pages report an
// empty Kind().
func (m *Model) navigate(kind, namespace string) (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	if k, ok := view.ResolveKind(kind); ok {
		kind = k
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
	return m, tea.Batch(page.Init(), page.OnEnter(), m.recordPosition(kind, namespace))
}

// switchConfirmed is the yes to a switch that closes port-forwards; it starts the
// connect the confirm held back.
type switchConfirmed struct{ ref k8s.ClusterRef }

// switchContext starts a bootstrap for ref; it does not dispose the current
// Session, which stays up until the new one is ready (onSessionReady). The
// switch will close every open port-forward, so with any open it asks first.
func (m *Model) switchContext(ref k8s.ClusterRef) (tea.Model, tea.Cmd) {
	if m.sess != nil && m.sess.Forwards != nil {
		if n := m.sess.Forwards.Count(); n > 0 {
			return m, func() tea.Msg {
				return view.ConfirmRequest{
					Title:  "switch cluster",
					Prompt: fmt.Sprintf("Switching closes %d port-forwards.", n),
					Action: func() tea.Msg { return switchConfirmed{ref: ref} },
				}
			}
		}
	}
	return m, m.connect(ref)
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
	// keyRetry rebuilds a view that stopped on a terminal error. Both cases are
	// accepted because the banner has always told the user to press "r" and the
	// panic screen uses "r" too; "R" is kept for muscle memory from the uppercase
	// action keys. There is no collision with the page's rollout binding: this only
	// fires while a banner is up, and then the page is not rendered at all.
	keyRetry = key.NewBinding(key.WithKeys("r", "R"), key.WithHelp("r", "retry"))
)

// bannerUp reports whether the active page is showing a terminal-error banner
// instead of its content. Stale is deliberately excluded: a stale view still
// renders its last-good rows and stays fully interactive.
func (m *Model) bannerUp() bool {
	p := m.active()
	if p == nil {
		return false
	}
	s := p.Summary()
	return s.Phase == engine.PhaseTerminal && s.Error != nil
}

// retryActive rebuilds the failed view. For an auth failure the whole Session is
// re-bootstrapped, because credentials are session-wide — every other kind is
// just as dead, and the exec plugin (tsh and friends) needs to be re-run to mint
// a fresh token. Anything else is scoped to the one kind that failed, so an
// RBAC-forbidden resource does not tear down the working views around it.
func (m *Model) retryActive() (tea.Model, tea.Cmd) {
	p := m.active()
	if p == nil || m.sess == nil {
		return m, nil
	}
	if s := p.Summary(); s.Error != nil && s.Error.Class == engine.ClassAuth {
		return m.reconnect()
	}
	if kind := p.Kind(); kind != "" {
		m.sess.Engine.StopKind(kind)
	}
	return m, tea.Batch(p.OnEnter(), toastCmd("retrying "+p.Title(), msg.LevelInfo))
}

// reconnect builds a fresh Session for the same ClusterRef, which re-runs the exec
// credential plugin, and swaps it in once it connects. The ref pins the context
// by name, so a `kubectl config use-context` run elsewhere since the connect
// cannot move the user to another cluster.
func (m *Model) reconnect() (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	// Reconnecting tears down every port-forward with it, so say so first, in
	// its own words; a yes connects without switchContext asking again.
	if n := m.sess.Forwards.Count(); n > 0 {
		ref := m.ref // the confirm's action runs as a Cmd, off the UI goroutine
		return m, func() tea.Msg {
			return view.ConfirmRequest{
				Title:  "reconnect",
				Prompt: fmt.Sprintf("re-authenticate and rebuild the session? this closes %d active port-forward(s)", n),
				Action: func() tea.Msg { return switchConfirmed{ref: ref} },
			}
		}
	}
	return m.switchContext(m.ref)
}

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

	// The paste box takes every key, global ones included: they are text to it.
	// It comes after the prompt and the confirm because it opens both on top of
	// itself (the name prompt, the esc confirm), and they need the keys.
	if m.paste != nil {
		return m.handlePasteBoxKey(k)
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
	case m.beforeConnect():
		// The launch picker gets every other key: the command line, the filter,
		// digit jumps and esc all need a cluster.
		return m, m.routeToPage(k)
	case key.Matches(k, keyCommand):
		m.mode = modeCommand
		m.inputBuf = ""
		m.cmdSel = 0
		return m, nil
	case key.Matches(k, keyFilter):
		m.mode = modeFilter
		m.inputBuf = ""
		m.recomputeSuggest()
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
	case m.bannerUp() && key.Matches(k, keyRetry):
		return m.retryActive()
	case m.bannerUp():
		// The page is not rendered behind a terminal banner, so forwarding keys to
		// it would act on rows the user cannot see — including the destructive ones.
		return m, nil
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
		m.prompt.prefilled = false
		m.prompt.errMsg = ""
		return m, nil
	case "ctrl+u":
		m.prompt.buf = ""
		m.prompt.prefilled = false
		m.prompt.errMsg = ""
		return m, nil
	default:
		// k.Text carries the printable character(s), including " " for space, so a
		// prompt can accept spaces (e.g. cp's "localPath remotePath").
		m.promptInsert(k.Text)
		return m, nil
	}
}

// promptInsert appends typed or pasted text to the prompt buffer. Any edit clears
// the last validation error, since it no longer describes what is in the field.
func (m *Model) promptInsert(s string) {
	if s == "" {
		return
	}
	m.prompt.buf += s
	m.prompt.prefilled = false
	m.prompt.errMsg = ""
}

// promptPaste is promptInsert for a paste, except that a paste replaces a value
// the prompt opened with and nobody has touched yet. Every prefilled value is a
// whole answer (a log path, an image, a replica count), so a pasted one belongs
// in its place: appended, Explorer's "Copy as path" would turn the log-save
// default into `%TEMP%\web.log"C:\Users\me\web.log"`.
func (m *Model) promptPaste(s string) {
	if s != "" && m.prompt.prefilled {
		m.prompt.buf = ""
	}
	m.promptInsert(s)
}

// handlePaste delivers a bracketed paste. Bubble Tea turns bracketed paste on by
// default, so text pasted into the terminal (Ctrl+V, Cmd+V, right-click) arrives
// as one PasteMsg rather than as key presses, and the key handlers never see it.
// It goes to whichever text input is open, exactly as if it had been typed. With
// none open it is gated like a key: it cannot answer a confirm dialog, and it
// does not reach a page that is hidden behind help or an error banner.
func (m *Model) handlePaste(p tea.PasteMsg) (tea.Model, tea.Cmd) {
	switch {
	case m.panicInfo != "" || m.fatal != nil:
		return m, nil
	case m.prompt != nil:
		m.promptPaste(singleLine(p.Content))
		return m, nil
	case m.confirm != nil:
		return m, nil
	case m.paste != nil:
		return m.handlePasteBoxPaste(p)
	case m.mode != modeNone:
		m.inputInsert(singleLine(p.Content))
		return m, nil
	case m.showHelp || m.bannerUp():
		return m, nil
	default:
		return m, m.routeToPage(p)
	}
}

// singleLine flattens pasted text for a one-line input. Every line break becomes
// one space — CRLF, a lone CR and a lone LF alike, because terminals differ in
// which they send — so a pasted newline can never act as enter and submit a
// half-written prompt or command. Tabs become a space too, and other control
// characters are dropped, which defuses an escape sequence in the clipboard
// instead of writing it to the terminal when the buffer is drawn. Quotes are left
// alone: the path prompts strip them on submit, and elsewhere they are just text.
func singleLine(s string) string {
	s = strings.ReplaceAll(s, "\r\n", "\n")
	return strings.Map(func(r rune) rune {
		switch {
		case r == '\r' || r == '\n' || r == '\t':
			return ' '
		case unicode.IsControl(r):
			return -1
		}
		return r
	}, s)
}

// recomputeSuggest refreshes the filter suggestion list for the current buffer's
// last term and clears the highlight. Called whenever the filter text changes (a
// typed character, backspace, or opening the filter) — but not on suggestion
// navigation, which keeps the list stable while the highlight moves.
func (m *Model) recomputeSuggest() {
	m.suggest, m.suggestPrefix, m.suggestSel = nil, "", -1
	if m.mode != modeFilter {
		return
	}
	if p := m.active(); p != nil {
		if fc, ok := p.(view.FilterCompleter); ok {
			prefix, _, matches := fc.FilterMatches(m.inputBuf)
			m.suggestPrefix, m.suggest = prefix, matches
		}
	}
}

// selectSuggest moves the suggestion highlight by delta (wrapping) and fills the
// buffer with the highlighted value, re-applying the filter live. From no
// selection, delta>0 highlights the first entry and delta<0 the last.
func (m *Model) selectSuggest(delta int) {
	n := len(m.suggest)
	if n == 0 {
		return
	}
	switch {
	case m.suggestSel < 0 && delta > 0:
		m.suggestSel = 0
	case m.suggestSel < 0:
		m.suggestSel = n - 1
	default:
		m.suggestSel = (m.suggestSel + delta + n) % n
	}
	m.inputBuf = m.suggestPrefix + m.suggest[m.suggestSel]
	if p := m.active(); p != nil {
		p.SetFilter(m.inputBuf)
	}
}

// handleInputKey drives the command line while typing after ":" or "/". In
// command mode it also drives the command palette (↑/↓ select, Tab complete);
// in filter mode ↑/↓/Tab move through the suggestion dropdown.
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

	// Suggestion navigation in filter mode: ↑/↓ and Tab move through the candidate
	// values, filling the buffer with the highlighted one.
	if m.mode == modeFilter {
		switch k.String() {
		case "up", "ctrl+p":
			m.selectSuggest(-1)
			return m, nil
		case "down", "ctrl+n", "tab":
			m.selectSuggest(1)
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
		// Drop the last rune, not the last byte: a non-ASCII character (typed on a
		// non-US layout or pasted) is several bytes, and cutting one would leave
		// invalid UTF-8 in the buffer and the filter.
		_, size := utf8.DecodeLastRuneInString(m.inputBuf)
		m.inputBuf = m.inputBuf[:len(m.inputBuf)-size]
		m.cmdSel = 0
		if m.mode == modeFilter {
			if p := m.active(); p != nil {
				p.SetFilter(m.inputBuf)
			}
			m.recomputeSuggest()
		}
		return m, nil
	default:
		// k.Text is the printable text the key produced (" " for space, "" for
		// non-text keys like arrows). Using it rather than String() — which renders
		// space as "space" — lets the command/filter line accept spaces, which
		// multi-term filters and "kind namespace" commands need.
		m.inputInsert(k.Text)
		return m, nil
	}
}

// inputInsert appends typed or pasted text to the command line. The palette
// selection resets because the match list changed under it, and in filter mode
// the filter is re-applied live and its suggestions recomputed.
func (m *Model) inputInsert(s string) {
	if s == "" {
		return
	}
	m.inputBuf += s
	m.cmdSel = 0
	if m.mode == modeFilter {
		if p := m.active(); p != nil {
			p.SetFilter(m.inputBuf)
		}
		m.recomputeSuggest()
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
		if cmd.arg != "" {
			return m, lookupClusterCmd(m.store, cmd.arg)
		}
		if m.sess == nil {
			return m, nil
		}
		mdl, push := m.openPicker()
		return mdl, tea.Batch(push, loadClustersCmd(m.store))
	case "apply":
		return m.applyCommand()
	case "explain":
		return m.explainCommand(cmd.arg)
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

// explainCommand renders OpenAPI field documentation for :explain <kind[.field.path]>
// (e.g. :explain pod.spec.containers) in a scrollable text page. The schema fetch
// is a live discovery/OpenAPI call, so it runs off the UI thread.
func (m *Model) explainCommand(expr string) (tea.Model, tea.Cmd) {
	if m.sess == nil {
		return m, nil
	}
	if expr == "" {
		return m, toastCmd("usage: :explain <kind[.field.path]> (e.g. pod.spec.containers)", msg.LevelInfo)
	}
	sess, theme := m.sess, m.theme
	return m, func() tea.Msg {
		text, err := explain.Explain(sess.Context(), sess.Disco, expr)
		if err != nil {
			return msg.Toast{Text: "explain: " + err.Error(), Level: msg.LevelError}
		}
		return view.PushMsg{Page: view.NewTextView("explain "+expr, text, theme)}
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
	path, sum, err := editor.WriteTemp(applyTemplate(ns))
	if err != nil {
		return m, toastCmd("apply: "+err.Error(), msg.LevelError)
	}
	opened := time.Now() // the editor starts within a frame of this
	sess, theme, ro := m.sess, m.theme, m.cfg.Config.ReadOnly
	after := func(execErr error) tea.Msg {
		elapsed := time.Since(opened)
		defer os.Remove(path)
		if execErr != nil {
			return msg.Toast{Text: "editor: " + execErr.Error(), Level: msg.LevelError}
		}
		data, rerr := os.ReadFile(path)
		if rerr != nil {
			return msg.Toast{Text: "apply: " + rerr.Error(), Level: msg.LevelError}
		}
		// The template back untouched within a second means the editor never
		// waited for the user; say so rather than "no changes to apply".
		if hint := editor.QuickExitHint(elapsed); hint != "" && editor.Unchanged(data, sum) {
			return msg.Toast{Text: hint, Level: msg.LevelWarn}
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
