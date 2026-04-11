package tui

import (
	"fmt"
	"strings"
	"time"
)

const statusAnimationInterval = 150 * time.Millisecond

var statusSpinnerFrames = []string{"◐", "◓", "◑", "◒"}

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
	Phase workPhase
	Label string
	Meta  string
}

func deriveWorkStatus(status string) workStatus {
	switch {
	case status == "thinking":
		return workStatus{Phase: workPhaseThinking, Label: "Thinking", Meta: "Working through the next step"}
	case status == "streaming" || status == "streaming response":
		return workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply"}
	case strings.HasPrefix(status, "tool:"):
		name := trimToWidth(strings.TrimSpace(strings.TrimPrefix(status, "tool:")), 36)
		if name == "" {
			name = "tool"
		}
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Running %s", name), Meta: "Making progress with a tool"}
	case strings.HasPrefix(status, "executing tool:"):
		name := trimToWidth(strings.TrimSpace(strings.TrimPrefix(status, "executing tool:")), 36)
		if name == "" {
			name = "tool"
		}
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Running %s", name), Meta: "Making progress with a tool"}
	case strings.HasPrefix(status, "Reconnecting"):
		return workStatus{Phase: workPhaseReconnecting, Label: "Reconnecting", Meta: "Restoring the live response"}
	case strings.HasPrefix(status, "auto-resume"):
		return workStatus{Phase: workPhaseAutoResume, Label: "Continuing", Meta: "Picking up after worker updates"}
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
	return workStatus{Phase: workPhaseWorker, Label: "Running", Meta: meta}
}

func thinkingBlockStatus(done bool, duration time.Duration) workStatus {
	if done {
		return workStatus{
			Phase: workPhaseThinking,
			Label: "Thinking complete",
			Meta:  fmt.Sprintf("Finished in %.1fs", duration.Seconds()),
		}
	}
	return workStatus{
		Phase: workPhaseThinking,
		Label: "Thinking",
		Meta:  fmt.Sprintf("Elapsed %.1fs", duration.Seconds()),
	}
}

func toolCallStatus(tc ToolCallEntry) workStatus {
	name := strings.TrimSpace(tc.Name)
	if name == "" {
		name = "tool"
	}
	switch tc.Status {
	case ToolCallRunning:
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Running %s", name), Meta: "Making progress with a tool"}
	case ToolCallError:
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("%s failed", name), Meta: "Tool run failed"}
	default:
		return workStatus{Phase: workPhaseTool, Label: fmt.Sprintf("Finished %s", name), Meta: "Tool run complete"}
	}
}

func isWaitingStatus(status string) bool {
	return deriveWorkStatus(status).Phase != workPhaseIdle
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
	switch ws.Phase {
	case workPhaseTool, workPhaseThinking, workPhaseGenerating, workPhaseReconnecting, workPhaseAutoResume, workPhaseWorker:
		return statusSpinner(frame)
	default:
		return "•"
	}
}

func renderStatusHeader(ws workStatus, frame int) string {
	if ws.Phase == workPhaseIdle {
		return ""
	}
	parts := []string{
		waitingStatusPrefixStyle.Render(statusGlyph(ws, frame)),
		waitingStatusLabelStyle.Render(ws.Label),
	}
	if meta := strings.TrimSpace(ws.Meta); meta != "" && meta != ws.Label {
		parts = append(parts, waitingStatusMetaStyle.Render("· "+trimToWidth(meta, 44)))
	}
	return strings.Join(parts, " ")
}

func renderInlineStatus(status string, frame int) string {
	return renderStatusHeader(deriveWorkStatus(status), frame)
}
