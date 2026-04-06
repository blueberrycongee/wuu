package tui

import (
	"fmt"
	"strings"
	"unicode/utf8"

	"github.com/charmbracelet/lipgloss"
)

const (
	maxDisplayRunes  = 10_000
	truncateHeadRunes = 2_500
	truncateTailRunes = 2_500
)

// truncateForDisplay applies head+tail truncation to long content.
// Cuts on rune boundaries to avoid splitting multi-byte characters.
func truncateForDisplay(content string) string {
	if utf8.RuneCountInString(content) <= maxDisplayRunes {
		return content
	}

	head := runeSlice(content, 0, truncateHeadRunes)
	tail := runeSliceFromEnd(content, truncateTailRunes)

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

// runeSlice returns the first n runes of s.
func runeSlice(s string, start, n int) string {
	i := 0
	count := 0
	for count < start && i < len(s) {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		count++
	}
	begin := i
	for count < start+n && i < len(s) {
		_, size := utf8.DecodeRuneInString(s[i:])
		i += size
		count++
	}
	return s[begin:i]
}

// runeSliceFromEnd returns the last n runes of s.
func runeSliceFromEnd(s string, n int) string {
	total := utf8.RuneCountInString(s)
	if n >= total {
		return s
	}
	return runeSlice(s, total-n, n)
}
