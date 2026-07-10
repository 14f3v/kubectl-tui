package view

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/apply"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

// diffApplyPage previews what a server-side apply would change (computed with a
// read-only dry-run) before actually applying. It wraps a scrollable TextView for
// the unified diff and adds a single mutating action: `a` runs the real apply.
// This is the safety step for `:apply` — the user sees the diff, then confirms.
type diffApplyPage struct {
	tv        *TextView
	sess      *k8s.Session
	namespace string
	data      []byte // the raw manifest(s) to apply on confirm
	readOnly  bool
}

// NewDiffApply builds the diff preview page from pre-computed dry-run results and
// the raw manifest data that produced them (kept so `a` can run the real apply).
func NewDiffApply(sess *k8s.Session, theme style.Theme, namespace string, data []byte, results []apply.DiffResult, readOnly bool) Page {
	return &diffApplyPage{
		tv:        NewTextView("apply diff", renderDiffResults(results), theme),
		sess:      sess,
		namespace: namespace,
		data:      data,
		readOnly:  readOnly,
	}
}

// renderDiffResults formats the per-document dry-run diffs into one scrollable
// body, with a header per document and a leading hint about the apply key.
func renderDiffResults(results []apply.DiffResult) string {
	var b strings.Builder
	b.WriteString("# dry-run diff — press 'a' to apply, esc to cancel\n")
	for _, r := range results {
		name := r.Name
		if name == "" {
			name = "(unnamed)"
		}
		loc := r.GVK + " " + name
		if r.Namespace != "" {
			loc = r.GVK + " " + r.Namespace + "/" + name
		}
		b.WriteString("\n=== " + loc + " ===\n")
		switch {
		case r.Err != nil:
			b.WriteString("! error: " + r.Err.Error() + "\n")
		case strings.TrimSpace(r.Diff) == "":
			b.WriteString("(no change)\n")
		default:
			b.WriteString(r.Diff)
			if !strings.HasSuffix(r.Diff, "\n") {
				b.WriteByte('\n')
			}
		}
	}
	return b.String()
}

func (p *diffApplyPage) Init() tea.Cmd      { return p.tv.Init() }
func (p *diffApplyPage) Title() string      { return "apply diff" }
func (p *diffApplyPage) Kind() string       { return "" }
func (p *diffApplyPage) Namespace() string  { return p.namespace }
func (p *diffApplyPage) Filter() string     { return p.tv.Filter() }
func (p *diffApplyPage) SetFilter(f string) { p.tv.SetFilter(f) }
func (p *diffApplyPage) OnEnter() tea.Cmd   { return nil }
func (p *diffApplyPage) OnLeave()           {}
func (p *diffApplyPage) Summary() Summary   { return p.tv.Summary() }

func (p *diffApplyPage) Keys() []key.Binding {
	return append([]key.Binding{
		key.NewBinding(key.WithKeys("a"), key.WithHelp("a", "apply")),
	}, p.tv.Keys()...)
}

func (p *diffApplyPage) Update(m tea.Msg) (Page, tea.Cmd) {
	if k, ok := m.(tea.KeyPressMsg); ok && k.String() == "a" {
		if p.readOnly {
			return p, toast("read-only mode: mutations are disabled", msg.LevelWarn)
		}
		// Pop back to the list immediately; the apply runs off-thread and reports a
		// toast when it lands.
		return p, tea.Batch(p.applyCmd(), func() tea.Msg { return PopMsg{} })
	}
	// Delegate scrolling/search to the embedded TextView (it mutates in place).
	_, cmd := p.tv.Update(m)
	return p, cmd
}

// applyCmd runs the real server-side apply and reports a summary toast.
func (p *diffApplyPage) applyCmd() tea.Cmd {
	sess, data, ns := p.sess, p.data, p.namespace
	return func() tea.Msg {
		mapper := apply.NewMapper(sess.Disco)
		return summarizeApply(apply.Apply(sess.Context(), sess.Dyn, mapper, data, "kubetui", ns))
	}
}

func (p *diffApplyPage) View(width, height int) string { return p.tv.View(width, height) }

// summarizeApply condenses per-document apply results into a single toast.
func summarizeApply(results []apply.Result) msg.Toast {
	if len(results) == 0 {
		return msg.Toast{Text: "nothing to apply", Level: msg.LevelInfo}
	}
	var okc, failed int
	firstErr := ""
	for _, r := range results {
		if r.Err != nil {
			failed++
			if firstErr == "" {
				firstErr = r.Name + ": " + r.Err.Error()
			}
			continue
		}
		okc++
	}
	if failed == 0 {
		return msg.Toast{Text: fmt.Sprintf("applied %d object(s)", okc), Level: msg.LevelSuccess}
	}
	return msg.Toast{Text: fmt.Sprintf("applied %d, %d failed — %s", okc, failed, firstErr), Level: msg.LevelError}
}
