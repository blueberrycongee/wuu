package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

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

	// Diff.
	DiffAddBg    lipgloss.Color // insert line background
	DiffAddFg    lipgloss.Color // insert line foreground
	DiffDeleteBg lipgloss.Color // delete line background
	DiffDeleteFg lipgloss.Color // delete line foreground

	// User bubble.
	UserBubbleBg lipgloss.Color
	UserBubbleFg lipgloss.Color

	// Selection background — used as the background overlay color for
	// the chat-viewport text selection. Foreground is intentionally NOT
	// part of the selection style: the highlight only swaps the bg so
	// the original text colors (markdown styling, syntax highlighting,
	// role labels) keep showing through. Pick a color that contrasts
	// with the terminal default text color and isn't already used by
	// any other styled element in the chat (otherwise the selection
	// vanishes over those regions).
	SelectionBg lipgloss.Color
}

// darkTheme is the default color palette.
var darkTheme = theme{
	Brand:      lipgloss.Color("#D77757"), // warm orange
	BrandLight: lipgloss.Color("#EB9F7F"), // lighter orange

	Success: lipgloss.Color("#4EBA65"), // bright green
	Error:   lipgloss.Color("#FF6B80"), // bright red
	Warning: lipgloss.Color("#FFC107"), // amber

	Text:     lipgloss.Color("#E0E0E0"), // soft white
	Subtle:   lipgloss.Color("#888888"), // medium gray
	Inactive: lipgloss.Color("#555555"), // dark gray

	Border:      lipgloss.Color("#555555"), // dark gray
	HeaderBg:    lipgloss.Color("#D77757"), // brand orange
	ToolBorder:  lipgloss.Color("#B1B9F9"), // blue-purple
	InputBorder: lipgloss.Color("#888888"), // medium gray

	DiffAddBg:    lipgloss.Color("#213A2B"), // dark green
	DiffAddFg:    lipgloss.Color("#4EBA65"), // bright green
	DiffDeleteBg: lipgloss.Color("#4A221D"), // dark red
	DiffDeleteFg: lipgloss.Color("#FF6B80"), // bright red

	UserBubbleBg: lipgloss.Color("#2F3842"), // blue-gray (not pure black)
	UserBubbleFg: lipgloss.Color("#F5F7FA"), // near-white

	SelectionBg: lipgloss.Color("#264F78"), // muted dark blue (VS Code / Claude Code dark selection)
}

var lightTheme = theme{
	Brand:      lipgloss.Color("#B85A39"), // warm orange, darker for light bg
	BrandLight: lipgloss.Color("#D77757"), // lighter accent

	Success: lipgloss.Color("#2A8F45"), // deep green
	Error:   lipgloss.Color("#C33A4A"), // deep red
	Warning: lipgloss.Color("#B88700"), // deep amber

	Text:     lipgloss.Color("#1F2328"), // near-black
	Subtle:   lipgloss.Color("#5C6670"), // muted gray
	Inactive: lipgloss.Color("#8A939C"), // inactive gray

	Border:      lipgloss.Color("#C9D1D9"), // light border
	HeaderBg:    lipgloss.Color("#EDEFF2"), // light header background
	ToolBorder:  lipgloss.Color("#4C61C9"), // readable indigo
	InputBorder: lipgloss.Color("#B6BEC7"), // input border

	DiffAddBg:    lipgloss.Color("#E8F7EC"), // light green bg
	DiffAddFg:    lipgloss.Color("#1E7E34"), // dark green text
	DiffDeleteBg: lipgloss.Color("#FDECEC"), // light red bg
	DiffDeleteFg: lipgloss.Color("#A3212F"), // dark red text

	UserBubbleBg: lipgloss.Color("#EAF1FF"), // light blue bubble
	UserBubbleFg: lipgloss.Color("#1F2328"), // dark text

	SelectionBg: lipgloss.Color("#B4D5FF"), // soft pastel blue (VS Code / Claude Code light selection)
}

type themeMode string

const (
	themeModeAuto  themeMode = "auto"
	themeModeDark  themeMode = "dark"
	themeModeLight themeMode = "light"
)

// currentTheme is the active theme at runtime.
var currentTheme = darkTheme

