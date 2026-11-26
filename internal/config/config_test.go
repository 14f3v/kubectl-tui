package config

import (
	"path/filepath"
	"testing"
)

func TestConfigDir(t *testing.T) {
	t.Setenv("XDG_CONFIG_HOME", "/x")
	if got, want := Dir(), filepath.FromSlash("/x/kubetui"); got != want {
		t.Fatalf("Dir() = %q, want %q", got, want)
	}
}
