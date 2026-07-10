package view

import (
	"os"
	"regexp"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	appsv1 "k8s.io/api/apps/v1"
	batchv1 "k8s.io/api/batch/v1"
	corev1 "k8s.io/api/core/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"

	"github.com/14f3v/kubectl-tui/internal/action/csr"
	"github.com/14f3v/kubectl-tui/internal/action/debug"
	"github.com/14f3v/kubectl-tui/internal/action/editor"
	"github.com/14f3v/kubectl-tui/internal/action/execshell"
	"github.com/14f3v/kubectl-tui/internal/action/inspect"
	"github.com/14f3v/kubectl-tui/internal/action/rollout"
	"github.com/14f3v/kubectl-tui/internal/action/scale"
	"github.com/14f3v/kubectl-tui/internal/action/write"
	"github.com/14f3v/kubectl-tui/internal/component"
	"github.com/14f3v/kubectl-tui/internal/engine"
	"github.com/14f3v/kubectl-tui/internal/engine/columns"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/metrics"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// resourcePage is the generic table page shared by every core kind. It watches
// one engine kind, renders a windowed table, and applies a live filter. Kind-
// specific behavior (extra keys, drill-in) is layered by wrapping this page.
type resourcePage struct {
	kind  string
	title string

	sess  deps
	theme style.Theme
	table *component.Table

	viewID    uint64
	namespace string

	remote  engine.Remote[columns.Row]
	allRows []columns.Row
	filter  string

	metrics map[string]metrics.PodUsage // pod usage keyed by ns/name (pods only)
	cpuCol  int                         // CPU column index, or -1
	memCol  int                         // MEM column index, or -1
}

// deps mirrors Deps but lets resourcePage store the session without re-importing.
type deps = Deps

// Option customizes a resource page at construction.
type Option func(*resourcePage)

// WithInitialSort opens the page sorted by a column and direction (e.g. events
// newest-first) instead of the default NAME-ascending.
func WithInitialSort(col int, desc bool) Option {
	return func(p *resourcePage) { p.table.SetSortState(col, desc) }
}

// newResourcePage builds a generic page for a kind.
func newResourcePage(kind, title string, d Deps, opts ...Option) *resourcePage {
	tbl := component.NewTable(d.Theme)
	if proj := columns.For(kind); proj != nil {
		tbl.SetColumns(proj.Columns())
	}
	p := &resourcePage{
		kind:      kind,
		title:     title,
		sess:      d,
		theme:     d.Theme,
		table:     tbl,
		namespace: d.Namespace,
		cpuCol:    -1,
		memCol:    -1,
	}
	if proj := columns.For(kind); proj != nil {
		for i, c := range proj.Columns() {
			switch c.Title {
			case "CPU":
				p.cpuCol = i
			case "MEM":
				p.memCol = i
			}
		}
	}
	for _, o := range opts {
		o(p)
	}
	return p
}

func (p *resourcePage) Init() tea.Cmd { return nil }

func (p *resourcePage) Title() string     { return p.title }
func (p *resourcePage) Kind() string      { return p.kind }
func (p *resourcePage) Namespace() string { return p.namespace }
func (p *resourcePage) Filter() string    { return p.filter }

func (p *resourcePage) OnEnter() tea.Cmd {
	vs, err := p.sess.Session.Engine.Ensure(p.kind, p.namespace)
	if err != nil {
		return func() tea.Msg { return msg.Toast{Text: err.Error(), Level: msg.LevelError} }
	}
	p.viewID = p.sess.Session.Engine.NextViewID()
	vs.SetViewID(p.viewID)
	// Paint the current cache immediately; further snapshots arrive via the sink.
	p.apply(vs.Snapshot())
	return nil
}

func (p *resourcePage) OnLeave() {
	p.sess.Session.Engine.StopIfScreenScoped(p.kind)
}

func (p *resourcePage) SetFilter(f string) {
	p.filter = f
	p.reapplyFilter()
}

func (p *resourcePage) Summary() Summary {
	total, ok, warn, errc := statusCounts(p.allRows)
	return Summary{
		Total:    total,
		OK:       ok,
		Warn:     warn,
		Err:      errc,
		Phase:    p.remote.Phase,
		Error:    p.remote.Err,
		SyncedAt: p.remote.SyncedAt,
	}
}

func (p *resourcePage) Keys() []key.Binding {
	keys := []key.Binding{
		keyEnter, keyDescribe, keyLogs, keyShell, keyYAML,
		keyEdit, keyLabel, keyDelete, keyKill, keyPortFwd, keySortNext, keySortDir,
	}
	// Workload-only actions are advertised only where they apply.
	if scale.Scalable(p.kind) {
		keys = append(keys, keyScale)
	}
	if rollout.Restartable(p.kind) {
		keys = append(keys, keyRollout)
	}
	if p.settable() {
		keys = append(keys, keySet)
	}
	if p.kind == "pods" {
		keys = append(keys, keyDebug)
	}
	if p.kind == "certificatesigningrequests" {
		keys = append(keys, keyApprove, keyDeny)
	}
	return keys
}

// settable reports whether the set/patch menu applies to this kind (pod-template
// workloads and bare pods).
func (p *resourcePage) settable() bool {
	return p.kind == "pods" || scale.Scalable(p.kind) || rollout.Restartable(p.kind)
}

func (p *resourcePage) Update(m tea.Msg) (Page, tea.Cmd) {
	switch t := m.(type) {
	case engine.SnapshotMsg:
		if t.Kind == p.kind && t.ViewID == p.viewID {
			p.apply(t.Snap)
		}
		return p, nil
	case metrics.Snapshot:
		if t.Available {
			p.metrics = t.Pods
		} else {
			p.metrics = nil
		}
		p.reapplyFilter()
		return p, nil
	case tea.KeyPressMsg:
		return p.handleKey(t)
	}
	return p, nil
}

func (p *resourcePage) handleKey(k tea.KeyPressMsg) (Page, tea.Cmd) {
	switch {
	case key.Matches(k, keyUp):
		p.table.MoveUp()
	case key.Matches(k, keyDown):
		p.table.MoveDown()
	case key.Matches(k, keyPageUp):
		p.table.PageUp()
	case key.Matches(k, keyPageDown):
		p.table.PageDown()
	case key.Matches(k, keyHome):
		p.table.Home()
	case key.Matches(k, keyEnd):
		p.table.End()
	case key.Matches(k, keyYAML):
		return p, p.yamlAction()
	case key.Matches(k, keyDescribe):
		return p, p.describeAction()
	case key.Matches(k, keyLogs):
		return p, p.logsAction()
	case key.Matches(k, keyDelete):
		return p, p.deleteAction(false)
	case key.Matches(k, keyKill):
		return p, p.deleteAction(true)
	case key.Matches(k, keyShell):
		return p, p.shellAction()
	case key.Matches(k, keyEdit):
		return p, p.editAction()
	case key.Matches(k, keyPortFwd):
		return p, p.portForwardAction()
	case key.Matches(k, keyScale):
		return p, p.scaleAction()
	case key.Matches(k, keyRollout):
		return p, p.rolloutAction()
	case key.Matches(k, keySet):
		return p, p.setAction()
	case key.Matches(k, keyLabel):
		return p, p.labelAction()
	case key.Matches(k, keyDebug):
		return p, p.debugAction()
	case key.Matches(k, keyApprove):
		return p, p.csrDecision(true)
	case key.Matches(k, keyDeny):
		return p, p.csrDecision(false)
	case key.Matches(k, keySortNext):
		p.cycleSort()
	case key.Matches(k, keySortDir):
		p.toggleSortDir()
	case key.Matches(k, keyEnter):
		return p, p.enterAction()
	}
	return p, nil
}

// cycleSort advances the sort to the next column (wrapping), ascending.
func (p *resourcePage) cycleSort() {
	n := p.table.ColumnCount()
	if n == 0 {
		return
	}
	col, _ := p.table.SortColumn()
	p.table.SetSortState((col+1)%n, false)
}

// toggleSortDir flips the current sort column's direction.
func (p *resourcePage) toggleSortDir() {
	col, _ := p.table.SortColumn()
	p.table.SetSort(col) // re-selecting the current column toggles direction
}

// csrDecision approves or denies the selected CertificateSigningRequest.
func (p *resourcePage) csrDecision(approve bool) tea.Cmd {
	if p.kind != "certificatesigningrequests" {
		return nil
	}
	if p.sess.ReadOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	sess, name := p.sess.Session, row.Name
	title, past, danger := "Approve CSR", "approved", false
	if !approve {
		title, past, danger = "Deny CSR", "denied", true
	}
	act := func() tea.Msg {
		var err error
		if approve {
			err = csr.Approve(sess.Context(), sess.CS, name, "via kubetui")
		} else {
			err = csr.Deny(sess.Context(), sess.CS, name, "via kubetui")
		}
		if err != nil {
			return msg.Toast{Text: name + ": " + err.Error(), Level: msg.LevelError}
		}
		return msg.Toast{Text: name + " " + past, Level: msg.LevelSuccess}
	}
	return func() tea.Msg {
		return ConfirmRequest{Title: title, Prompt: title[:len(title)-4] + " " + name + "?", Danger: danger, Action: act}
	}
}

// debugAction adds an ephemeral debug container to the selected pod and execs a
// shell into it — how you get a shell on a distroless pod. The add + wait run off
// the UI thread inside the prompt action, then the exec hands over the terminal.
func (p *resourcePage) debugAction() tea.Cmd {
	if p.kind != "pods" {
		return toast("debug: select a pod", msg.LevelInfo)
	}
	if p.sess.ReadOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	sess, ns, pod := p.sess.Session, row.Namespace, row.Name
	return func() tea.Msg {
		return PromptRequest{
			Title:   "Debug " + pod,
			Label:   "Image",
			Initial: "busybox",
			Action: func(image string) tea.Msg {
				image = strings.TrimSpace(image)
				if image == "" {
					image = "busybox"
				}
				name, err := debug.AddEphemeralContainer(sess.Context(), sess.CS, ns, pod, image, "")
				if err != nil {
					return msg.Toast{Text: "debug: " + err.Error(), Level: msg.LevelError}
				}
				if err := debug.WaitRunning(sess.Context(), sess.CS, ns, pod, name); err != nil {
					return msg.Toast{Text: "debug: " + err.Error(), Level: msg.LevelError}
				}
				return ExecRequest{Label: "debug", Command: execshell.New(sess.RestCfg, sess.CS, ns, pod, name, nil)}
			},
		}
	}
}

// setAction opens the set/patch menu for the selected workload or pod.
func (p *resourcePage) setAction() tea.Cmd {
	if !p.settable() {
		return toast("set: select a pod or workload", msg.LevelInfo)
	}
	if p.sess.ReadOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	page := newSetMenuPage(p.sess.Session, p.theme, p.kind, row.Namespace, row.Name)
	return func() tea.Msg { return PushMsg{Page: page} }
}

// labelAction opens the label/annotation menu for the selected resource.
func (p *resourcePage) labelAction() tea.Cmd {
	if p.sess.ReadOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	page := newMetaMenuPage(p.sess.Session, p.theme, p.kind, row.Namespace, row.Name)
	return func() tea.Msg { return PushMsg{Page: page} }
}

// enterAction drills into the selected row: a pod's containers, or a secret's
// (masked) data keys. For other kinds enter is a no-op for now (their detail
// views can be added later).
func (p *resourcePage) enterAction() tea.Cmd {
	switch p.kind {
	case "pods":
		return p.enterPod()
	case "secrets":
		return p.enterSecret()
	case "namespaces":
		return p.enterNamespace()
	case "nodes":
		return p.enterNode()
	case "cronjobs":
		return p.enterCronJob()
	default:
		return nil
	}
}

// enterCronJob opens the cronjob actions menu (trigger / suspend / resume).
func (p *resourcePage) enterCronJob() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	page := newCronJobMenuPage(p.sess.Session, p.theme, row.Namespace, row.Name, p.sess.ReadOnly)
	return func() tea.Msg { return PushMsg{Page: page} }
}