// Reusable styles built from the theme.
var (
	// Header: bold brand color.
	headerStyle lipgloss.Style

	// Footer: subtle text.
	footerStyle lipgloss.Style

	// User message role label.
	userLabelStyle lipgloss.Style

	// Assistant message role label.
	assistantLabelStyle lipgloss.Style

	// System message role label.
	systemLabelStyle lipgloss.Style

	// Tool call header.
	toolLabelStyle lipgloss.Style

	// Tool call body (output).
	toolOutputStyle lipgloss.Style

	// Status indicators.
	statusReadyStyle  lipgloss.Style
	statusStreamStyle lipgloss.Style
	statusToolStyle   lipgloss.Style
	statusErrorStyle  lipgloss.Style

	// Borders.
	outputBorderStyle lipgloss.Style
	inputBorderStyle  lipgloss.Style

	// User message content.
	userContentStyle lipgloss.Style

	// Banner.
	bannerStyle         lipgloss.Style
	bannerSubtitleStyle lipgloss.Style
	bannerInfoStyle     lipgloss.Style

	// Inline status (below user message).
	inlineStatusTrackStyle lipgloss.Style
	inlineStatusSweepStyle lipgloss.Style
	inlineStatusLabelStyle lipgloss.Style

	waitingStatusPrefixStyle      lipgloss.Style
	waitingStatusLabelStyle       lipgloss.Style
	waitingStatusLabelStrongStyle lipgloss.Style
	waitingStatusLabelBrightStyle lipgloss.Style
	waitingStatusMetaStyle        lipgloss.Style
)

func init() {
	applyTheme(darkTheme)
}

func normalizeThemeMode(raw string) themeMode {
	switch strings.ToLower(strings.TrimSpace(raw)) {
	case "", string(themeModeAuto):
		return themeModeAuto
	case string(themeModeDark):
		return themeModeDark
	case string(themeModeLight):
		return themeModeLight
	default:
		return ""
	}
}

// SetThemeMode applies a theme mode: "auto", "dark", or "light".
func SetThemeMode(mode string) error {
	normalized := normalizeThemeMode(mode)
	if normalized == "" {
		return fmt.Errorf("invalid theme %q (expected: auto, dark, light)", mode)
	}

	switch normalized {
	case themeModeDark:
		applyTheme(darkTheme)
	case themeModeLight:
		applyTheme(lightTheme)
	default:
		if lipgloss.HasDarkBackground() {
			applyTheme(darkTheme)
		} else {
			applyTheme(lightTheme)
		}
	}
	return nil
}

func applyTheme(t theme) {
	currentTheme = t

	headerStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	footerStyle = lipgloss.NewStyle().Foreground(t.Subtle)
	userLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	assistantLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Success)
	systemLabelStyle = lipgloss.NewStyle().Foreground(t.Subtle).Italic(true)
	toolLabelStyle = lipgloss.NewStyle().Bold(true).Foreground(t.ToolBorder)
	toolOutputStyle = lipgloss.NewStyle().Foreground(t.Inactive)

	statusReadyStyle = lipgloss.NewStyle().Foreground(t.Success)
	statusStreamStyle = lipgloss.NewStyle().Foreground(t.Brand)
	statusToolStyle = lipgloss.NewStyle().Foreground(t.ToolBorder)
	statusErrorStyle = lipgloss.NewStyle().Foreground(t.Error)

	outputBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.Border)
	inputBorderStyle = lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(t.InputBorder)

	userContentStyle = lipgloss.NewStyle().
		Foreground(t.UserBubbleFg).
		Background(t.UserBubbleBg).
		Padding(0, 1)

	bannerStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	bannerSubtitleStyle = lipgloss.NewStyle().Foreground(t.Subtle)
	bannerInfoStyle = lipgloss.NewStyle().Foreground(t.Inactive)

	inlineStatusTrackStyle = lipgloss.NewStyle().Foreground(t.Inactive)
	inlineStatusSweepStyle = lipgloss.NewStyle().Bold(true).Foreground(t.BrandLight)
	inlineStatusLabelStyle = lipgloss.NewStyle().Foreground(t.Subtle)

	waitingStatusPrefixStyle = lipgloss.NewStyle().Foreground(t.Subtle)
	waitingStatusLabelStyle = lipgloss.NewStyle().Foreground(t.Subtle)
	waitingStatusLabelStrongStyle = lipgloss.NewStyle().Bold(true).Foreground(t.Brand)
	waitingStatusLabelBrightStyle = lipgloss.NewStyle().Bold(true).Foreground(t.BrandLight)
	waitingStatusMetaStyle = lipgloss.NewStyle().Foreground(t.Subtle)

	refreshTextareasForTheme()
	initPickerStyles()
	initWorkerPanelStyles()
	initProcessPanelStyles()
	initImageBarStyles()
}
