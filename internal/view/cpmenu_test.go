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
	clash := downloadToast("web", cp.Result{Renamed: 1, Overwritten: []string{"src/makefile", "src/a_b"}})
	if !strings.Contains(clash.Text, "2 overwritten") || !strings.Contains(clash.Text, "src/makefile") || clash.Level != msg.LevelWarn {
		t.Errorf("clash download toast = %+v, want a warning naming the first clash", clash)
	}
	if i, j := strings.Index(clash.Text, "overwritten"), strings.Index(clash.Text, "renamed"); i < 0 || j < 0 || i > j {
		t.Errorf("clash download toast = %q, want the loss before the renames", clash.Text)
	}
}

func TestUploadToast(t *testing.T) {
	plain := uploadToast("web", cp.Result{})
	if plain.Text != "uploaded to web" || plain.Level != msg.LevelSuccess {
		t.Errorf("plain upload toast = %+v", plain)
	}
	one := uploadToast("web", cp.Result{Skipped: []string{"link"}})
	if one.Text != "uploaded to web · skipped 1 (not a regular file): link" || one.Level != msg.LevelWarn {
		t.Errorf("one-skip upload toast = %+v", one)
	}
	many := uploadToast("web", cp.Result{Skipped: []string{"a", "b", "c", "d", "e"}})
	if !strings.Contains(many.Text, "skipped 5") || !strings.HasSuffix(many.Text, "a, b, c, …") {
		t.Errorf("many-skip upload toast = %q, want the count and the first three names", many.Text)
	}
}
