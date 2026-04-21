package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// truncateToWidth cuts a string (possibly with ANSI codes) to a visual width.
func truncateToWidth(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}

// fitToWidth ensures the returned string has exactly the target visual width.
func fitToWidth(s string, width int) string {
	if width <= 0 {
		return ""
	}

	out := truncateToWidth(s, width)
	current := lipgloss.Width(out)
	if current < width {
		out += strings.Repeat(" ", width-current)
	}
	return out
}
