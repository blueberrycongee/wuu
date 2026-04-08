package markdown

import "github.com/charmbracelet/lipgloss"

// Styles defines the lipgloss styles for each markdown node type.
type Styles struct {
	H1, H2, H3, H4, H5, H6 lipgloss.Style
	Emphasis            lipgloss.Style
	Strong              lipgloss.Style
	CodeSpan            lipgloss.Style
	Blockquote          lipgloss.Style
	Link                lipgloss.Style
	OrderedListMarker   lipgloss.Style
	UnorderedListMarker lipgloss.Style
}

// DefaultStyles returns the default style palette aligned with the wuu theme.
func DefaultStyles() Styles {
	brand := lipgloss.Color("#D77757")
	brandLight := lipgloss.Color("#EB9F7F")
	success := lipgloss.Color("#4EBA65")
	toolBorder := lipgloss.Color("#B1B9F9")

	return Styles{
		H1:                  lipgloss.NewStyle().Bold(true).Italic(true).Underline(true).Foreground(brand),
		H2:                  lipgloss.NewStyle().Bold(true).Foreground(brandLight),
		H3:                  lipgloss.NewStyle().Bold(true),
		H4:                  lipgloss.NewStyle().Italic(true),
		H5:                  lipgloss.NewStyle().Italic(true),
		H6:                  lipgloss.NewStyle().Italic(true),
		Emphasis:            lipgloss.NewStyle().Italic(true),
		Strong:              lipgloss.NewStyle().Bold(true),
		CodeSpan:            lipgloss.NewStyle().Foreground(toolBorder),
		Blockquote:          lipgloss.NewStyle().Foreground(success),
		Link:                lipgloss.NewStyle().Foreground(toolBorder).Underline(true),
		OrderedListMarker:   lipgloss.NewStyle().Foreground(toolBorder),
		UnorderedListMarker: lipgloss.NewStyle().Foreground(brand),
	}
}
