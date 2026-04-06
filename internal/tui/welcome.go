package tui

import (
	"fmt"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// ASCII art banner for wuu.
const wuuBanner = `
 ██╗    ██╗██╗   ██╗██╗   ██╗
 ██║    ██║██║   ██║██║   ██║
 ██║ █╗ ██║██║   ██║██║   ██║
 ██║███╗██║██║   ██║██║   ██║
 ╚███╔███╔╝╚██████╔╝╚██████╔╝~
  ╚══╝╚══╝  ╚═════╝  ╚═════╝`

// welcomeScreen renders the startup banner with session info.
func welcomeScreen(width int, provider, model, sessionID string) string {
	if width < 40 {
		return welcomeCompact(provider, model, sessionID)
	}

	bannerStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("12")).
		Bold(true)

	subtitleStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("8"))

	infoStyle := lipgloss.NewStyle().
		Foreground(lipgloss.Color("7"))

	var b strings.Builder

	// Center the banner.
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
	b.WriteString(subtitleStyle.Render(subtitle))
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
	b.WriteString(infoStyle.Render(info))
	b.WriteString("\n\n")

	// Hints.
	hints := []string{
		"Type a prompt to start  ·  /help for commands  ·  /resume to restore a session",
	}
	for _, h := range hints {
		hPad := (width - len(h)) / 2
		if hPad < 0 {
			hPad = 0
		}
		b.WriteString(strings.Repeat(" ", hPad))
		b.WriteString(subtitleStyle.Render(h))
		b.WriteString("\n")
	}

	return b.String()
}

func welcomeCompact(provider, model, sessionID string) string {
	var b strings.Builder
	b.WriteString("wuu · coding agent\n\n")
	b.WriteString(fmt.Sprintf("%s/%s\n", provider, model))
	if sessionID != "" {
		b.WriteString(fmt.Sprintf("session: %s\n", sessionID))
	}
	b.WriteString("\nType a prompt or /help")
	return b.String()
}
