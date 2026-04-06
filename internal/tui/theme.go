package tui

import "github.com/charmbracelet/lipgloss"

// Theme defines the color palette for the TUI.
type theme struct {
	// Brand.
	Brand      lipgloss.Color // primary accent
	BrandLight lipgloss.Color // lighter accent for shimmer/highlights

	// Semantic.
	Success lipgloss.Color
	Error   lipgloss.Color
	Warning lipgloss.Color

	// Text.
	Text     lipgloss.Color // primary text
	Subtle   lipgloss.Color // secondary/dimmed text
	Inactive lipgloss.Color // disabled/placeholder text

	// UI chrome.
	Border      lipgloss.Color // box borders
	HeaderBg    lipgloss.Color // header background accent
	ToolBorder  lipgloss.Color // tool call borders
	InputBorder lipgloss.Color // input box border
}

// darkTheme is the default color palette.
var darkTheme = theme{
	Brand:      lipgloss.Color("#D77757"), // warm orange
	BrandLight: lipgloss.Color("#EB9F7F"), // lighter orange

	Success: lipgloss.Color("#4EBA65"), // bright green
	Error:   lipgloss.Color("#FF6B80"), // bright red
	Warning: lipgloss.Color("#FFC107"), // amber

	Text:     lipgloss.Color("#FFFFFF"), // white
	Subtle:   lipgloss.Color("#888888"), // medium gray
	Inactive: lipgloss.Color("#555555"), // dark gray

	Border:      lipgloss.Color("#555555"), // dark gray
	HeaderBg:    lipgloss.Color("#D77757"), // brand orange
	ToolBorder:  lipgloss.Color("#B1B9F9"), // blue-purple
	InputBorder: lipgloss.Color("#888888"), // medium gray
}

// currentTheme is the active theme. Swap this for light mode later.
var currentTheme = darkTheme

// Reusable styles built from the theme.
var (
	// Header: bold brand color.
	headerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(darkTheme.Brand)

	// Footer: subtle text.
	footerStyle = lipgloss.NewStyle().
			Foreground(darkTheme.Subtle)

	// User message role label.
	userLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(darkTheme.Brand)

	// Assistant message role label.
	assistantLabelStyle = lipgloss.NewStyle().
				Bold(true).
				Foreground(darkTheme.Success)

	// System message role label.
	systemLabelStyle = lipgloss.NewStyle().
				Foreground(darkTheme.Subtle).
				Italic(true)

	// Tool call header.
	toolLabelStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(darkTheme.ToolBorder)

	// Tool call body (output).
	toolOutputStyle = lipgloss.NewStyle().
			Foreground(darkTheme.Inactive)

	// Status indicators.
	statusReadyStyle     = lipgloss.NewStyle().Foreground(darkTheme.Success)
	statusStreamStyle    = lipgloss.NewStyle().Foreground(darkTheme.Brand)
	statusToolStyle      = lipgloss.NewStyle().Foreground(darkTheme.ToolBorder)
	statusErrorStyle     = lipgloss.NewStyle().Foreground(darkTheme.Error)

	// Borders.
	outputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(darkTheme.Border)

	inputBorderStyle = lipgloss.NewStyle().
				Border(lipgloss.RoundedBorder()).
				BorderForeground(darkTheme.InputBorder)

	// Banner.
	bannerStyle = lipgloss.NewStyle().
			Bold(true).
			Foreground(darkTheme.Brand)

	bannerSubtitleStyle = lipgloss.NewStyle().
				Foreground(darkTheme.Subtle)

	bannerInfoStyle = lipgloss.NewStyle().
			Foreground(darkTheme.Inactive)
)
