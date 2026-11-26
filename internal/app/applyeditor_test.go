package app

import (
	"errors"
	"io/fs"
	"os"
	"strings"
	"testing"

	"github.com/charmbracelet/x/ansi"

	"github.com/14f3v/kubectl-tui/internal/action/editor"
	"github.com/14f3v/kubectl-tui/internal/msg"
	"github.com/14f3v/kubectl-tui/internal/view"
)

// A GUI editor without its wait flag hands :apply's template back untouched at
// once. That must read as the wait-flag hint, not "no changes to apply", the
// temp file must still be removed, and the hint must survive the footer at 80
// columns with its example whole. The editor is never run: After is called as
// the editor's exit would call it, a moment after the file was handed out.
func TestApplyHintsAtAnEditorThatDidNotWait(t *testing.T) {
	m := navModel(t, nil)
	_, cmd := m.applyCommand()
	if cmd == nil {
		t.Fatal("applyCommand returned no command")
	}
	req, ok := cmd().(view.ExecRequest)
	if !ok || req.Process == nil || req.After == nil {
		t.Fatalf("applyCommand's command = %#v, want an editor ExecRequest", req)
	}
	path := req.Process.Args[len(req.Process.Args)-1]
	if _, err := os.Stat(path); err != nil {
		t.Fatalf("the editor's last argument %q is not the temp file: %v", path, err)
	}

	toast, ok := req.After(nil).(msg.Toast)
	if !ok || toast.Level != msg.LevelWarn || !strings.Contains(toast.Text, "wait flag") {
		t.Fatalf("After(nil) = %#v, want the wait-flag warning", toast)
	}
	if _, err := os.Stat(path); !errors.Is(err, fs.ErrNotExist) {
		t.Errorf("temp file %s still there after the editor returned (stat err %v)", path, err)
	}

	m.width = 80
	m.showToast(toast)
	if got := ansi.Strip(m.renderToast()); !strings.Contains(got, editor.QuickExitHint(0)) {
		t.Errorf("toast at 80 columns = %q, want the hint whole, example included", got)
	}
}
