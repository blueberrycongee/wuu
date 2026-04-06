package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	maxDisplayChars  = 10_000
	truncateHeadLen  = 2_500
	truncateTailLen  = 2_500
)

// truncateForDisplay applies head+tail truncation to long content,
// matching Claude Code's strategy for render performance.
func truncateForDisplay(content string) string {
	if len(content) <= maxDisplayChars {
		return content
	}

	head := content[:truncateHeadLen]
	tail := content[len(content)-truncateTailLen:]

	// Count hidden lines.
	totalLines := strings.Count(content, "\n")
	headLines := strings.Count(head, "\n")
	tailLines := strings.Count(tail, "\n")
	hiddenLines := totalLines - headLines - tailLines
	if hiddenLines < 1 {
		hiddenLines = 1
	}

	indicator := lipgloss.NewStyle().
		Foreground(currentTheme.Subtle).
		Italic(true).
		Render(fmt.Sprintf("… +%d lines …", hiddenLines))

	return head + "\n" + indicator + "\n" + tail
}
