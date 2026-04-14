package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// Image bar styles — initialized alongside the theme.
var (
	imageBarPillStyle    lipgloss.Style
	imageBarPillSelStyle lipgloss.Style
	imageBarHintStyle    lipgloss.Style
	imageBarIconStyle    lipgloss.Style
)

func initImageBarStyles() {
	t := currentTheme
	imageBarPillStyle = lipgloss.NewStyle().
		Foreground(t.Brand).
		Bold(true)
	imageBarPillSelStyle = lipgloss.NewStyle().
		Foreground(t.Text).
		Background(t.Brand).
		Bold(true)
	imageBarHintStyle = lipgloss.NewStyle().
		Foreground(t.Subtle)
	imageBarIconStyle = lipgloss.NewStyle().
		Foreground(t.Subtle)
}

// renderImageBar produces the image attachment bar shown between the
// separator and the input box when images are pending.
//
// Layout:
//
//	 📎 [Image #1] [Image #2]            ← backspace esc
//
// When imageBarFocused is true and selectedIdx is valid, the selected
// pill is highlighted and hints show navigation keys.
func renderImageBar(count, selectedIdx int, focused bool, width int) string {
	if count == 0 {
		return ""
	}

	var b strings.Builder

	b.WriteString(imageBarIconStyle.Render(" 📎 "))

	for i := 0; i < count; i++ {
		pill := fmt.Sprintf("[Image #%d]", i+1)
		if focused && i == selectedIdx {
			b.WriteString(imageBarPillSelStyle.Render(pill))
		} else {
			b.WriteString(imageBarPillStyle.Render(pill))
		}
		if i < count-1 {
			b.WriteString(" ")
		}
	}

	// Build hint text.
	var hint string
	if focused {
		parts := []string{"backspace:remove"}
		if count > 1 {
			parts = append([]string{"←→:navigate"}, parts...)
		}
		parts = append(parts, "esc:back")
		hint = strings.Join(parts, " ")
	} else {
		hint = "↓:select"
	}

	left := b.String()
	leftW := lipgloss.Width(left)
	hintRendered := imageBarHintStyle.Render(hint)
	hintW := lipgloss.Width(hintRendered)

	gap := width - leftW - hintW
	if gap < 1 {
		gap = 1
	}

	return left + strings.Repeat(" ", gap) + hintRendered
}
