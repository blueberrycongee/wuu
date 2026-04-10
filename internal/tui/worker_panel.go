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
	// Title row + worker rows.
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
	// Sort by StartedAt ascending (oldest first).
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
	title := fmt.Sprintf(" Active workers (%d)", len(active))
	b.WriteString(workerPanelTitleStyle.Render(trimToWidth(title, width)))

	now := time.Now()
	limit := len(active)
	if limit > workerPanelMaxRows {
		limit = workerPanelMaxRows
	}

	// Spinner frame derived from the existing inline spin counter.
	spinFrames := []rune{'⠋', '⠙', '⠹', '⠸', '⠼', '⠴', '⠦', '⠧', '⠇', '⠏'}
	spin := string(spinFrames[m.spinnerTick%len(spinFrames)])

	for i := 0; i < limit; i++ {
		s := active[i]
		elapsed := ""
		if !s.StartedAt.IsZero() {
			elapsed = formatElapsed(now.Sub(s.StartedAt))
		}
		desc := s.Description
		if desc == "" {
			desc = "(no description)"
		}
		// Compose: " ⠹ explorer-7af3 explore auth module       12s"
		left := fmt.Sprintf(" %s %-14s %s", spin, truncate(s.ID, 14), desc)
		right := elapsed
		availableW := width - lipgloss.Width(right) - 1
		if availableW < 4 {
			availableW = 4
		}
		left = trimToWidth(left, availableW)
		gap := width - lipgloss.Width(left) - lipgloss.Width(right)
		if gap < 1 {
			gap = 1
		}
		row := workerPanelRowStyle.Render(left + strings.Repeat(" ", gap) + right)
		b.WriteString("\n")
		b.WriteString(row)
	}
	return b.String()
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
	workerPanelTitleStyle = lipgloss.NewStyle().Bold(true).Foreground(currentTheme.Brand)
	workerPanelRowStyle = lipgloss.NewStyle().Foreground(currentTheme.Subtle)
}
