package component

import (
	"math"
	"strings"

	"charm.land/lipgloss/v2"

	"github.com/14f3v/kubectl-tui/internal/style"
)

// Gauge renders a fixed-width block bar (█ filled, ░ empty) colored by threshold:
// accent below 60%, warn 60–80%, err at or above 80% — matching the design's CPU
// and MEM header gauges.
func Gauge(pct float64, width int, theme style.Theme) string {
	if pct < 0 {
		pct = 0
	}
	if pct > 100 {
		pct = 100
	}
	n := int(math.Round(pct / 100 * float64(width)))
	if n > width {
		n = width
	}
	col := theme.Pal.Accent
	switch {
	case pct >= 80:
		col = theme.Pal.Err
	case pct >= 60:
		col = theme.Pal.Warn
	}
	filled := lipgloss.NewStyle().Foreground(col).Render(strings.Repeat("█", n))
	empty := theme.Faint.Render(strings.Repeat("░", width-n))
	return filled + empty
}

// KeyHint renders a "<key> label" pair for the header keybind grid: the key in
// pink angle brackets, the label muted.
func KeyHint(key, label string, theme style.Theme) string {
	return theme.PinkText.Render("<"+key+">") + " " + theme.Dim.Render(label)
}

// Chip renders a footer keybind pill: the key on an accent background, the label
// muted beside it.
func Chip(key, label string, theme style.Theme) string {
	k := lipgloss.NewStyle().
		Foreground(theme.Pal.Bg).
		Background(theme.Pal.Accent).
		Bold(true).
		Render(" " + key + " ")
	l := theme.Dim.Render(" " + label)
	return k + l
}

// Count renders the "total ↑ok ◐warn ✕err" summary line used in the header.
func Count(total, ok, warn, err int, theme style.Theme) string {
	var b strings.Builder
	b.WriteString(theme.Base.Render(itoa(total)))
	b.WriteString("  ")
	b.WriteString(lipgloss.NewStyle().Foreground(theme.Pal.OK).Render("↑" + itoa(ok)))
	b.WriteString("  ")
	b.WriteString(lipgloss.NewStyle().Foreground(theme.Pal.Warn).Render("◐" + itoa(warn)))
	b.WriteString("  ")
	b.WriteString(lipgloss.NewStyle().Foreground(theme.Pal.Err).Render("✕" + itoa(err)))
	return b.String()
}

func itoa(n int) string {
	if n == 0 {
		return "0"
	}
	neg := n < 0
	if neg {
		n = -n
	}
	var buf [20]byte
	i := len(buf)
	for n > 0 {
		i--
		buf[i] = byte('0' + n%10)
		n /= 10
	}
	if neg {
		i--
		buf[i] = '-'
	}
	return string(buf[i:])
}
