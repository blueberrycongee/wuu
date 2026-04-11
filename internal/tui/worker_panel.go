package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"

	"github.com/blueberrycongee/wuu/internal/subagent"
)

// workerPanelMaxRows caps how many active workers we render at once
// to keep the panel from eating the chat area on busy sessions.
const workerPanelMaxRows = 6

// workerPanelHeight returns the number of rows the worker activity
// panel will consume given the current Model state. Returns 0 when
// nothing is active so the layout reclaims the space.
func (m Model) workerPanelHeight() int {
	if m.coordinator == nil {
		return 0
	}
	active := m.activeWorkerSnapshots()
	if len(active) == 0 {
		return 0
	}
	rows := len(active)
	if rows > workerPanelMaxRows {
		rows = workerPanelMaxRows
	}
	return rows + 1
}

// activeWorkerSnapshots returns the currently-running sub-agent
// snapshots, sorted oldest first.
func (m Model) activeWorkerSnapshots() []subagent.SubAgentSnapshot {
	if m.coordinator == nil {
		return nil
	}
	all := m.coordinator.List()
	out := make([]subagent.SubAgentSnapshot, 0, len(all))
	for _, s := range all {
		if s.Status == subagent.StatusRunning {
			out = append(out, s)
		}
	}
	for i := 1; i < len(out); i++ {
		for j := i; j > 0 && out[j].StartedAt.Before(out[j-1].StartedAt); j-- {
			out[j], out[j-1] = out[j-1], out[j]
		}
	}
	return out
}

// renderWorkerPanel builds the panel string. Width should match the
// terminal width. Returns empty string when nothing is active.
func (m Model) renderWorkerPanel(width int) string {
	if m.coordinator == nil {
		return ""
	}
	active := m.activeWorkerSnapshots()
	if len(active) == 0 {
		return ""
	}

	var b strings.Builder
	titleStatus := workerRunningStatus(fmt.Sprintf("%d background task(s)", len(active)))
	title := fmt.Sprintf(" %s", renderStatusHeader(titleStatus, m.spinnerFrame))
	b.WriteString(workerPanelTitleStyle.Render(fitToWidth(title, width)))

	now := time.Now()
	limit := len(active)
	if limit > workerPanelMaxRows {
		limit = workerPanelMaxRows
	}

	for i := 0; i < limit; i++ {
		s := active[i]
		elapsed := ""
		if !s.StartedAt.IsZero() {
			elapsed = formatElapsed(now.Sub(s.StartedAt))
		}
		desc := s.Description
		if desc == "" {
			desc = "working"
		}
		left := strings.Join([]string{
			waitingStatusMetaStyle.Render(truncate(s.ID, 14)),
			renderStatusHeader(workerRunningStatus(desc), m.spinnerFrame+i),
		}, "  ")
		right := elapsed
		if s.InputTokens > 0 || s.OutputTokens > 0 {
			right = fmt.Sprintf("%s · %s↑/%s↓", elapsed, formatCompactNum(s.InputTokens), formatCompactNum(s.OutputTokens))
		}
		availableW := width - lipgloss.Width(right) - 1
		if availableW < 4 {
			availableW = 4
		}
		left = trimToWidth(left, availableW)
		gap := width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		raw := fitToWidth(left+strings.Repeat(" ", gap)+right, width)
		b.WriteString("\n")
		b.WriteString(workerPanelRowStyle.Render(raw))
	}
	return b.String()
}

// formatCompactNum returns a short representation of a token count.
// 1234 → "1.2k", 12345 → "12k", 1234567 → "1.2M".
func formatCompactNum(n int) string {
	if n < 1000 {
		return fmt.Sprintf("%d", n)
	}
	if n < 10000 {
		return fmt.Sprintf("%.1fk", float64(n)/1000)
	}
	if n < 1_000_000 {
		return fmt.Sprintf("%dk", n/1000)
	}
	return fmt.Sprintf("%.1fM", float64(n)/1_000_000)
}

func formatElapsed(d time.Duration) string {
	if d < time.Second {
		return "<1s"
	}
	if d < time.Minute {
		return fmt.Sprintf("%ds", int(d.Seconds()))
	}
	if d < time.Hour {
		return fmt.Sprintf("%dm%02ds", int(d.Minutes()), int(d.Seconds())%60)
	}
	return fmt.Sprintf("%dh%02dm", int(d.Hours()), int(d.Minutes())%60)
}

func truncate(s string, n int) string {
	if len(s) <= n {
		return s
	}
	if n <= 1 {
		return s[:n]
	}
	return s[:n-1] + "…"
}

var (
	workerPanelTitleStyle lipgloss.Style
	workerPanelRowStyle   lipgloss.Style
)

func initWorkerPanelStyles() {
	workerPanelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(currentTheme.Subtle)
	workerPanelRowStyle = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
}
