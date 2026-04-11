package tui

import (
	"fmt"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const statusAnimationInterval = 300 * time.Millisecond

var statusSpinnerFrames = []string{"·", "·", "·", "·"}

const (
	statusShimmerTrail    = 5
	statusShimmerLeadSpan = 2
)

type workPhase int

const (
	workPhaseIdle workPhase = iota
	workPhaseThinking
	workPhaseTool
	workPhaseGenerating
	workPhaseReconnecting
	workPhaseAutoResume
	workPhaseWorker
)

type workStatus struct {
	Phase   workPhase
	Label   string
	Meta    string
	Running bool
}

func deriveWorkStatus(status string) workStatus {
	switch {
	case status == "thinking":
		return workStatus{Phase: workPhaseThinking, Label: "Thinking", Meta: "Working through the next step", Running: true}
	case status == "streaming" || status == "streaming response":
		return workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true}
	case strings.HasPrefix(status, "tool:"):
		name := trimToWidth(strings.TrimSpace(strings.TrimPrefix(status, "tool:")), 36)
		if name == "" {
			name = "tool"
		}
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Running %s", name), Meta: "Making progress with a tool", Running: true}
	case strings.HasPrefix(status, "executing tool:"):
		name := trimToWidth(strings.TrimSpace(strings.TrimPrefix(status, "executing tool:")), 36)
		if name == "" {
			name = "tool"
		}
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Running %s", name), Meta: "Making progress with a tool", Running: true}
	case strings.HasPrefix(status, "Reconnecting"):
		return workStatus{Phase: workPhaseReconnecting, Label: "Reconnecting", Meta: "Restoring the live response", Running: true}
	case strings.HasPrefix(status, "auto-resume"):
		return workStatus{Phase: workPhaseAutoResume, Label: "Continuing", Meta: "Picking up after worker updates", Running: true}
	default:
		return workStatus{Phase: workPhaseIdle}
	}
}

func workerRunningStatus(desc string) workStatus {
	desc = trimToWidth(strings.TrimSpace(desc), 40)
	meta := "Running in the background"
	if desc != "" {
		meta = desc
	}
	return workStatus{Phase: workPhaseWorker, Label: "Running", Meta: meta, Running: true}
}

func thinkingBlockStatus(done bool, duration time.Duration) workStatus {
	if done {
		return workStatus{
			Phase:   workPhaseThinking,
			Label:   "Thinking complete",
			Meta:    fmt.Sprintf("Finished in %.1fs", duration.Seconds()),
			Running: false,
		}
	}
	return workStatus{
		Phase:   workPhaseThinking,
		Label:   "Thinking",
		Meta:    fmt.Sprintf("Elapsed %.1fs", duration.Seconds()),
		Running: true,
	}
}

func toolCallStatus(tc ToolCallEntry) workStatus {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		name = "tool"
	}
	switch tc.Status {
	case ToolCallRunning:
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Running %s", name), Meta: "Making progress with a tool", Running: true}
	case ToolCallError:
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("%s failed", name), Meta: "Tool run failed", Running: false}
	default:
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Finished %s", name), Meta: "Tool run complete", Running: false}
	}
}

func isWaitingStatus(status string) bool {
	return deriveWorkStatus(status).Phase != workPhaseIdle
}

func nextStatusFrame(frame int) int {
	if len(statusSpinnerFrames) == 0 {
		return 0
	}
	if frame < 0 {
		frame = 0
	}
	return (frame + 1) % len(statusSpinnerFrames)
}

func statusSpinner(frame int) string {
	if len(statusSpinnerFrames) == 0 {
		return ""
	}
	if frame < 0 {
		frame = 0
	}
	return statusSpinnerFrames[frame%len(statusSpinnerFrames)]
}

func statusGlyph(ws workStatus, frame int) string {
	if ws.Running {
		return statusSpinner(frame)
	}
	switch ws.Phase {
	case workPhaseTool:
		if strings.Contains(strings.ToLower(ws.Label), "failed") {
			return "✗"
		}
		return "✓"
	case workPhaseThinking:
		return "✓"
	default:
		return "•"
	}
}

func renderShimmerText(label string, frame int, running bool) string {
	if label == "" {
		return ""
	}
	if !running {
		return waitingStatusLabelStyle.Render(label)
	}
	runes := []rune(label)
	if len(runes) == 0 {
		return ""
	}
	cycle := len(runes) + statusShimmerTrail + statusShimmerLeadSpan
	if cycle <= 0 {
		cycle = 1
	}
	offset := frame % cycle
	if offset < 0 {
		offset += cycle
	}

	var plain strings.Builder
	var styled strings.Builder
	for i, r := range runes {
		plain.WriteRune(r)
		styled.WriteString(statusShimmerStyleAt(i, offset).Render(string(r)))
	}
	return lipgloss.NewStyle().SetString(plain.String()).Value() + styled.String()
}

func statusShimmerStyleAt(idx int, offset int) lipgloss.Style {
	delta := idx - offset
	switch {
	case delta == 0:
		return waitingStatusLabelBrightStyle
	case delta > 0 && delta <= statusShimmerLeadSpan:
		return waitingStatusLabelStrongStyle
	case delta < 0 && delta >= -statusShimmerTrail:
		return waitingStatusLabelStrongStyle
	default:
		return waitingStatusLabelStyle
	}
}

func renderStatusHeader(ws workStatus, frame int) string {
	if ws.Phase == workPhaseIdle {
		return ""
	}
	parts := []string{
		waitingStatusPrefixStyle.Render(statusGlyph(ws, frame)),
		renderShimmerText(ws.Label, frame, ws.Running),
	}
	if meta := strings.TrimSpace(ws.Meta); meta != "" && meta != ws.Label {
		parts = append(parts, waitingStatusMetaStyle.Render("· "+trimToWidth(meta, 44)))
	}
	return strings.Join(parts, " ")
}

func renderInlineStatus(status string, frame int, width int) string {
	ws := deriveWorkStatus(status)
	if ws.Phase == workPhaseIdle {
		return ""
	}
	line := renderStatusHeader(ws, frame)
	if width <= 0 {
		return line
	}
	return trimToWidth(line, width)
}
