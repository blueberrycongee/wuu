package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ASCII art banner for wuu.
const wuuBanner = `
 ██╗    ██╗██╗   ██╗██╗   ██╗ ██╗
 ██║    ██║██║   ██║██║   ██║ ██║
 ██║ █╗ ██║██║   ██║██║   ██║ ██║
 ██║███╗██║██║   ██║██║   ██║ ╚═╝
 ╚███╔███╔╝╚██████╔╝╚██████╔╝ ██╗
  ╚══╝╚══╝  ╚═════╝  ╚═════╝  ╚═╝`

// welcomeScreen renders the startup banner with session info.
func welcomeScreen(width int, provider, model, sessionID string) string {
	if width < 40 {
		return welcomeCompact(provider, model, sessionID)
	}

	var b strings.Builder

	// Center the banner with brand color.
	for _, line := range strings.Split(strings.TrimSpace(wuuBanner), "\n") {
		pad := (width - lipgloss.Width(line)) / 2
		if pad < 0 {
			pad = 0
		}
		b.WriteString(strings.Repeat(" ", pad))
		b.WriteString(bannerStyle.Render(line))
		b.WriteString("\n")
	}

	b.WriteString("\n")

	// Subtitle.
	subtitle := "coding agent"
	pad := (width - len(subtitle)) / 2
	if pad < 0 {
		pad = 0
	}
	b.WriteString(strings.Repeat(" ", pad))
	b.WriteString(bannerSubtitleStyle.Render(subtitle))
	b.WriteString("\n\n")

	// Session info.
	info := fmt.Sprintf("%s/%s", provider, model)
	if sessionID != "" {
		info += fmt.Sprintf("  ·  session %s", sessionID)
	}
	infoPad := (width - lipgloss.Width(info)) / 2
	if infoPad < 0 {
		infoPad = 0
	}
	b.WriteString(strings.Repeat(" ", infoPad))
	b.WriteString(bannerInfoStyle.Render(info))
	b.WriteString("\n\n")

	// Hints.
	hint := "Type a prompt to start  ·  /help for commands  ·  /resume to restore a session"
	hPad := (width - len(hint)) / 2
	if hPad < 0 {
		hPad = 0
	}
	b.WriteString(strings.Repeat(" ", hPad))
	b.WriteString(bannerSubtitleStyle.Render(hint))
	b.WriteString("\n")

	return b.String()
}

func welcomeCompact(provider, model, sessionID string) string {
	var b strings.Builder
	b.WriteString(bannerStyle.Render("wuu"))
	b.WriteString(bannerSubtitleStyle.Render(" · coding agent"))
	b.WriteString("\n\n")
	b.WriteString(bannerInfoStyle.Render(fmt.Sprintf("%s/%s", provider, model)))
	b.WriteString("\n")
	if sessionID != "" {
		b.WriteString(bannerInfoStyle.Render(fmt.Sprintf("session: %s", sessionID)))
		b.WriteString("\n")
	}
	b.WriteString("\n")
	b.WriteString(bannerSubtitleStyle.Render("Type a prompt or /help"))
	return b.String()
}
