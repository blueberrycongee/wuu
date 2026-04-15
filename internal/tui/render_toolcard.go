package tui

import (
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/stringutil"
	"github.com/charmbracelet/lipgloss"
)

// renderToolCard renders a single tool call card.
func renderToolCard(tc ToolCallEntry, width int, frame int) string {
	// ask_user has its own card layout that mirrors Claude Code's
	// "User answered:" rendering — nicer than dumping the JSON
	// answer payload through the generic body formatter.
	if tc.Name == "ask_user" {
		return renderAskUserCard(tc, width)
	}

	metaStyle := waitingStatusMetaStyle
	contentStyle := lipgloss.NewStyle().Foreground(currentTheme.Inactive)
	borderStyle := lipgloss.NewStyle().
		Border(lipgloss.RoundedBorder()).
		BorderForeground(currentTheme.Border)

	ws := toolCallStatus(tc)
	headerParts := []string{renderStatusHeader(ws, frame)}

	if tc.Collapsed {
		summary := toolArgsSummary(tc.Name, tc.Args, width-28)
		if summary != "" {
			headerParts = append(headerParts, metaStyle.Render("· "+summary))
		}
		if tc.Result != "" {
			if dr := diffResultFromJSON(tc.Result); dr != nil {
				headerParts = append(headerParts, diffStats(dr))
			}
		}
		return strings.Join(headerParts, " ")
	}

	var content strings.Builder
	if tc.Args != "" {
		formatted := formatToolArgs(tc.Name, tc.Args)
		content.WriteString(contentStyle.Render(wrapText(formatted, width-6)))
	}
	if tc.Result != "" {
		if content.Len() > 0 {
			content.WriteString("\n")
			content.WriteString(metaStyle.Render(strings.Repeat("─", min(width-6, 32))))
			content.WriteString("\n")
		}
		if dr := diffResultFromJSON(tc.Result); dr != nil {
			content.WriteString(renderDiff(dr, max(20, width-6)))
		} else {
			content.WriteString(contentStyle.Render(wrapText(truncateToolResult(tc.Result, 500), max(20, width-6))))
		}
	}

	if content.Len() == 0 {
		return strings.Join(headerParts, " ")
	}

	innerW := width - 4
	if innerW < 20 {
		innerW = 20
	}
	box := borderStyle.Width(innerW).Render(content.String())
	return strings.Join(headerParts, " ") + "\n" + box
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
	return stringutil.Truncate(result, maxLen, "\n… (truncated)")
}
