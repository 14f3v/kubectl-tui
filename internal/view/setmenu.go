package view

import (
	"fmt"
	"strings"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"k8s.io/apimachinery/pkg/types"

	"github.com/14f3v/kubectl-tui/internal/action/setspec"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

type setItem struct{ label, desc string }

// setMenuPage is the `=` menu for kubectl-set-style quick edits and a raw patch,
// pushed from a workload or pod. Each item opens a single-line prompt; the change
// is applied as a strategic-merge patch via the dynamic client.
type setMenuPage struct {
	sess       *k8s.Session
	theme      style.Theme
	kind       string
	namespace  string
	name       string
	inTemplate bool // patch spec.template.spec (workloads) vs spec (bare pod)
	items      []setItem
	cursor     int
}

func newSetMenuPage(sess *k8s.Session, theme style.Theme, kind, namespace, name string) *setMenuPage {
	return &setMenuPage{
		sess: sess, theme: theme, kind: kind, namespace: namespace, name: name,
		inTemplate: kind != "pods",
		items: []setItem{
			{"Set image", "container=image"},
			{"Set resources", "container cpu=req[/lim] memory=req[/lim]"},
			{"Set env", "container KEY=VAL[,KEY2=VAL2]"},
			{"Set serviceaccount", "serviceaccount name"},
			{"Patch", "raw strategic-merge JSON"},
		},
	}
}

func (p *setMenuPage) Init() tea.Cmd     { return nil }
func (p *setMenuPage) Title() string     { return p.name + " · set" }
func (p *setMenuPage) Kind() string      { return "" }
func (p *setMenuPage) Namespace() string { return p.namespace }
func (p *setMenuPage) Filter() string    { return "" }
func (p *setMenuPage) SetFilter(string)  {}
func (p *setMenuPage) OnEnter() tea.Cmd  { return nil }
func (p *setMenuPage) OnLeave()          {}
func (p *setMenuPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *setMenuPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "edit")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *setMenuPage) Update(m tea.Msg) (Page, tea.Cmd) {
	k, ok := m.(tea.KeyPressMsg)
	if !ok {
		return p, nil
	}
	switch k.String() {
	case "j", "down":
		if p.cursor < len(p.items)-1 {
			p.cursor++
		}
	case "k", "up":
		if p.cursor > 0 {
			p.cursor--
		}
	case "enter", " ":
		it := p.items[p.cursor]
		switch it.label {
		case "Set image":
			return p, p.prompt(it, p.applyImage)
		case "Set resources":
			return p, p.prompt(it, p.applyResources)
		case "Set env":
			return p, p.prompt(it, p.applyEnv)
		case "Set serviceaccount":
			return p, p.prompt(it, p.applyServiceAccount)
		case "Patch":
			return p, p.prompt(it, p.applyPatch)
		}
	}
	return p, nil
}

// prompt opens a single-line input for a set item; f runs off the UI thread and
// applies the patch, then a toast reports the result.
func (p *setMenuPage) prompt(it setItem, f func(string) error) tea.Cmd {
	name := p.name
	return func() tea.Msg {
		return PromptRequest{
			Title: it.label + " · " + name,
			Label: it.desc,
			Action: func(v string) tea.Msg {
				if err := f(strings.TrimSpace(v)); err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				return msg.Toast{Text: name + " patched", Level: msg.LevelSuccess}
			},
		}
	}
}

func (p *setMenuPage) patch(pt types.PatchType, body []byte) error {
	info, ok := k8s.ResourceFor(p.kind)
	if !ok {
		return fmt.Errorf("set not supported for %s", p.kind)
	}
	return setspec.Patch(p.sess.Context(), p.sess.Dyn, info.GVR, info.Namespaced, p.namespace, p.name, pt, body)
}

func (p *setMenuPage) applyImage(v string) error {
	container, image, ok := strings.Cut(v, "=")
	container, image = strings.TrimSpace(container), strings.TrimSpace(image)
	if !ok || container == "" || image == "" {
		return fmt.Errorf("expected container=image")
	}
	return p.patch(setspec.SetImage(p.inTemplate, container, image))
}

func (p *setMenuPage) applyServiceAccount(v string) error {
	if v == "" {
		return fmt.Errorf("expected a serviceaccount name")
	}
	return p.patch(setspec.SetServiceAccount(p.inTemplate, v))
}

func (p *setMenuPage) applyEnv(v string) error {
	container, rest, ok := strings.Cut(v, " ")
	if !ok || container == "" || strings.TrimSpace(rest) == "" {
		return fmt.Errorf("expected: container KEY=VAL[,KEY2=VAL2]")
	}
	env := map[string]string{}
	for _, pair := range strings.Split(rest, ",") {
		pair = strings.TrimSpace(pair)
		if pair == "" {
			continue
		}
		k, val, ok := strings.Cut(pair, "=")
		if !ok || strings.TrimSpace(k) == "" {
			return fmt.Errorf("bad env pair %q", pair)
		}
		env[strings.TrimSpace(k)] = val
	}
	if len(env) == 0 {
		return fmt.Errorf("no env vars given")
	}
	return p.patch(setspec.SetEnv(p.inTemplate, container, env))
}

func (p *setMenuPage) applyResources(v string) error {
	fields := strings.Fields(v)
	if len(fields) < 2 {
		return fmt.Errorf("expected: container cpu=req[/lim] memory=req[/lim]")
	}
	container := fields[0]
	req, lim := map[string]string{}, map[string]string{}
	for _, f := range fields[1:] {
		res, spec, ok := strings.Cut(f, "=")
		if !ok || res == "" {
			return fmt.Errorf("bad resource %q", f)
		}
		r, l, hasLim := strings.Cut(spec, "/")
		if r != "" {
			req[res] = r
		}
		if hasLim && l != "" {
			lim[res] = l
		}
	}
	if len(req) == 0 && len(lim) == 0 {
		return fmt.Errorf("no resource values given")
	}
	return p.patch(setspec.SetResources(p.inTemplate, container, req, lim))
}

func (p *setMenuPage) applyPatch(v string) error {
	if v == "" {
		return fmt.Errorf("expected a JSON patch body")
	}
	return p.patch(types.StrategicMergePatchType, []byte(v))
}

func (p *setMenuPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  SET / PATCH") + "\n")
	for i, it := range p.items {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		b.WriteString(marker +
			nameStyle.Render(pad(it.label, 20)) +
			t.Faint.Render(trunc(it.desc, width-26)) + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  enter edit · esc back"))
	return b.String()
}