// enterNamespace drills into a namespace by scoping the pods view to it (k9s-style
// "open this namespace").
func (p *resourcePage) enterNamespace() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	ns := row.Name
	return func() tea.Msg { return msg.Navigate{Kind: "pods", Namespace: ns} }
}

// enterNode opens the node operations menu (cordon/uncordon/drain).
func (p *resourcePage) enterNode() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	page := newNodeOpsPage(p.sess.Session, p.theme, row.Name, p.sess.ReadOnly)
	return func() tea.Msg { return PushMsg{Page: page} }
}

func (p *resourcePage) enterPod() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	vs := p.sess.Session.Engine.Get("pods")
	if vs == nil {
		return nil
	}
	obj, ok := vs.Get(row.Namespace, row.Name)
	if !ok {
		return toast(row.Name+" is no longer in the cache", msg.LevelWarn)
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return nil
	}
	page := newContainersPage(p.sess.Session, p.theme, pod)
	return func() tea.Msg { return PushMsg{Page: page} }
}

func (p *resourcePage) enterSecret() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	vs := p.sess.Session.Engine.Get("secrets")
	if vs == nil {
		return nil
	}
	obj, ok := vs.Get(row.Namespace, row.Name)
	if !ok {
		return toast(row.Name+" is no longer in the cache", msg.LevelWarn)
	}
	sec, ok := obj.(*corev1.Secret)
	if !ok {
		return nil
	}
	page := newSecretRevealPage(p.sess.Session, p.theme, sec)
	return func() tea.Msg { return PushMsg{Page: page} }
}

