package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
)

const (
	scrollbarThumb = "┃"
	scrollbarTrack = "│"
)

// renderScrollbar builds a vertical scrollbar string based on content and viewport size.
// Returns an empty string if content fits within the viewport.
func renderScrollbar(height, contentSize, viewportSize, offset int) string {
	if height <= 0 || contentSize <= viewportSize {
		return ""
	}

	// Thumb size proportional to visible ratio, minimum 1.
	thumbSize := height * viewportSize / contentSize
	if thumbSize < 1 {
		thumbSize = 1
	}

	// Thumb position mapped to available track space.
	maxOffset := contentSize - viewportSize
	if maxOffset <= 0 {
		return ""
	}
	trackSpace := height - thumbSize
	thumbPos := 0
	if trackSpace > 0 {
		thumbPos = offset * trackSpace / maxOffset
		if thumbPos > trackSpace {
			thumbPos = trackSpace
		}
	}

	thumbStyle := lipgloss.NewStyle().Foreground(currentTheme.Brand)
	trackStyle := lipgloss.NewStyle().Foreground(currentTheme.Border)

	var sb strings.Builder
	for i := range height {
		if i > 0 {
			sb.WriteString("\n")
		}
		if i >= thumbPos && i < thumbPos+thumbSize {
			sb.WriteString(thumbStyle.Render(scrollbarThumb))
		} else {
			sb.WriteString(trackStyle.Render(scrollbarTrack))
		}
	}

	return sb.String()
}
