// Package style holds the Theme: the design's color tokens plus the prebuilt
// lipgloss styles the UI renders with. A Theme is constructed once (on the
// program's BackgroundColorMsg) and passed by value to views; styles are never
// built inside a render loop, which is lipgloss's chief performance pitfall.
package style

import (
	"image/color"

	"charm.land/lipgloss/v2"

	"github.com/khemphetsouvannaphasy/kubectl-tui/internal/engine/columns"
)

// Density controls how tightly rows are packed. In a terminal every row is one
// line tall, so density maps to horizontal cell padding.
type Density int

const (
	// Comfortable adds a space of padding between columns.
	Comfortable Density = iota
	// Compact removes inter-column padding to fit more columns.
	Compact
)

// CellPad is the per-column horizontal padding for the density.
func (d Density) CellPad() int {
	if d == Compact {
		return 1
	}
	return 2
}

// Palette is the raw color token set. The zero value is not useful; use a preset.
type Palette struct {
	Bg, Panel                 color.Color
	Border, BorderStrong      color.Color
	Text, TextDim, TextFaint  color.Color
	Accent                    color.Color
	OK, Warn, Err, Info       color.Color
	Pink, Cyan                color.Color
}

// designPalette is the palette from the reference design.
func designPalette() Palette {
	return Palette{
		Bg:           lipgloss.Color("#0A0C12"),
		Panel:        lipgloss.Color("#0d0f17"),
		Border:       lipgloss.Color("#20242e"),
		BorderStrong: lipgloss.Color("#2b303c"),
		Text:         lipgloss.Color("#EAECF2"),
		TextDim:      lipgloss.Color("#9AA3B4"),
		TextFaint:    lipgloss.Color("#646C7D"),
		Accent:       lipgloss.Color("#6366F1"),
		OK:           lipgloss.Color("#34D399"),
		Warn:         lipgloss.Color("#FBBF24"),
		Err:          lipgloss.Color("#F87171"),
		Info:         lipgloss.Color("#60A5FA"),
		Pink:         lipgloss.Color("#F472B6"),
		Cyan:         lipgloss.Color("#4FD1C5"),
	}
}

// AccentPreset resolves a named accent to a color, falling back to indigo.
func AccentPreset(name string) color.Color {
	switch name {
	case "green":
		return lipgloss.Color("#34D399")
	case "teal":
		return lipgloss.Color("#4FD1C5")
	case "pink":
		return lipgloss.Color("#F472B6")
	case "indigo", "":
		return lipgloss.Color("#6366F1")
	default:
		return lipgloss.Color(name) // allow a raw hex string
	}
}

// Theme is the resolved, render-ready style set.
type Theme struct {
	Pal     Palette
	Density Density

	// Prebuilt base styles.
	Base       lipgloss.Style // default text on the app background
	Dim        lipgloss.Style
	Faint      lipgloss.Style
	AccentText lipgloss.Style
	PinkText   lipgloss.Style
	CyanText   lipgloss.Style

	// Chrome styles.
	HeaderLabel lipgloss.Style // accent label in the header identity column
	ColHeader   lipgloss.Style // uppercase muted table header
	SortHeader  lipgloss.Style // accented sort column header
	RowSel      lipgloss.Style // selected-row background tint
	Breadcrumb  lipgloss.Style
}

// New builds a Theme for an accent color and density.
func New(accent color.Color, density Density) Theme {
	pal := designPalette()
	if accent != nil {
		pal.Accent = accent
	}
	base := lipgloss.NewStyle().Foreground(pal.Text)
	return Theme{
		Pal:         pal,
		Density:     density,
		Base:        base,
		Dim:         lipgloss.NewStyle().Foreground(pal.TextDim),
		Faint:       lipgloss.NewStyle().Foreground(pal.TextFaint),
		AccentText:  lipgloss.NewStyle().Foreground(pal.Accent),
		PinkText:    lipgloss.NewStyle().Foreground(pal.Pink),
		CyanText:    lipgloss.NewStyle().Foreground(pal.Cyan),
		HeaderLabel: lipgloss.NewStyle().Foreground(pal.Accent).Bold(true),
		ColHeader:   lipgloss.NewStyle().Foreground(pal.TextFaint).Bold(true),
		SortHeader:  lipgloss.NewStyle().Foreground(pal.Accent).Bold(true),
		RowSel:      lipgloss.NewStyle().Background(lipgloss.Color("#1a1d2e")).Foreground(pal.Text),
		Breadcrumb:  lipgloss.NewStyle().Foreground(pal.TextFaint),
	}
}

// Default returns the indigo, comfortable theme used before config/background
// detection resolves.
func Default() Theme { return New(nil, Comfortable) }

// StatusColor maps a semantic status class to a theme color.
func (t Theme) StatusColor(c columns.StatusClass) color.Color {
	switch c {
	case columns.StatusOK:
		return t.Pal.OK
	case columns.StatusWarn:
		return t.Pal.Warn
	case columns.StatusError:
		return t.Pal.Err
	case columns.StatusInfo:
		return t.Pal.Info
	case columns.StatusMuted:
		return t.Pal.TextFaint
	default:
		return t.Pal.Text
	}
}
