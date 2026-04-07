package tui

import (
	"encoding/json"
	"strings"

	"github.com/charmbracelet/lipgloss"
)

// renderToolCard renders a single tool call card.
func renderToolCard(tc ToolCallEntry, width int) string {
	iconStyle := lipgloss.NewStyle().Foreground(currentTheme.ToolBorder)
	nameStyle := lipgloss.NewStyle().Bold(true).Foreground(currentTheme.ToolBorder)
	statusDone := lipgloss.NewStyle().Foreground(currentTheme.Success)
	statusRunning := lipgloss.NewStyle().Foreground(currentTheme.Warning)
	statusError := lipgloss.NewStyle().Foreground(currentTheme.Error)
	contentStyle := lipgloss.NewStyle().Foreground(currentTheme.Inactive)
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(currentTheme.ToolBorder)

	var b strings.Builder

	// Header line: icon + name + status
	b.WriteString(" ")
	b.WriteString(iconStyle.Render("⚡"))
	b.WriteString(" ")
	b.WriteString(nameStyle.Render(tc.Name))

	switch tc.Status {
	case ToolCallDone:
		b.WriteString("  ")
		b.WriteString(statusDone.Render("✓ done"))
	case ToolCallRunning:
		b.WriteString("  ")
		b.WriteString(statusRunning.Render("⏳ running"))
	case ToolCallError:
		b.WriteString("  ")
		b.WriteString(statusError.Render("✗ error"))
	}

	// Collapsed: header + human-readable args summary
	if tc.Collapsed {
		summary := toolArgsSummary(tc.Name, tc.Args, width-30)
		if summary != "" {
			b.WriteString(" ── ")
			b.WriteString(contentStyle.Render(summary))
		}
		return b.String()
	}

	// Expanded: show formatted args and result in a bordered box
	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}

	var content strings.Builder
	if tc.Args != "" {
		formatted := formatToolArgs(tc.Name, tc.Args)
		content.WriteString(contentStyle.Render(wrapText(formatted, innerW)))
	}
	if tc.Result != "" {
		if content.Len() > 0 {
			content.WriteString("\n")
			content.WriteString(contentStyle.Render(strings.Repeat("─", min(innerW, 40))))
			content.WriteString("\n")
		}
		content.WriteString(contentStyle.Render(wrapText(truncateToolResult(tc.Result, 500), innerW)))
	}

	if content.Len() > 0 {
		box := borderStyle.Width(innerW).Render(content.String())
		b.WriteString("\n")
		b.WriteString(box)
	}

	return b.String()
}

// toolArgsSummary extracts a human-readable one-line summary from tool arguments.
func toolArgsSummary(toolName, args string, maxWidth int) string {
	if args == "" || maxWidth <= 0 {
		return ""
	}

	var parsed map[string]any
	if json.Unmarshal([]byte(args), &parsed) != nil {
		// Fallback: strip JSON braces.
		s := strings.TrimPrefix(strings.TrimSuffix(strings.TrimSpace(args), "}"), "{")
		s = strings.TrimSpace(s)
		if len(s) > maxWidth {
			s = s[:maxWidth] + "…"
		}
		return s
	}

	var summary string
	switch toolName {
	case "read_file", "write_file", "edit_file":
		if p, ok := parsed["path"].(string); ok {
			summary = p
		}
	case "list_files":
		if p, ok := parsed["path"].(string); ok && p != "" {
			summary = p
		} else {
			summary = "."
		}
	case "run_shell":
		if c, ok := parsed["command"].(string); ok {
			summary = c
		}
	case "grep":
		if p, ok := parsed["pattern"].(string); ok {
			summary = p
		}
	case "glob":
		if p, ok := parsed["pattern"].(string); ok {
			summary = p
		}
	case "web_search":
		if q, ok := parsed["query"].(string); ok {
			summary = q
		}
	case "web_fetch":
		if u, ok := parsed["url"].(string); ok {
			summary = u
		}
	}

	if summary == "" {
		// Generic fallback: first string value.
		for _, v := range parsed {
			if s, ok := v.(string); ok && s != "" {
				summary = s
				break
			}
		}
	}

	if len(summary) > maxWidth {
		summary = summary[:maxWidth] + "…"
	}
	return summary
}

// formatToolArgs returns a human-readable multi-line format for expanded view.
func formatToolArgs(toolName, args string) string {
	var parsed map[string]any
	if json.Unmarshal([]byte(args), &parsed) != nil {
		return args
	}

	var lines []string
	for k, v := range parsed {
		switch val := v.(type) {
		case string:
			// Skip very long values (like file content) in the display.
			if len(val) > 200 {
				lines = append(lines, k+": ("+string(rune(len(val)))+" chars)")
			} else {
				lines = append(lines, k+": "+val)
			}
		default:
			b, _ := json.Marshal(val)
			lines = append(lines, k+": "+string(b))
		}
	}
	return strings.Join(lines, "\n")
}

// truncateToolResult shortens tool output for display.
func truncateToolResult(result string, maxLen int) string {
	if len(result) <= maxLen {
		return result
	}
	return result[:maxLen] + "\n… (truncated)"
}
