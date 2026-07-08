// Package config loads the optional user configuration from
// ~/.config/kubetui/config.yaml (or $XDG_CONFIG_HOME). A missing or malformed
// file is not an error: the defaults are used.
package config

import (
	"os"
	"path/filepath"

	"sigs.k8s.io/yaml"

	"github.com/14f3v/kubectl-tui/internal/style"
)

// Config is the user-tunable configuration.
type Config struct {
	// Accent is the accent color: a preset name (indigo|green|teal|pink) or a hex
	// string like "#6366F1".
	Accent string `json:"accent"`
	// Density is "comfortable" or "compact".
	Density string `json:"density"`
	// ReadOnly disables every mutating action when true.
	ReadOnly bool `json:"readOnly"`
	// TierLabel is the tenant label key used for the TIER column.
	TierLabel string `json:"tierLabel"`
	// Favorites are namespaces surfaced first in pickers.
	Favorites []string `json:"favorites"`
}

// Default returns the built-in configuration.
func Default() Config {
	return Config{
		Accent:    "indigo",
		Density:   "comfortable",
		ReadOnly:  false,
		TierLabel: "tier",
	}
}

// DensityValue maps the density string to the style enum.
func (c Config) DensityValue() style.Density {
	if c.Density == "compact" {
		return style.Compact
	}
	return style.Comfortable
}

// Path returns the config file path, honoring XDG_CONFIG_HOME.
func Path() string {
	base := os.Getenv("XDG_CONFIG_HOME")
	if base == "" {
		home, err := os.UserHomeDir()
		if err != nil {
			return ""
		}
		base = filepath.Join(home, ".config")
	}
	return filepath.Join(base, "kubetui", "config.yaml")
}

// Load reads the config file, falling back to defaults for any missing or
// malformed content. It never returns an error; a bad config should not stop the
// tool from starting.
func Load() Config {
	cfg := Default()
	path := Path()
	if path == "" {
		return cfg
	}
	data, err := os.ReadFile(path)
	if err != nil {
		return cfg
	}
	// Unmarshal onto the default so absent keys keep their defaults.
	_ = yaml.Unmarshal(data, &cfg)
	if cfg.Accent == "" {
		cfg.Accent = "indigo"
	}
	return cfg
}
