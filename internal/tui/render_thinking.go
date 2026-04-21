package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderThinkingBlock renders the thinking indicator and optional content.
// Visual hierarchy aligned with Claude Code: subtle border when done,
// brand-colored border while active, compact preview when collapsed.
func renderThinkingBlock(content string, done bool, expanded bool, duration time.Duration, width int, tick int) string {
	contentStyle := lipgloss.NewStyle().Foreground(currentTheme.ThinkingFg)
	borderColor := currentTheme.ThinkingBorder
	if !done {
		borderColor = currentTheme.Brand
	}
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(borderColor)

	header := renderStatusHeader(thinkingBlockStatus(done, duration), tick)
	trimmed := strings.TrimSpace(content)
	if trimmed == "" {
		return header
	}
	if !expanded {
		// Collapsed: show a one-line preview with an expand hint.
		preview := trimToWidth(strings.Join(strings.Fields(trimmed), " "), max(24, width-24))
		if preview != "" {
			expandHint := waitingStatusMetaStyle.Render(" · press 't' to expand")
			return header + "\n" + indentLines(waitingStatusMetaStyle.Render(preview)+expandHint, 2)
		}
		return header
	}

	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}
	wrapped := wrapText(trimmed, innerW)
	styled := contentStyle.Render(wrapped)
	box := borderStyle.Width(innerW).Render(styled)
	return header + "\n" + box
}
