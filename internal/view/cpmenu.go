package view

import (
	"fmt"
	"strings"
	"unicode"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"

	"github.com/14f3v/kubectl-tui/internal/action/cp"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/style"
)

type cpItem struct{ label, desc string }

// cpMenuPage is the copy-files menu, pushed from a pod row (issue #30). Each item
// collects two whitespace-separated paths (see splitPaths for quoting) and
// streams the file to or from the pod off the UI thread. container is "" for the
// pod's first container.
type cpMenuPage struct {
	sess      *k8s.Session
	theme     style.Theme
	namespace string
	pod       string
	container string
	items     []cpItem
	cursor    int
}

func newCpMenuPage(sess *k8s.Session, theme style.Theme, namespace, pod, container string) *cpMenuPage {
	return &cpMenuPage{
		sess: sess, theme: theme, namespace: namespace, pod: pod, container: container,
		items: []cpItem{
			{"Upload", `local → pod (localPath remotePath; "quote" a path with spaces)`},
			{"Download", `pod → local (remotePath localPath; "quote" a path with spaces)`},
		},
	}
}

func (p *cpMenuPage) Init() tea.Cmd     { return nil }
func (p *cpMenuPage) Title() string     { return p.pod + " · copy" }
func (p *cpMenuPage) Kind() string      { return "" }
func (p *cpMenuPage) Namespace() string { return p.namespace }
func (p *cpMenuPage) Filter() string    { return "" }
func (p *cpMenuPage) SetFilter(string)  {}
func (p *cpMenuPage) OnEnter() tea.Cmd  { return nil }
func (p *cpMenuPage) OnLeave()          {}
func (p *cpMenuPage) Summary() Summary  { return Summary{Total: len(p.items)} }

func (p *cpMenuPage) Keys() []key.Binding {
	return []key.Binding{
		key.NewBinding(key.WithKeys("j", "k"), key.WithHelp("j/k", "move")),
		key.NewBinding(key.WithKeys("enter"), key.WithHelp("enter", "copy")),
		key.NewBinding(key.WithKeys("esc"), key.WithHelp("esc", "back")),
	}
}

func (p *cpMenuPage) Update(m tea.Msg) (Page, tea.Cmd) {
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
	case "enter", "space":
		switch p.items[p.cursor].label {
		case "Upload":
			return p, p.upload()
		case "Download":
			return p, p.download()
		}
	}
	return p, nil
}

// upload collects "localPath remotePath" and streams the local file into the pod
// off the UI thread.
func (p *cpMenuPage) upload() tea.Cmd {
	pod := p.pod
	const usage = "localPath remotePath"
	return func() tea.Msg {
		return PromptRequest{
			Title:    "Upload · " + pod,
			Label:    usage,
			Validate: func(v string) error { _, _, err := twoPaths(v, usage); return err },
			Action: func(v string) tea.Msg {
				local, remote, err := twoPaths(v, usage)
				if err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				res, err := cp.CopyToPod(p.sess.Context(), p.sess.RestCfg, p.sess.CS, p.namespace, p.pod, p.container, local, remote)
				if err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				return uploadToast(pod, res)
			},
		}
	}
}

// uploadToast reports a finished upload, and warns when part of a directory was
// left out (symlinks, devices, unreadable placeholders), naming the first few so
// the user is not left assuming the whole tree arrived.
func uploadToast(pod string, res cp.Result) msg.Toast {
	n := len(res.Skipped)
	if n == 0 {
		return msg.Toast{Text: "uploaded to " + pod, Level: msg.LevelSuccess}
	}
	names := res.Skipped
	if len(names) > 3 {
		names = append(names[:3:3], "…")
	}
	why := "not regular files"
	if n == 1 {
		why = "not a regular file"
	}
	return msg.Toast{
		Text:  fmt.Sprintf("uploaded to %s · skipped %d (%s): %s", pod, n, why, strings.Join(names, ", ")),
		Level: msg.LevelWarn,
	}
}

