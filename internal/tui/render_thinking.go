package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderThinkingBlock renders the thinking indicator and optional content.
func renderThinkingBlock(content string, done bool, expanded bool, duration time.Duration, width int, tick int) string {
	contentStyle := lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	borderColor := currentTheme.Border
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
		preview := trimToWidth(strings.Join(strings.Fields(trimmed), " "), max(24, width-20))
		if preview != "" {
			return header + "\n" + indentLines(waitingStatusMetaStyle.Render(preview), 2)
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
