package view

import (
	"context"
	"sort"
	"strings"
	"time"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	authnv1 "k8s.io/api/authentication/v1"
	metav1 "k8s.io/apimachinery/pkg/apis/meta/v1"
	"k8s.io/apimachinery/pkg/runtime/schema"

	"github.com/14f3v/kubectl-tui/internal/action/write"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/style"
)

func init() {
	Register("whoami", []string{"whoami", "auth"}, "current identity and access review", func(d Deps) Page {
		return newWhoamiPage(d)
	})
}

// accessProbe is one can-i check rendered in the whoami permissions grid.
type accessProbe struct {
	label   string
	verb    string
	gvr     schema.GroupVersionResource
	cluster bool // cluster-scoped: reviewed with an empty namespace
}

// probes is the fixed set of can-i checks shown by :whoami. It samples common
// read and mutate verbs plus the cluster-admin wildcard.
var probes = []accessProbe{
	{"list pods", "list", schema.GroupVersionResource{Resource: "pods"}, false},
	{"create pods", "create", schema.GroupVersionResource{Resource: "pods"}, false},
	{"delete pods", "delete", schema.GroupVersionResource{Resource: "pods"}, false},
	{"get secrets", "get", schema.GroupVersionResource{Resource: "secrets"}, false},
	{"list nodes", "list", schema.GroupVersionResource{Resource: "nodes"}, true},
	{"* on * (cluster-admin)", "*", schema.GroupVersionResource{Group: "*", Resource: "*"}, true},
}

// probeResult pairs a probe with its allowed/denied/errored outcome.
type probeResult struct {
	label   string
	allowed bool
	err     bool
}

// whoamiResult is the async payload delivered to whoamiPage.Update after the
// identity lookup and can-i probes finish.
type whoamiResult struct {
	username string
	uid      string
	groups   []string
	authErr  string
	probes   []probeResult
}

// whoamiPage shows the resolved identity (from the kubeconfig and the API
// server's SelfSubjectReview) plus a can-i permissions grid. It is not backed by
// an engine kind; OnEnter fires one command that fills the page.
type whoamiPage struct {
	sess      *k8s.Session
	theme     style.Theme
	namespace string

	loaded bool
	res    whoamiResult
}

func newWhoamiPage(d Deps) *whoamiPage {
	return &whoamiPage{sess: d.Session, theme: d.Theme, namespace: d.Namespace}
}

func (p *whoamiPage) Init() tea.Cmd     { return nil }
func (p *whoamiPage) Title() string     { return "whoami" }
func (p *whoamiPage) Kind() string      { return "" }
func (p *whoamiPage) Namespace() string { return p.namespace }
func (p *whoamiPage) Filter() string    { return "" }
func (p *whoamiPage) SetFilter(string)  {}
func (p *whoamiPage) OnLeave()          {}

func (p *whoamiPage) Summary() Summary { return Summary{Total: len(probes)} }

func (p *whoamiPage) Keys() []key.Binding {
	return []key.Binding{key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back"))}
}

// OnEnter runs the identity lookup and can-i probes as one background command.
func (p *whoamiPage) OnEnter() tea.Cmd {
	sess := p.sess
	ns := p.namespace
	return func() tea.Msg {
		ctx, cancel := context.WithTimeout(sess.Context(), 8*time.Second)
		defer cancel()

		res := whoamiResult{}
		// Authenticated identity as the API server sees it (falls back to the
		// kubeconfig user when SelfSubjectReview is unavailable/forbidden).
		review, err := sess.CS.AuthenticationV1().SelfSubjectReviews().
			Create(ctx, &authnv1.SelfSubjectReview{}, metav1.CreateOptions{})
		if err != nil {
			res.authErr = err.Error()
			res.username = sess.Identity.User
		} else {
			ui := review.Status.UserInfo
			res.username = ui.Username
			res.uid = ui.UID
			res.groups = append(res.groups, ui.Groups...)
		}

		for _, pr := range probes {
			scope := ns
			if pr.cluster {
				scope = ""
			}
			allowed, cerr := write.CanI(ctx, sess.CS, pr.verb, pr.gvr, scope)
			res.probes = append(res.probes, probeResult{label: pr.label, allowed: allowed, err: cerr != nil})
		}
		return whoamiResult(res)
	}
}

func (p *whoamiPage) Update(m tea.Msg) (Page, tea.Cmd) {
	if r, ok := m.(whoamiResult); ok {
		p.res = r
		p.loaded = true
		return p, nil
	}
	return p, nil
}

func (p *whoamiPage) View(width, height int) string {
	t := p.theme
	id := p.sess.Identity
	var b strings.Builder

	row := func(k, v string) {
		b.WriteString(t.Dim.Render(pad("  "+k, 16)) + t.Base.Render(dashOr(v)) + "\n")
	}
	b.WriteString(t.ColHeader.Render("  CONTEXT") + "\n")
	row("context", id.Context)
	row("cluster", id.Cluster)
	row("server", id.Server)
	row("namespace", nsLabel(p.namespace))
	row("k8s version", id.K8sVersion)

	b.WriteString("\n" + t.ColHeader.Render("  IDENTITY") + "\n")
	if !p.loaded {
		b.WriteString(t.Faint.Render("  resolving identity…") + "\n")
	} else {
		row("user", p.res.username)
		if p.res.uid != "" {
			row("uid", p.res.uid)
		}
		if len(p.res.groups) > 0 {
			sort.Strings(p.res.groups)
			row("groups", strings.Join(p.res.groups, ", "))
		}
		if p.res.authErr != "" {
			b.WriteString(t.Faint.Render("  (SelfSubjectReview unavailable: "+trunc(p.res.authErr, width-36)+")") + "\n")
		}
	}

	b.WriteString("\n" + t.ColHeader.Render("  CAN-I") + "\n")
	if !p.loaded {
		b.WriteString(t.Faint.Render("  probing access…") + "\n")
	} else {
		for _, pr := range p.res.probes {
			mark, col := "— unknown", t.Pal.Warn
			switch {
			case pr.err:
				mark, col = "— unknown", t.Pal.Warn
			case pr.allowed:
				mark, col = "✔ yes", t.Pal.OK
			default:
				mark, col = "✗ no", t.Pal.Err
			}
			b.WriteString(t.Dim.Render(pad("  "+pr.label, 28)) +
				lipgloss.NewStyle().Foreground(col).Render(mark) + "\n")
		}
	}

	b.WriteString("\n" + t.Faint.Render("  esc back"))
	return b.String()
}

func dashOr(s string) string {
	if s == "" {
		return "—"
	}
	return s
}

func nsLabel(ns string) string {
	if ns == "" {
		return "(all namespaces)"
	}
	return ns
}
