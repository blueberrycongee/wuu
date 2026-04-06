package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// overlayBottom places the popup over the bottom lines of the base string,
// keeping the total line count the same (no layout shift).
func overlayBottom(base, popup string, width int) string {
	baseLines := strings.Split(base, "\n")
	popupLines := strings.Split(popup, "\n")

	popupH := len(popupLines)
	if popupH == 0 || len(baseLines) == 0 {
		return base
	}

	// If popup is taller than base, just show what fits.
	startLine := len(baseLines) - popupH
	if startLine < 0 {
		startLine = 0
		popupLines = popupLines[len(popupLines)-len(baseLines):]
	}

	// Replace bottom lines of base with popup lines.
	result := make([]string, len(baseLines))
	copy(result, baseLines)
	for i, pl := range popupLines {
		idx := startLine + i
		if idx < len(result) {
			result[idx] = pl
		}
	}

	return strings.Join(result, "\n")
}

// updateCompletion refreshes the completion popup based on current input.
func (m *Model) updateCompletion() {
	val := m.input.Value()

	// Only show completion when input starts with "/" and is a single line.
	if !strings.HasPrefix(val, "/") || strings.Contains(val, "\n") {
		m.completionVisible = false
		m.completionItems = nil
		return
	}

	// Extract the partial command name (everything after "/" up to first space).
	partial := val[1:]
	if idx := strings.IndexByte(partial, ' '); idx >= 0 {
		// Already has a space after command name — hide completion.
		m.completionVisible = false
		m.completionItems = nil
		return
	}

	partial = strings.ToLower(partial)

	// Filter matching commands.
	var matches []command
	for _, cmd := range commandRegistry {
		if cmd.Hidden {
			continue
		}
		if strings.HasPrefix(cmd.Name, partial) {
			matches = append(matches, cmd)
		}
	}

	if len(matches) == 0 {
		m.completionVisible = false
		m.completionItems = nil
		return
	}

	m.completionVisible = true
	m.completionItems = matches

	// Clamp selection index.
	if m.completionIndex >= len(matches) {
		m.completionIndex = len(matches) - 1
	}
	if m.completionIndex < 0 {
		m.completionIndex = 0
	}
}

// renderCompletion renders the slash command completion popup.
func renderCompletion(items []command, selected int, width int) string {
	if len(items) == 0 {
		return ""
	}

	maxVisible := 8
	if len(items) < maxVisible {
		maxVisible = len(items)
	}

	// Determine visible window around selected item.
	start := 0
	if selected >= maxVisible {
		start = selected - maxVisible + 1
	}
	end := start + maxVisible
	if end > len(items) {
		end = len(items)
		start = end - maxVisible
		if start < 0 {
			start = 0
		}
	}

	selectedStyle := lipgloss.NewStyle().
		Background(lipgloss.Color("4")).
		Foreground(lipgloss.Color("15")).
		Bold(true)

	normalStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	dimStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	// Calculate column widths.
	maxNameW := 0
	for _, cmd := range items[start:end] {
		nameW := len(cmd.Name) + 2 // "/" + name + space
		if nameW > maxNameW {
			maxNameW = nameW
		}
	}

	contentWidth := width - 4 // padding
	if contentWidth < 30 {
		contentWidth = 30
	}

	var lines []string
	for i := start; i < end; i++ {
		cmd := items[i]
		name := fmt.Sprintf("/%s", cmd.Name)
		desc := cmd.Description

		// Truncate description to fit.
		descW := contentWidth - maxNameW - 3
		if descW > 0 && len(desc) > descW {
			desc = desc[:descW-1] + "…"
		}

		// Pad name to align descriptions.
		padded := name + strings.Repeat(" ", maxNameW-len(name)+1)

		line := padded + dimStyle.Render(desc)
		if i == selected {
			// Re-render with selected style.
			line = selectedStyle.Render(padded+desc) + strings.Repeat(" ", max(0, contentWidth-lipgloss.Width(padded+desc)))
		} else {
			line = normalStyle.Render(padded) + dimStyle.Render(desc)
		}

		lines = append(lines, "  "+line)
	}

	// Show scroll indicator if there are more items.
	if len(items) > maxVisible {
		indicator := dimStyle.Render(fmt.Sprintf("  %d/%d commands", selected+1, len(items)))
		lines = append(lines, indicator)
	}

	popup := strings.Join(lines, "\n")

	return lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(lipgloss.Color("8")).
		Padding(0, 1).
		Width(min(contentWidth+4, width-2)).
		Render(popup)
}
