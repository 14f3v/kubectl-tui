package app

import (
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"charm.land/bubbles/v2/key"
	tea "charm.land/bubbletea/v2"
	"charm.land/lipgloss/v2"
	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/clusters"
	"github.com/14f3v/kubectl-tui/internal/k8s"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// pasteBox is the paste-a-kubeconfig box. It is a root-level modal, not a page,
// because pages only see keys after the global ones (q, :, /, digits, esc) and
// base64 almost always holds a q. It holds the text but never draws it: the box
// shows counts, the contexts found with their servers, the review and any error.
type pasteBox struct {
	buf      strings.Builder
	lines    int // line breaks in buf, counted as clusters.Normalize sees them
	report   *clusters.Report
	err      error
	cursor   int    // selected context in report.Contexts
	review   bool   // the review step is showing
	existing string // ReviewCluster: the pasted file being reviewed, "" for a new paste

	gen       int    // counts edits, so a check of older text is dropped
	current   bool   // report and err describe buf as it is now
	saveAsked bool   // ctrl+s is waiting for the check of this text
	saving    bool   // the save is in flight
	want      string // the context to select when the first report arrives
}

// pasteChecked is clusters.Inspect's verdict on a box's text, or for a review on
// its file. A result for a box since closed, or for text since edited, is
// dropped. box is only compared, never read, off the UI goroutine.
type pasteChecked struct {
	box    *pasteBox
	gen    int
	report *clusters.Report
	err    error
}

// pasteNamed is the name prompt's answer: save report under name, then connect
// to context. It carries the report the user reviewed, so the box can change
// under it without changing what is saved.
type pasteNamed struct {
	name    string
	report  *clusters.Report
	context string
}

// pasteDiscarded is the yes to closing a box that holds text.
type pasteDiscarded struct{}

// pasteSaved is the result of the save Cmd: the cluster to connect to, or why
// the save failed.
type pasteSaved struct {
	ref k8s.ClusterRef
	err error
}

// openPasteBox opens the box for a new paste or, with existing set, to review a
// pasted file that has no state record yet; that file is read in a Cmd.
func (m *Model) openPasteBox(existing string) (tea.Model, tea.Cmd) {
	if m.store == nil {
		return m.showToast(msg.Toast{Text: m.noStoreReason(), Level: msg.LevelWarn})
	}
	m.paste = &pasteBox{existing: existing}
	if existing == "" {
		return m, nil
	}
	return m, m.checkPaste()
}

// reviewCluster opens the review of a pasted file the picker found without a
// state record, so nothing in it runs before the user has seen it. The cursor
// starts on the row's context.
func (m *Model) reviewCluster(r clusters.Row) (tea.Model, tea.Cmd) {
	mdl, cmd := m.openPasteBox(r.Key.File)
	if m.paste != nil {
		m.paste.want = r.Ref.Context
	}
	return mdl, cmd
}

// checkPaste runs clusters.Inspect off the UI goroutine, on the text of a new
// paste or on the file under review. Inspect is not IO-free: ConfirmUsable opens
// the certificate and key files a kubeconfig names.
func (m *Model) checkPaste() tea.Cmd {
	box := m.paste
	gen := box.gen
	if box.existing != "" {
		path := m.store.PastePath(box.existing)
		return func() tea.Msg {
			data, err := os.ReadFile(path)
			if err != nil {
				return pasteChecked{box: box, gen: gen, err: err}
			}
			r, err := clusters.Inspect(data)
			return pasteChecked{box: box, gen: gen, report: r, err: err}
		}
	}
	// A strings.Builder never rewrites bytes it has handed out, so the Cmd can
	// keep this string while the box takes more input.
	text := box.buf.String()
	return func() tea.Msg {
		r, err := clusters.Inspect([]byte(text))
		return pasteChecked{box: box, gen: gen, report: r, err: err}
	}
}

// onPasteChecked takes a check of the text as it is now, and goes on with a
// ctrl+s that was waiting for it, unless a dialog opened meanwhile.
func (m *Model) onPasteChecked(t pasteChecked) (tea.Model, tea.Cmd) {
	b := m.paste
	if b == nil || t.box != b || t.gen != b.gen || b.current {
		return m, nil // a closed box, edited text, or a result already in
	}
	b.setReport(t.report, t.err)
	ask := b.saveAsked && m.prompt == nil && m.confirm == nil
	b.saveAsked = false
	if !ask {
		return m, nil
	}
	return m.savePasteBox()
}

// handlePasteBoxKey takes every key while the box is open, so q, :, / and digits
// are text here, as are j and k (the context list moves with the arrows). Text
// keys are not checked as they come: a paste typed in is ~10 K key messages.
func (m *Model) handlePasteBoxKey(k tea.KeyPressMsg) (tea.Model, tea.Cmd) {
	b := m.paste
	if key.Matches(k, keyForceQ) {
		return m, tea.Quit
	}
	switch k.String() {
	case "esc":
		return m.closePasteBox()
	case "ctrl+s":
		return m.savePasteBox()
	case "up":
		if b.cursor > 0 {
			b.cursor--
		}
		return m, nil
	case "down":
		if b.report != nil && b.cursor < len(b.report.Contexts)-1 {
			b.cursor++
		}
		return m, nil
	}
	if b.existing != "" {
		return m, nil // a saved file is reviewed, not edited
	}
	switch k.String() {
	case "ctrl+u":
		b.clear()
	case "backspace":
		b.backspace()
	case "enter", "ctrl+j":
		b.write("\n")
	case "tab":
		b.write("  ")
	default:
		b.write(k.Text)
	}
	return m, nil
}

// handlePasteBoxPaste appends a paste whole (never through singleLine, which
// would flatten it) and checks the text the box now holds.
func (m *Model) handlePasteBoxPaste(p tea.PasteMsg) (tea.Model, tea.Cmd) {
	b := m.paste
	if b.existing != "" || !b.write(p.Content) || b.current {
		return m, nil // a review, a refused write, or text already known too large
	}
	return m, m.checkPaste()
}

// savePasteBox is ctrl+s. It does nothing while the text has errors, shows the
// review first when the paste needs one, then asks for a name, which leads to
// the save. A saved file under review is recorded as it is, with no prompt.
// With the review showing, ctrl+s means "I've read this", so it does nothing
// while any line of the review is cut off the screen; the footer says so.
func (m *Model) savePasteBox() (tea.Model, tea.Cmd) {
	b := m.paste
	switch {
	case b.saving || b.saveAsked:
		return m, nil
	case !b.current:
		b.saveAsked = true
		return m, m.checkPaste()
	case b.err != nil || b.report == nil:
		return m, nil
	case b.report.NeedsReview() && !b.review:
		b.review = true
		return m, nil
	case b.report.NeedsReview() && !m.reviewWhole():
		return m, nil
	case b.existing != "":
		// Named after the file's stem ("team" for team.yaml), which is the
		// label clusters.Build gave its unreviewed row, so approving it does
		// not rename the row. Contexts already recorded keep their names.
		b.saving = true
		name := strings.TrimSuffix(b.existing, filepath.Ext(b.existing))
		return m, recordPasteCmd(m.store, b.existing, name, b.report, b.selected())
	}
	r, context := b.report, b.selected()
	req := view.PromptRequest{
		Title:    "Name this cluster",
		Label:    "Name",
		Initial:  b.selectedDisplay(),
		Validate: validName,
		Action: func(s string) tea.Msg {
			return pasteNamed{name: strings.TrimSpace(s), report: r, context: context}
		},
	}
	return m, func() tea.Msg { return req }
}

// maxNameRunes caps a paste's name, which labels its picker rows and stems its
// file name.
const maxNameRunes = 64

// validName is the name prompt's check: a name, on one line, and short.
func validName(s string) error {
	switch {
	case strings.TrimSpace(s) == "":
		return errors.New("give it a name")
	case strings.IndexFunc(s, unicode.IsControl) >= 0:
		return errors.New("a name is one line")
	case utf8.RuneCountInString(strings.TrimSpace(s)) > maxNameRunes:
		return fmt.Errorf("keep it to %d characters", maxNameRunes)
	}
	return nil
}

// onPasteNamed starts the save of the paste the user just named.
func (m *Model) onPasteNamed(t pasteNamed) (tea.Model, tea.Cmd) {
	if m.paste == nil || m.store == nil {
		return m, nil // the box was closed meanwhile
	}
	m.paste.saving = true
	return m, savePasteCmd(m.store, t.name, t.report, t.context)
}

// onPasteSaved closes the box and connects to the saved cluster. A failed save
// leaves the box open to try again. The picker, if open, lists the new rows.
func (m *Model) onPasteSaved(t pasteSaved) (tea.Model, tea.Cmd) {
	var reload tea.Cmd
	if m.picker() != nil {
		reload = loadClustersCmd(m.store)
	}
	b := m.paste
	if b == nil || !b.saving {
		return m, reload // closed while saving: the file stays, nothing connects
	}
	b.saving = false
	if t.err != nil {
		_, tick := m.showToast(msg.Toast{Text: "save kubeconfig: " + t.err.Error(), Level: msg.LevelError})
		return m, tea.Batch(tick, reload)
	}
	m.paste = nil
	mdl, connect := m.switchContext(t.ref)
	return mdl, tea.Batch(reload, connect)
}

// closePasteBox is esc: it closes at once when nothing was received, and asks
// first when it would throw text away.
func (m *Model) closePasteBox() (tea.Model, tea.Cmd) {
	if m.paste.buf.Len() == 0 {
		m.paste = nil
		return m, nil
	}
	req := view.ConfirmRequest{
		Title:  "discard paste",
		Prompt: "Close the box and forget what was pasted?",
		Action: func() tea.Msg { return pasteDiscarded{} },
	}
	return m, func() tea.Msg { return req }
}

// savePasteCmd saves the reviewed text in kubetui's clusters folder under name,
// records every context in it, and asks to connect to context.
func savePasteCmd(s *clusters.Store, name string, r *clusters.Report, context string) tea.Cmd {
	return func() tea.Msg {
		file, err := s.SavePaste(name, r.Data)
		if err != nil {
			return pasteSaved{err: err}
		}
		return recordPaste(s, file, name, r, context, true)
	}
}

// recordPasteCmd records a pasted file that was reviewed in the box, keeping
// the names its recorded contexts already have.
func recordPasteCmd(s *clusters.Store, file, name string, r *clusters.Report, context string) tea.Cmd {
	return func() tea.Msg { return recordPaste(s, file, name, r, context, false) }
}

// recordPaste writes the state record of every context in a pasted file: its
// name in the picker, and the login commands the user approved by saving it.
// The file is no longer listed as needing review once this is done. Records
// are keyed by each context's raw name, the one clusters.Build lists the file's
// rows under, and context is a raw name too.
//
// rename gives every context the name: a new paste, or one whose text is
// already saved, which this relabels. Without it, the review of a saved file,
// name only fills a context that has none, so names the user gave stay.
func recordPaste(s *clusters.Store, file, name string, r *clusters.Report, context string, rename bool) pasteSaved {
	path := s.PastePath(file)
	approved := r.ExecCommands()
	for _, c := range r.Contexts {
		ref := k8s.ClusterRef{Source: k8s.SourcePasted, Path: path, Context: c.Name}
		if err := s.Update(clusters.KeyFor(ref, ""), func(e *clusters.Entry) {
			if rename || e.Name == "" {
				e.Name = name
			}
			e.ApprovedExec = approved
		}); err != nil {
			return pasteSaved{err: err}
		}
	}
	return pasteSaved{ref: k8s.ClusterRef{Source: k8s.SourcePasted, Path: path, Context: context}}
}

// write appends s, unless the text is already over clusters.MaxPasteBytes.
// Going over it shows ErrTooLarge at once and stops input; ctrl+u starts over.
// It reports whether the text changed.
func (b *pasteBox) write(s string) bool {
	if s == "" || b.tooLarge() {
		return false
	}
	if strings.HasSuffix(b.buf.String(), "\r") && strings.HasPrefix(s, "\n") {
		b.lines-- // a CRLF split across two pastes is one break
	}
	b.buf.WriteString(s)
	b.lines += lineBreaks(s)
	b.edited()
	if b.tooLarge() {
		b.report, b.err, b.current = nil, clusters.ErrTooLarge, true
	}
	return true
}

// backspace removes the last rune, not the last byte, so the text stays UTF-8.
func (b *pasteBox) backspace() {
	s := b.buf.String()
	if s == "" || b.tooLarge() {
		return
	}
	_, size := utf8.DecodeLastRuneInString(s)
	s = s[:len(s)-size]
	b.buf.Reset()
	b.buf.WriteString(s)
	b.lines = lineBreaks(s)
	b.edited()
}

// clear is ctrl+u: it empties the box, which also lifts the size limit's stop.
func (b *pasteBox) clear() {
	b.buf.Reset()
	b.lines, b.cursor = 0, 0
	b.report, b.err = nil, nil
	b.edited()
}

// edited marks the report as one of older text. The review it showed was of
// that text too, so ctrl+s checks and reviews again.
func (b *pasteBox) edited() {
	b.gen++
	b.current, b.review, b.saveAsked = false, false, false
}

func (b *pasteBox) tooLarge() bool { return b.buf.Len() > clusters.MaxPasteBytes }

// setReport takes a check of the text as it is now. The selected context stays
// selected if the report still has it; otherwise the cursor goes to want, then
// to the paste's current-context, then to the first. Contexts are matched by
// their raw names, which are what want and the current-context hold.
func (b *pasteBox) setReport(r *clusters.Report, err error) {
	prev := b.selected()
	b.report, b.err, b.current, b.review, b.cursor = r, err, true, false, 0
	if r == nil {
		return
	}
	for _, name := range []string{prev, b.want, r.CurrentContext} {
		for i, c := range r.Contexts {
			if name != "" && c.Name == name {
				b.cursor = i
				return
			}
		}
	}
}

// selected is the raw name of the context the cursor is on, "" with no report:
// the context to connect to and to record.
func (b *pasteBox) selected() string {
	if b.report == nil || b.cursor >= len(b.report.Contexts) {
		return ""
	}
	return b.report.Contexts[b.cursor].Name
}

// selectedDisplay is the selected context's name as it is drawn.
func (b *pasteBox) selectedDisplay() string {
	if b.report == nil || b.cursor >= len(b.report.Contexts) {
		return ""
	}
	return b.report.Contexts[b.cursor].Display
}

// lineCount is the number of lines received, counting a last one with no break.
func (b *pasteBox) lineCount() int {
	s := b.buf.String()
	if s != "" && !strings.HasSuffix(s, "\n") && !strings.HasSuffix(s, "\r") {
		return b.lines + 1
	}
	return b.lines
}

// lineBreaks counts line breaks as clusters.Normalize sees them: a CRLF, a lone
// CR and a lone LF are one each.
func lineBreaks(s string) int {
	return strings.Count(s, "\n") + strings.Count(s, "\r") - strings.Count(s, "\r\n")
}

// renderPasteBox draws the box in the content area.
func (m *Model) renderPasteBox(h int) string {
	box, _ := m.pasteBoxLayout(h)
	return box
}

// reviewWhole reports whether the paste box, as render draws it now, shows
// every line of its review. Nothing is drawn before the screen has a size.
func (m *Model) reviewWhole() bool {
	if m.width <= 0 || m.height <= 0 {
		return false
	}
	_, whole := m.pasteBoxLayout(m.contentHeight())
	return whole
}

// pasteBoxLayout lays the box out in h lines and reports whether every line of
// the review is among them. It never draws the text the box holds: only the
// counts, the review, the contexts with their servers and problems, any error
// and the keys. Everything else it shows came through clusters.Inspect, which
// strips control characters.
//
// The review comes before the contexts, so a paste with many of them cannot
// push the login command off the screen, and the contexts collapse to the
// selected one when the whole list does not fit. On a screen too short for the
// review even so, the footer says to make the window taller instead of
// offering ctrl+s as "I've read this".
func (m *Model) pasteBoxLayout(h int) (box string, reviewWhole bool) {
	t, b := m.theme, m.paste
	w := max(m.width-4, 10) // two cells of margin on each side
	errText := lipgloss.NewStyle().Foreground(t.Pal.Err)
	warnText := lipgloss.NewStyle().Foreground(t.Pal.Warn)
	wrap := func(indent int, st lipgloss.Style, s string) []string {
		pad := strings.Repeat(" ", 2+indent)
		var out []string
		for _, l := range strings.Split(ansi.Wrap(s, max(w-indent, 10), ""), "\n") {
			out = append(out, pad+st.Render(l))
		}
		return out
	}
	add := func(dst *[]string, indent int, st lipgloss.Style, s string) {
		*dst = append(*dst, wrap(indent, st, s)...)
	}

	var head []string
	title, intro := "paste a kubeconfig", "Paste it now (ctrl+shift+v or right-click in most terminals)."
	if b.existing != "" {
		title = "review a saved kubeconfig"
		intro = singleLine(b.existing) + " is in kubetui's clusters folder but was never reviewed. Check what it does before connecting."
	}
	add(&head, 0, t.AccentText.Bold(true), title)
	head = append(head, "")
	add(&head, 0, t.Base, intro)
	head = append(head, "")
	if b.existing == "" {
		lines := fmt.Sprintf("%d lines", b.lineCount())
		if b.lineCount() == 1 {
			lines = "1 line"
		}
		add(&head, 0, t.Dim, fmt.Sprintf("received %s, %.1f KB", lines, float64(b.buf.Len())/1024))
	}

	r := b.report
	var review []string
	if r != nil && r.NeedsReview() {
		review = append(review, "")
		if !b.review {
			add(&review, 0, warnText, "This kubeconfig needs a review: ctrl+s shows what connecting with it does.")
		} else {
			add(&review, 0, warnText, "Check what connecting with it does:")
			for _, e := range r.Exec {
				add(&review, 2, t.Base, "• user "+e.User+" logs in by running this command, every time kubetui connects:")
				add(&review, 6, t.Base, strings.Join(append([]string{e.Command}, e.Args...), " "))
				if len(e.EnvNames) > 0 {
					add(&review, 6, t.Dim, "with env "+strings.Join(e.EnvNames, ", ")+" (values hidden)")
				}
				if e.InteractiveMode != "" {
					add(&review, 6, t.Dim, "interactive mode: "+e.InteractiveMode)
				}
			}
			for _, a := range r.AuthProviders {
				issuer := a.IssuerURL
				if issuer == "" {
					issuer = "its issuer"
				}
				add(&review, 2, t.Base, "• user "+a.User+" logs in with the "+a.Name+" auth-provider: it contacts "+issuer+" and writes refreshed tokens into the saved file")
			}
			for _, f := range r.LocalFiles {
				add(&review, 2, t.Base, "• reads "+f+" on this computer and sends what is in it to the server")
			}
			for _, c := range r.Insecure {
				add(&review, 2, t.Base, "• skips TLS verification for cluster "+c+" (insecure-skip-tls-verify)")
			}
		}
	}

	// contexts lists every context, or with only set just the selected one and
	// how many more there are. A problem goes on its own lines under the name,
	// wrapped rather than cut at the edge.
	contexts := func(only bool) []string {
		if r == nil {
			return nil
		}
		nameW := 0
		for _, c := range r.Contexts {
			nameW = max(nameW, ansi.StringWidth(c.Display))
		}
		nameW = min(nameW, 32)
		var out []string
		for i, c := range r.Contexts {
			if only && i != b.cursor {
				continue
			}
			label, marker, st := "           ", "  ", t.Dim
			if len(out) == 0 {
				label = "contexts:  "
			}
			if i == b.cursor {
				marker, st = "› ", t.Base
			}
			out = append(out, "  "+t.Dim.Render(label)+t.AccentText.Render(marker)+
				st.Render(padRight(ansi.Truncate(c.Display, nameW, "…"), nameW)+"   "+c.Server))
			if c.Problem != "" {
				add(&out, 13, errText, "✗ "+c.Problem) // under the name
			}
		}
		if only {
			add(&out, 13, t.Dim, fmt.Sprintf("%d more (↑↓)", len(r.Contexts)-1))
		}
		return out
	}

	var tail []string
	if b.err != nil {
		tail = append(tail, "")
		add(&tail, 0, errText, "✗ "+singleLine(b.err.Error()))
	}

	assemble := func(ctxs []string) []string {
		body := append(append([]string{}, head...), review...)
		if len(ctxs) > 0 && len(review) > 0 {
			body = append(body, "")
		}
		return append(append(body, ctxs...), tail...)
	}
	reviewEnd := len(head) + len(review) // the review is body[len(head):reviewEnd]

	// cut says the review does not fit: the footer then says so rather than
	// offer ctrl+s. The keys always show, wrapped rather than cut.
	foot := func(cut bool) string {
		keys := "ctrl+s save · esc cancel"
		switch {
		case b.review && cut:
			keys = "make the window taller to see the whole review · esc cancel"
		case b.review:
			keys = "ctrl+s I've read this, save and connect · esc cancel"
		case r != nil && r.NeedsReview():
			keys = "ctrl+s review · esc cancel"
		}
		if r != nil && len(r.Contexts) > 1 {
			keys = "↑↓ context · " + keys
		}
		return strings.Join(wrap(0, t.Faint, keys), "\n")
	}
	// lay fits the body into the room the footer leaves: the whole list of
	// contexts, else the collapsed one, and on a short screen the start of the
	// body and a line saying how much is cut. It reports whether the review is
	// all in it.
	lay := func(foot string) (string, bool) {
		room := h - 1 - strings.Count(foot, "\n") - 1 // a blank line, then the keys
		if room < 1 {
			return fitBlock(foot, m.width, h), len(review) == 0
		}
		body := assemble(contexts(false))
		if len(body) > room && r != nil && len(r.Contexts) > 1 {
			body = assemble(contexts(true))
		}
		whole := true
		if len(body) > room {
			more := len(body) - room + 1
			whole = reviewEnd <= room-1
			body = append(body[:room-1], "  "+warnText.Render(fmt.Sprintf("… %d more lines (make the window taller)", more)))
		}
		return fitBlock(strings.Join(body, "\n")+"\n\n"+foot, m.width, h), whole
	}
	// Whether the review fits is decided with the footer that offers ctrl+s,
	// the one it would be read with.
	box, reviewWhole = lay(foot(false))
	if b.review && !reviewWhole {
		box, _ = lay(foot(true))
	}
	return box, reviewWhole
}
