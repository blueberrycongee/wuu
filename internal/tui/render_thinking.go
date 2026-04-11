package tui

import (
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

// renderThinkingBlock renders the thinking indicator and optional content.
func renderThinkingBlock(content string, done bool, expanded bool, duration time.Duration, width int, tick int) string {
	contentStyle := lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(currentTheme.Border)

	header := renderStatusHeader(thinkingBlockStatus(done, duration), tick)
	if !expanded || strings.TrimSpace(content) == "" {
		return header
	}

	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}
	wrapped := wrapText(content, innerW)
	styled := contentStyle.Render(wrapped)
	box := borderStyle.Width(innerW).Render(styled)
	return header + "\n" + box
}