// download collects "remotePath localPath" and streams the pod file to the local
// path off the UI thread.
func (p *cpMenuPage) download() tea.Cmd {
	pod := p.pod
	const usage = "remotePath localPath"
	return func() tea.Msg {
		return PromptRequest{
			Title:    "Download · " + pod,
			Label:    usage,
			Validate: func(v string) error { _, _, err := twoPaths(v, usage); return err },
			Action: func(v string) tea.Msg {
				remote, local, err := twoPaths(v, usage)
				if err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				res, err := cp.CopyFromPod(p.sess.Context(), p.sess.RestCfg, p.sess.CS, p.namespace, p.pod, p.container, remote, local)
				if err != nil {
					return msg.Toast{Text: err.Error(), Level: msg.LevelError}
				}
				return downloadToast(pod, res)
			},
		}
	}
}

// downloadToast reports a finished download, and says so when the files on disk
// differ from the pod's: on Windows a pod file called "app-10:00.log" is saved as
// "app-10_00.log", and of "Makefile" and "makefile" only one can exist. The toast
// is a single line, so the loss comes first and turns it into a warning.
func downloadToast(pod string, res cp.Result) msg.Toast {
	text, level := "downloaded from "+pod, msg.LevelSuccess
	if n := len(res.Overwritten); n > 0 {
		text += fmt.Sprintf(" · %d overwritten (same name on Windows: %s)", n, res.Overwritten[0])
		level = msg.LevelWarn
	}
	if res.Renamed > 0 {
		text += fmt.Sprintf(" · %d renamed to be valid on Windows", res.Renamed)
	}
	return msg.Toast{Text: text, Level: level}
}

// twoPaths parses a cp prompt line into exactly two non-empty paths, or explains
// what was expected using usage ("localPath remotePath"). The prompt validates
// with it too, so a line that does not parse keeps the prompt open with the
// text still there to fix.
func twoPaths(v, usage string) (a, b string, err error) {
	f, err := splitPaths(v)
	if err != nil {
		return "", "", fmt.Errorf("%v; expected: %s", err, usage)
	}
	if len(f) != 2 || f[0] == "" || f[1] == "" {
		return "", "", fmt.Errorf(`expected: %s (put "quotes" around a path with spaces)`, usage)
	}
	return f[0], f[1], nil
}

// splitPaths splits a prompt line into whitespace-separated paths. A double-quoted
// span belongs to the path it is in, spaces and all, and its quotes are dropped,
// so a path quoted by hand or pasted from Explorer's "Copy as path" works:
// "C:\Users\First Last\out.txt" is one path, and so is the quoted form of a path
// with no spaces, which Explorer always produces. Nothing else is special.
// Backslashes are literal, so Windows separators survive; a POSIX shell splitter
// (shlex, go-shellquote) would read "C:\Users" as an escaped "U". A single quote
// is literal too, because apostrophes are ordinary in Windows names
// (C:\Users\O'Brien) and Explorer never uses them. An unclosed quote is an
// error rather than a guess.
func splitPaths(v string) ([]string, error) {
	var (
		fields  []string
		cur     strings.Builder
		inField bool // a field has started, even if it is only "" so far
		quoted  bool
	)
	for _, r := range v {
		switch {
		case r == '"':
			quoted, inField = !quoted, true
		case !quoted && unicode.IsSpace(r):
			if inField {
				fields = append(fields, cur.String())
				cur.Reset()
				inField = false
			}
		default:
			cur.WriteRune(r)
			inField = true
		}
	}
	if quoted {
		return nil, fmt.Errorf(`unclosed " quote`)
	}
	if inField {
		fields = append(fields, cur.String())
	}
	return fields, nil
}

func (p *cpMenuPage) View(width, height int) string {
	t := p.theme
	var b strings.Builder
	b.WriteString(t.ColHeader.Render("  COPY") + "\n")
	for i, it := range p.items {
		marker := "  "
		nameStyle := t.Dim
		if i == p.cursor {
			marker = t.AccentText.Render("▶ ")
			nameStyle = t.Base
		}
		b.WriteString(marker +
			nameStyle.Render(pad(it.label, 12)) +
			t.Faint.Render(trunc(it.desc, width-18)) + "\n")
	}
	b.WriteString("\n" + t.Faint.Render("  enter copy · esc back"))
	return b.String()
}