// portForwardAction forwards the selected pod's declared container ports to
// ephemeral local ports. Open ":pf" to see the resolved local ports.
func (p *resourcePage) portForwardAction() tea.Cmd {
	if p.kind != "pods" {
		return toast("port-forward: select a pod", msg.LevelInfo)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	vs := p.sess.Session.Engine.Get("pods")
	if vs == nil {
		return toast("no live data for pods", msg.LevelError)
	}
	obj, ok := vs.Get(row.Namespace, row.Name)
	if !ok {
		return toast(row.Name+" is no longer in the cache", msg.LevelWarn)
	}
	pod, ok := obj.(*corev1.Pod)
	if !ok {
		return toast("unexpected object type", msg.LevelError)
	}
	ports := podForwardPorts(pod)
	if len(ports) == 0 {
		return toast(row.Name+" declares no container ports", msg.LevelWarn)
	}
	p.sess.Session.Forwards.Start(row.Namespace, row.Name, ports)
	return toast("port-forward starting — open :pf to view", msg.LevelSuccess)
}

// podForwardPorts collects the pod's distinct container ports as ":remote"
// specs, letting the kernel pick each local port.
func podForwardPorts(pod *corev1.Pod) []string {
	seen := map[int32]bool{}
	var out []string
	for _, c := range pod.Spec.Containers {
		for _, port := range c.Ports {
			if port.ContainerPort == 0 || seen[port.ContainerPort] {
				continue
			}
			seen[port.ContainerPort] = true
			out = append(out, ":"+itoa32(port.ContainerPort))
		}
	}
	return out
}

func itoa32(n int32) string {
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

// shellAction hands the terminal to an interactive shell in the selected pod
// (pods only).
func (p *resourcePage) shellAction() tea.Cmd {
	if p.kind != "pods" {
		return toast("shell: select a pod", msg.LevelInfo)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	sess := p.sess.Session
	cmd := execshell.New(sess.RestCfg, sess.CS, row.Namespace, row.Name, "", nil)
	return func() tea.Msg {
		return ExecRequest{Label: "shell", Command: cmd}
	}
}

// editAction dumps the selected object to $EDITOR and applies changes with SSA.
func (p *resourcePage) editAction() tea.Cmd {
	if p.sess.ReadOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	info, ok := k8s.ResourceFor(p.kind)
	if !ok {
		return toast("edit not supported for "+p.kind, msg.LevelError)
	}
	vs := p.sess.Session.Engine.Get(p.kind)
	if vs == nil {
		return toast("no live data for "+p.kind, msg.LevelError)
	}
	obj, ok := vs.Get(row.Namespace, row.Name)
	if !ok {
		return toast(row.Name+" is no longer in the cache", msg.LevelWarn)
	}
	yamlStr, err := inspect.YAML(obj)
	if err != nil {
		return toast("edit: "+err.Error(), msg.LevelError)
	}
	path, sum, err := editor.WriteTemp(yamlStr)
	if err != nil {
		return toast("edit: "+err.Error(), msg.LevelError)
	}

	sess := p.sess.Session
	ns, name := row.Namespace, row.Name
	after := func(execErr error) tea.Msg {
		defer os.Remove(path)
		if execErr != nil {
			return msg.Toast{Text: "editor: " + execErr.Error(), Level: msg.LevelError}
		}
		changed, aerr := editor.Apply(sess.Context(), sess.Dyn, info.GVR, info.Namespaced, ns, name, path, sum)
		switch {
		case aerr != nil:
			return msg.Toast{Text: "apply " + name + ": " + aerr.Error(), Level: msg.LevelError}
		case !changed:
			return msg.Toast{Text: "no changes", Level: msg.LevelInfo}
		default:
			return msg.Toast{Text: name + " applied", Level: msg.LevelSuccess}
		}
	}
	return func() tea.Msg {
		return ExecRequest{Label: "edit", Process: editor.Process(path), After: after}
	}
}

// deleteAction confirms and then deletes (or force-deletes) the selected object.
// The delete is fire-and-observe: on success nothing mutates locally; the watch
// removes the row.
func (p *resourcePage) deleteAction(force bool) tea.Cmd {
	if p.sess.ReadOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	info, ok := k8s.ResourceFor(p.kind)
	if !ok {
		return toast("delete not supported for "+p.kind, msg.LevelError)
	}

	sess := p.sess.Session
	ns, name := row.Namespace, row.Name
	verb := "delete"
	act := func() tea.Msg {
		err := write.Delete(sess.Context(), sess.Dyn, info.GVR, info.Namespaced, ns, name, force)
		if err != nil {
			return msg.Toast{Text: "delete " + name + ": " + err.Error(), Level: msg.LevelError}
		}
		what := "deleted"
		if force {
			what = "killed"
		}
		return msg.Toast{Text: name + " " + what, Level: msg.LevelSuccess}
	}

	title := "Delete " + info.Kind
	prompt := "Delete " + name + "?"
	if force {
		title = "Kill (force-delete) " + info.Kind
		prompt = "Force-delete " + name + " now with grace period 0?"
	}
	// Preflight the permission so the dialog can warn early (best-effort).
	if allowed, err := write.CanI(sess.Context(), sess.CS, verb, info.GVR, ns); err == nil && !allowed {
		prompt += "  (your account may not be permitted to " + verb + ")"
	}

	return func() tea.Msg {
		return ConfirmRequest{Title: title, Prompt: prompt, Danger: true, Action: act}
	}
}

// logsAction follows logs in a new page: a single pod, or — for a workload — every
// pod its selector matches, merged into one tagged stream.
func (p *resourcePage) logsAction() tea.Cmd {
	switch {
	case p.kind == "pods":
		row, ok := p.table.Selected()
		if !ok {
			return nil
		}
		page := NewLogsPage(p.sess.Session, p.theme, row.Namespace, row.Name, "")
		return func() tea.Msg { return PushMsg{Page: page} }
	case logsWorkload(p.kind):
		return p.workloadLogsAction()
	default:
		return toast("logs: select a pod or workload", msg.LevelInfo)
	}
}

// workloadLogsAction resolves the selected workload's pod selector and opens a
// merged multi-pod logs page.
func (p *resourcePage) workloadLogsAction() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	vs := p.sess.Session.Engine.Get(p.kind)
	if vs == nil {
		return toast("no live data for "+p.kind, msg.LevelError)
	}
	obj, ok := vs.Get(row.Namespace, row.Name)
	if !ok {
		return toast(row.Name+" is no longer in the cache", msg.LevelWarn)
	}
	sel := workloadSelector(obj)
	if sel == nil {
		return toast(row.Name+" has no pod selector", msg.LevelWarn)
	}
	title := singularKind(p.kind) + "/" + row.Name + " · logs"
	page := NewMultiLogsPage(p.sess.Session, p.theme, title, row.Namespace, sel)
	return func() tea.Msg { return PushMsg{Page: page} }
}

// scaleAction prompts for a replica count and scales the selected workload.
func (p *resourcePage) scaleAction() tea.Cmd {
	if p.sess.ReadOnly {
		return toast("read-only mode: mutations are disabled", msg.LevelWarn)
	}
	if !scale.Scalable(p.kind) {
		return toast("scale: select a deployment, statefulset or replicaset", msg.LevelInfo)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	cur := ""
	if vs := p.sess.Session.Engine.Get(p.kind); vs != nil {
		if obj, ok := vs.Get(row.Namespace, row.Name); ok {
			cur = currentReplicas(obj)
		}
	}
	sess := p.sess.Session
	kind, ns, name := p.kind, row.Namespace, row.Name
	return func() tea.Msg {
		return PromptRequest{
			Title:    "Scale " + name,
			Label:    "Replicas",
			Initial:  cur,
			Validate: func(s string) error { _, err := scale.ParseReplicas(s); return err },
			Action: func(s string) tea.Msg {
				n, err := scale.ParseReplicas(s)
				if err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				if err := scale.Scale(sess.Context(), sess.CS, kind, ns, name, n); err != nil {
					return msg.Toast{Text: "scale " + name + ": " + err.Error(), Level: msg.LevelError}
				}
				return msg.Toast{Text: name + " scaled to " + s, Level: msg.LevelSuccess}
			},
		}
	}
}

// rolloutAction opens the rollout menu (restart / status) for the selected workload.
func (p *resourcePage) rolloutAction() tea.Cmd {
	if !rollout.Restartable(p.kind) {
		return toast("rollout: select a deployment, statefulset or daemonset", msg.LevelInfo)
	}
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	page := newRolloutPage(p.sess.Session, p.theme, p.kind, row.Namespace, row.Name, p.sess.ReadOnly)
	return func() tea.Msg { return PushMsg{Page: page} }
}

// logsWorkload reports whether merged multi-pod logs apply to a kind.
func logsWorkload(kind string) bool {
	switch kind {
	case "deployments", "statefulsets", "daemonsets", "replicasets", "jobs":
		return true
	}
	return false
}

// workloadSelector returns a cached workload object's pod selector, or nil.
func workloadSelector(obj any) *metav1.LabelSelector {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		return o.Spec.Selector
	case *appsv1.StatefulSet:
		return o.Spec.Selector
	case *appsv1.DaemonSet:
		return o.Spec.Selector
	case *appsv1.ReplicaSet:
		return o.Spec.Selector
	case *batchv1.Job:
		return o.Spec.Selector
	}
	return nil
}

// currentReplicas renders a cached workload's desired replica count for prefilling
// the scale prompt, or "" if unknown.
func currentReplicas(obj any) string {
	switch o := obj.(type) {
	case *appsv1.Deployment:
		if o.Spec.Replicas != nil {
			return itoa32(*o.Spec.Replicas)
		}
	case *appsv1.StatefulSet:
		if o.Spec.Replicas != nil {
			return itoa32(*o.Spec.Replicas)
		}
	case *appsv1.ReplicaSet:
		if o.Spec.Replicas != nil {
			return itoa32(*o.Spec.Replicas)
		}
	}
	return ""
}

// singularKind maps a plural kind key to a short singular label for titles.
func singularKind(kind string) string { return strings.TrimSuffix(kind, "s") }

// yamlAction reads the selected object from the informer cache and pushes a YAML
// text page. It runs synchronously (the object is already cached).
func (p *resourcePage) yamlAction() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	vs := p.sess.Session.Engine.Get(p.kind)
	if vs == nil {
		return toast("no live data for "+p.kind, msg.LevelError)
	}
	obj, ok := vs.Get(row.Namespace, row.Name)
	if !ok {
		return toast(row.Name+" is no longer in the cache", msg.LevelWarn)
	}
	yamlStr, err := inspect.YAML(obj)
	if err != nil {
		return toast("yaml: "+err.Error(), msg.LevelError)
	}
	tv := NewTextView(row.Name+" · yaml", yamlStr, p.theme)
	return func() tea.Msg { return PushMsg{Page: tv} }
}

// describeAction runs kubectl-style describe as a command (it makes its own API
// calls) and pushes the result as a text page.
func (p *resourcePage) describeAction() tea.Cmd {
	row, ok := p.table.Selected()
	if !ok {
		return nil
	}
	cfg := p.sess.Session.RestCfg
	kind, ns, name, theme := p.kind, row.Namespace, row.Name, p.theme
	return func() tea.Msg {
		out, err := inspect.Describe(cfg, kind, ns, name)
		if err != nil {
			return msg.Toast{Text: "describe: " + err.Error(), Level: msg.LevelError}
		}
		return PushMsg{Page: NewTextView(name+" · describe", out, theme)}
	}
}

func toast(text string, level msg.Level) tea.Cmd {
	return func() tea.Msg { return msg.Toast{Text: text, Level: level} }
}

func (p *resourcePage) View(width, height int) string {
	if height < 2 {
		height = 2
	}
	p.table.SetSize(width, height-1) // one line for the column header
	return p.table.Header() + "\n" + p.table.Body()
}

// apply stores a new snapshot and refreshes the filtered view.
func (p *resourcePage) apply(snap engine.Remote[columns.Row]) {
	p.remote = snap
	p.allRows = snap.Rows
	p.reapplyFilter()
}

func (p *resourcePage) reapplyFilter() {
	rows := filterRows(p.allRows, p.filter)
	p.overlayMetrics(rows)
	p.table.SetRows(rows)
}

// overlayMetrics rewrites the CPU/MEM cells from the latest metrics snapshot.
// It clones each modified row's cells so the shared projector output (allRows) is
// not mutated. A no-op unless the kind has CPU/MEM columns and metrics are live.
func (p *resourcePage) overlayMetrics(rows []columns.Row) {
	if p.metrics == nil || (p.cpuCol < 0 && p.memCol < 0) {
		return
	}
	for i := range rows {
		u, ok := p.metrics[rows[i].Namespace+"/"+rows[i].Name]
		if !ok {
			continue
		}
		cells := append([]columns.Cell(nil), rows[i].Cells...)
		if p.cpuCol >= 0 && p.cpuCol < len(cells) {
			cells[p.cpuCol] = columns.Cell{Text: metrics.FormatCPU(u.CPUMillis), Status: columns.StatusNeutral}
		}
		if p.memCol >= 0 && p.memCol < len(cells) {
			cells[p.memCol] = columns.Cell{Text: metrics.FormatMem(u.MemBytes), Status: columns.StatusNeutral}
		}
		rows[i].Cells = cells
	}
}

// filterRows applies the live filter over the row's name and cell text. A leading
// "!" inverts the match. By default the term is a case-insensitive substring; a
// leading "~" switches to a case-insensitive regular expression. An unparseable
// regex filters nothing (all rows pass) until it becomes valid.
func filterRows(rows []columns.Row, filter string) []columns.Row {
	if filter == "" {
		return rows
	}
	invert := false
	term := filter
	if strings.HasPrefix(term, "!") {
		invert = true
		term = term[1:]
	}
	if term == "" {
		return rows
	}
	var re *regexp.Regexp
	if strings.HasPrefix(term, "~") {
		compiled, err := regexp.Compile("(?i)" + term[1:])
		if err != nil {
			return rows
		}
		re = compiled
	}
	lower := strings.ToLower(term)
	out := make([]columns.Row, 0, len(rows))
	for _, r := range rows {
		if rowMatches(r, lower, re) != invert {
			out = append(out, r)
		}
	}
	return out
}

// rowMatches reports whether a row matches the filter: regex when re != nil,
// otherwise case-insensitive substring on term.
func rowMatches(r columns.Row, term string, re *regexp.Regexp) bool {
	if re != nil {
		if re.MatchString(r.Name) {
			return true
		}
		for _, c := range r.Cells {
			if re.MatchString(c.Text) {
				return true
			}
		}
		return false
	}
	if strings.Contains(strings.ToLower(r.Name), term) {
		return true
	}
	for _, c := range r.Cells {
		if strings.Contains(strings.ToLower(c.Text), term) {
			return true
		}
	}
	return false
}

// statusCounts tallies rows by their health class, for the header count line.
func statusCounts(rows []columns.Row) (total, ok, warn, errc int) {
	total = len(rows)
	for _, r := range rows {
		switch r.Health {
		case columns.StatusOK:
			ok++
		case columns.StatusWarn:
			warn++
		case columns.StatusError:
			errc++
		}
	}
	return total, ok, warn, errc
}
