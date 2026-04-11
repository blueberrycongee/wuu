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

type statusTextSegment struct {
	Text   string
	Base   lipgloss.Style
	Strong lipgloss.Style
	Bright lipgloss.Style
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
	if frame < 0 {
		frame = 0
	}
	return frame + 1
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

func statusTextSegments(ws workStatus) []statusTextSegment {
	var segments []statusTextSegment
	label := strings.TrimSpace(ws.Label)
	if label != "" {
		segments = append(segments, statusTextSegment{
			Text:   label,
			Base:   waitingStatusLabelStyle,
			Strong: waitingStatusLabelStrongStyle,
			Bright: waitingStatusLabelBrightStyle,
		})
	}
	if meta := strings.TrimSpace(ws.Meta); meta != "" && meta != ws.Label {
		segments = append(segments, statusTextSegment{
			Text:   " · " + trimToWidth(meta, 44),
			Base:   waitingStatusMetaStyle,
			Strong: waitingStatusLabelStrongStyle,
			Bright: waitingStatusLabelBrightStyle,
		})
	}
	return segments
}

func statusShimmerCycleLength(segments []statusTextSegment) int {
	runes := 0
	for _, segment := range segments {
		runes += len([]rune(segment.Text))
	}
	cycle := runes + statusShimmerTrail + statusShimmerLeadSpan
	if cycle <= 0 {
		cycle = 1
	}
	return cycle
}

func renderShimmerText(segments []statusTextSegment, frame int, running bool) string {
	if len(segments) == 0 {
		return ""
	}
	if !running {
		var plain strings.Builder
		for _, segment := range segments {
			plain.WriteString(segment.Base.Render(segment.Text))
		}
		return plain.String()
	}

	offset := frame % statusShimmerCycleLength(segments)
	if offset < 0 {
		offset += statusShimmerCycleLength(segments)
	}

	var styled strings.Builder
	runeIndex := 0
	for _, segment := range segments {
		for _, r := range []rune(segment.Text) {
			styled.WriteString(statusShimmerStyleAt(segment, runeIndex, offset).Render(string(r)))
			runeIndex++
		}
	}
	return styled.String()
}

func statusShimmerStyleAt(segment statusTextSegment, idx int, offset int) lipgloss.Style {
	delta := idx - offset
	switch {
	case delta == 0:
		return segment.Bright
	case delta > 0 && delta <= statusShimmerLeadSpan:
		return segment.Strong
	case delta < 0 && delta >= -statusShimmerTrail:
		return segment.Strong
	default:
		return segment.Base
	}
}

func renderStatusHeader(ws workStatus, frame int) string {
	if ws.Phase == workPhaseIdle {
		return ""
	}
	segments := statusTextSegments(ws)
	if len(segments) == 0 {
		return waitingStatusPrefixStyle.Render(statusGlyph(ws, frame))
	}
	return strings.Join([]string{
		waitingStatusPrefixStyle.Render(statusGlyph(ws, frame)),
		renderShimmerText(segments, frame, ws.Running),
	}, " ")
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
