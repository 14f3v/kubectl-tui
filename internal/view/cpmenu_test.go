package view

import (
	"strings"
	"testing"

	"github.com/14f3v/kubectl-tui/internal/action/cp"
	"github.com/14f3v/kubectl-tui/internal/msg"
)

func TestDownloadToast(t *testing.T) {
	plain := downloadToast("web", cp.Result{})
	if plain.Text != "downloaded from web" || plain.Level != msg.LevelSuccess {
		t.Errorf("plain download toast = %+v", plain)
	}
	renamed := downloadToast("web", cp.Result{Renamed: 3})
	if !strings.Contains(renamed.Text, "3 renamed") || renamed.Level != msg.LevelSuccess {
		t.Errorf("renamed download toast = %+v, want it to count the renames", renamed)
	}
}
