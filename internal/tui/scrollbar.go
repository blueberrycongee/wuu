package tui

import (
	"strings"

	"github.com/charmbracelet/lipgloss"
	"github.com/charmbracelet/x/ansi"
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

// overlayScrollbar places each scrollbar character at the rightmost column
// of the corresponding viewport line. This avoids shrinking the viewport
// width which would cause content truncation.
func overlayScrollbar(viewport, scrollbar string, totalWidth int) string {
	vLines := strings.Split(viewport, "\n")
	sLines := strings.Split(scrollbar, "\n")

	for i := range vLines {
		if i >= len(sLines) {
			break
		}
		lineW := lipgloss.Width(vLines[i])
		if lineW >= totalWidth {
			// Replace the last visible column with the scrollbar char.
			vLines[i] = truncateToWidth(vLines[i], totalWidth-1) + sLines[i]
		} else {
			// Pad to position the scrollbar at the right edge.
			pad := totalWidth - lineW - 1
			if pad < 0 {
				pad = 0
			}
			vLines[i] = vLines[i] + strings.Repeat(" ", pad) + sLines[i]
		}
	}
	return strings.Join(vLines, "\n")
}

// truncateToWidth cuts a string (possibly with ANSI codes) to a visual width.
func truncateToWidth(s string, width int) string {
	if lipgloss.Width(s) <= width {
		return s
	}
	return ansi.Truncate(s, width, "")
}
