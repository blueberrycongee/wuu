package tui

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/stringutil"
	"github.com/charmbracelet/lipgloss"
)

// Codex-aligned tool card styles: lightweight tree indentation instead
// of heavy box-drawing borders. Tool calls render as:
//
//	• Called read_file · main.go
//	  └ package main...
//
// Running tools show a spinner, success shows green •, failure red •.
var (
	toolBulletSuccess = lipgloss.NewStyle().Foreground(lipgloss.Color("2")).Bold(true)  // green
	toolBulletFail    = lipgloss.NewStyle().Foreground(lipgloss.Color("1")).Bold(true)  // red
	toolVerbStyle     = lipgloss.NewStyle().Bold(true)
	toolNameStyle     = lipgloss.NewStyle().Foreground(lipgloss.Color("6")) // cyan
	toolMetaDim       = lipgloss.NewStyle().Faint(true)
	toolResultDim     = lipgloss.NewStyle().Faint(true)
	toolTreeBranch    = toolMetaDim.Render("  └ ")
	toolTreeIndent    = "    " // continuation indent after └
)

// renderToolCard renders a single tool call in Codex-aligned tree style.
func renderToolCard(tc *ToolCallEntry, width int, frame int) string {
	// Running tools have an animated spinner — don't cache those.
	if tc.Status != ToolCallRunning {
		key := fmt.Sprintf("%s:%v:%d:%d", tc.Status, tc.Collapsed, len(tc.Args), len(tc.Result))
		if tc.cachedCard != "" && tc.cachedCardKey == key && tc.cachedCardWidth == width {
			return tc.cachedCard
		}
	}

	// ask_user has its own card layout.
	if tc.Name == "ask_user" {
		return renderAskUserCard(*tc, width)
	}

	// ── Header line: bullet + verb + tool_name · summary ──
	bullet := toolBullet(tc.Status, frame)
	verb := "Called"
	if tc.Status == ToolCallRunning {
		verb = "Calling"
	}

	headerParts := []string{
		bullet,
		toolVerbStyle.Render(verb),
		toolNameStyle.Render(tc.Name),
	}

	// Inline summary (path, command, pattern, etc.)
	summary := toolArgsSummary(tc.Name, tc.Args, width-lipgloss.Width(strings.Join(headerParts, " "))-6)
	if summary != "" {
		headerParts = append(headerParts, toolMetaDim.Render("· "+summary))
	}

	// Diff stats for edit/write tools.
	if tc.Result != "" {
		if dr := diffResultFromJSON(tc.Result); dr != nil {
			headerParts = append(headerParts, diffStats(dr))
		}
	}

	header := strings.Join(headerParts, " ")

	// ── Result body (tree-indented, dimmed) ──
	var result string
	if tc.Collapsed || tc.Result == "" {
		result = header
	} else {
		resultContent := formatToolResult(tc, width-4)
		if strings.TrimSpace(resultContent) == "" {
			result = header
		} else {
			result = header + "\n" + resultContent
		}
	}

	// Cache for non-running tools.
	if tc.Status != ToolCallRunning {
		tc.cachedCard = result
		tc.cachedCardKey = fmt.Sprintf("%s:%v:%d:%d", tc.Status, tc.Collapsed, len(tc.Args), len(tc.Result))
		tc.cachedCardWidth = width
	}
	return result
}

// toolBullet returns the status bullet: spinner (running), green • (done), red • (error).
func toolBullet(status ToolCallStatus, frame int) string {
	switch status {
	case ToolCallRunning:
		return waitingStatusPrefixStyle.Render(statusSpinner(frame))
	case ToolCallError:
		return toolBulletFail.Render("✗")
	default:
		return toolBulletSuccess.Render("•")
	}
}

// formatToolResult renders the tool result in tree-indented dimmed style.
func formatToolResult(tc *ToolCallEntry, maxWidth int) string {
	if tc.Result == "" {
		return ""
	}

	// Diff results get their own renderer.
	if dr := diffResultFromJSON(tc.Result); dr != nil {
		diffOut := renderDiff(dr, max(20, maxWidth))
		return indentTreeResult(diffOut, maxWidth)
	}

	// Regular results: truncate and dim.
	content := truncateToolResult(tc.Result, 500)
	content = wrapText(content, max(20, maxWidth))
	return indentTreeResult(toolResultDim.Render(content), maxWidth)
}

// indentTreeResult prefixes the first line with └ and subsequent lines
// with continuation indent, matching Codex's tree style.
func indentTreeResult(content string, _ int) string {
	lines := strings.Split(content, "\n")
	var out strings.Builder
	for i, line := range lines {
		if i == 0 {
			out.WriteString(toolTreeBranch)
		} else {
			out.WriteString(toolTreeIndent)
		}
		out.WriteString(line)
		if i < len(lines)-1 {
			out.WriteString("\n")
		}
	}
	return out.String()
}

// toolArgsSummary extracts a human-readable one-line summary from tool arguments.
func toolArgsSummary(toolName, args string, maxWidth int) string {
	if args == "" || maxWidth <= 0 {
		return ""
	}

	var parsed map[string]any
	if json.Unmarshal([]byte(args), &parsed) != nil {
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
	case "git":
		if s, ok := parsed["subcommand"].(string); ok {
			summary = s
		}
	case "spawn_agent", "fork_agent":
		if d, ok := parsed["description"].(string); ok {
			summary = d
		} else if p, ok := parsed["prompt"].(string); ok {
			if len(p) > 60 {
				summary = p[:60] + "…"
			} else {
				summary = p
			}
		}
	}

	if summary == "" {
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
