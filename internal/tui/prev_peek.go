package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
)

// prevEntryPeekPreviewChars caps how much of the previous entry we quote
// in the floating peek line. The peek must fit on a single row; anything
// longer and the label + role prefix gets truncated out of view.
const prevEntryPeekPreviewChars = 80

// findPrevEntryForPeek returns the index of the most recent transcript
// entry whose top has scrolled above the visible viewport top. Returns
// -1 when there isn't one (viewport is at the very top or no entries).
func (m Model) findPrevEntryForPeek() int {
	if m.viewport.YOffset <= 0 {
		return -1
	}
	bestIdx := -1
	bestStart := -1
	for i := range m.entries {
		e := m.entries[i]
		if e.Role == "TOOL" {
			continue
		}
		if e.renderStart < 0 {
			continue
		}
		if e.renderStart < m.viewport.YOffset && e.renderStart > bestStart {
			bestIdx = i
			bestStart = e.renderStart
		}
	}
	return bestIdx
}

// renderPrevEntryPeek builds the single-line floating indicator that
// appears at the top of the viewport whenever the user has scrolled
// away from the newest content. Returns "" when nothing to show.
func (m Model) renderPrevEntryPeek(width int) string {
	idx := m.findPrevEntryForPeek()
	if idx < 0 {
		return ""
	}
	e := m.entries[idx]

	// Role label gets a terse one-character icon so we don't chew up the
	// visible width. Mirrors the cues already used elsewhere in the TUI.
	roleIcon := "·"
	switch strings.ToUpper(e.Role) {
	case "USER":
		roleIcon = "›"
	case "ASSISTANT":
		roleIcon = "‹"
	}

	preview := firstMeaningfulLine(e.Content)
	preview = strings.ReplaceAll(preview, "\n", " ")
	preview = strings.Join(strings.Fields(preview), " ")
	if preview == "" {
		preview = "(empty message)"
	}
	// Reserve 40 chars for "↑ › " prefix + " · press [ to jump" hint so
	// the preview slot adapts to the actual viewport width.
	hint := " · press [ to jump"
	prefixLen := 4 // "↑ " + icon + " "
	maxPreview := width - prefixLen - ansi.StringWidth(hint)
	if maxPreview > prevEntryPeekPreviewChars {
		maxPreview = prevEntryPeekPreviewChars
	}
	if maxPreview < 12 {
		maxPreview = 12
	}
	if ansi.StringWidth(preview) > maxPreview {
		preview = ansi.Truncate(preview, maxPreview, "…")
	}

	label := fmt.Sprintf("↑ %s %s%s", roleIcon, preview, hint)
	dim := lipgloss.NewStyle().Foreground(currentTheme.Subtle)
	return dim.Render(label)
}

// overlayPrevEntryPeek replaces the first visual line of the viewport's
// rendered output with the peek label. We cover the existing line rather
// than prepend a new one so the viewport height stays stable and the
// peek stays "glued" to the top regardless of scroll position.
func overlayPrevEntryPeek(output, peek string) string {
	if peek == "" {
		return output
	}
	idx := strings.Index(output, "\n")
	if idx < 0 {
		return peek
	}
	return peek + output[idx:]
}

// jumpToPrevEntry scrolls the viewport so the previous entry's top edge
// aligns with the viewport top. Returns true when a jump happened.
func (m *Model) jumpToPrevEntry() bool {
	idx := m.findPrevEntryForPeek()
	if idx < 0 {
		return false
	}
	m.setViewportOffset(m.entries[idx].renderStart)
	return true
}

// peekBarClick reports whether a mouse press lands on the row where the
// peek bar is rendered (row 0 of the chat viewport).
func (m *Model) peekBarClick(x, y int) bool {
	if m.viewport.YOffset <= 0 {
		return false
	}
	if y != m.layout.Chat.Y {
		return false
	}
	// Horizontal range matches the chat area (peek spans full width).
	return x >= m.layout.Chat.X && x < m.layout.Chat.X+m.layout.Chat.Width
}

// firstMeaningfulLine returns the first non-empty, trimmed line of s.
// Intended for message previews where the first line is the "subject".
func firstMeaningfulLine(s string) string {
	for _, line := range strings.Split(s, "\n") {
		if t := strings.TrimSpace(line); t != "" {
			return t
		}
	}
	return ""
}
