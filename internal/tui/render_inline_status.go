package tui

import (
	"fmt"
	"math"
	"strings"
	"time"

	"github.com/charmbracelet/lipgloss"
)

const statusAnimationInterval = 100 * time.Millisecond

var statusSpinnerFrames = []string{"·", "·", "·", "·"}

const (
	statusShimmerPadding       = 10
	statusShimmerBandHalfWidth = 5.0
	statusWaveAmplitude        = 0.55
	statusWaveFrequency        = 0.7
	statusWaveSpeed            = 0.85
)

type workPhase int

const (
	workPhaseIdle workPhase = iota
	workPhaseCompacting
	workPhaseThinking
	workPhaseTool
	workPhaseGenerating
	workPhaseReconnecting
	workPhaseAutoResume
	workPhaseWorker
)

type workStatus struct {
	Phase              workPhase
	Label              string
	Meta               string
	Detail             string
	Running            bool
	PersistentInlineUI bool
}

type statusTextSegment struct {
	Text   string
	Base   lipgloss.Style
	Strong lipgloss.Style
	Bright lipgloss.Style
}

func isOrchestrationTool(name string) bool {
	switch strings.TrimSpace(name) {
	case "spawn_agent", "fork_agent":
		return true
	default:
		return false
	}
}

func runningToolWorkStatus(name string) workStatus {
	name = strings.TrimSpace(name)
	if name == "" {
		name = "tool"
	}
	if isOrchestrationTool(name) {
		return workStatus{
			Phase:              workPhaseTool,
			Label:              "Spawning worker",
			Meta:               "Dispatching the background task",
			Running:            true,
			PersistentInlineUI: true,
		}
	}
	return workStatus{
		Phase:   workPhaseTool,
		Label:   fmt.Sprintf("Running %s", name),
		Meta:    "Making progress with a tool",
		Running: true,
	}
}

func deriveWorkStatus(status string) workStatus {
	switch {
	case status == "thinking":
		return workStatus{Phase: workPhaseThinking, Label: "Thinking", Meta: "Working through the next step", Running: true}
	case status == "streaming" || status == "streaming response":
		return workStatus{Phase: workPhaseGenerating, Label: "Responding", Meta: "Writing the reply", Running: true}
	case strings.HasPrefix(status, "tool:"):
		name := trimToWidth(strings.TrimSpace(strings.TrimPrefix(status, "tool:")), 36)
		return runningToolWorkStatus(name)
	case strings.HasPrefix(status, "executing tool:"):
		name := trimToWidth(strings.TrimSpace(strings.TrimPrefix(status, "executing tool:")), 36)
		return runningToolWorkStatus(name)
	case strings.HasPrefix(status, "Reconnecting"):
		label := trimToWidth(strings.TrimSpace(status), 32)
		if label == "" {
			label = "Reconnecting"
		}
		return workStatus{Phase: workPhaseReconnecting, Label: label, Meta: "Restoring the live response", Running: true}
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

// workerActivityStatus builds a status whose headline is the current
// activity phrase ("→ read_file", "thinking", etc.) while the spawn
// description rides along as the dimmed meta tail. Falls back to the
// plain "Running" variant when the activity phrase is empty.
func workerActivityStatus(activity, desc string) workStatus {
	activity = strings.TrimSpace(activity)
	if activity == "" {
		return workerRunningStatus(desc)
	}
	// Keep the label compact — the panel row is one line per worker.
	label := trimToWidth(activity, 32)
	meta := "Running in the background"
	if trimmed := strings.TrimSpace(desc); trimmed != "" {
		meta = trimToWidth(trimmed, 40)
	}
	return workStatus{Phase: workPhaseWorker, Label: label, Meta: meta, Running: true}
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
		return runningToolWorkStatus(name)
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

func italicizeStyle(style lipgloss.Style, italic bool) lipgloss.Style {
	if italic {
		return style.Italic(true)
	}
	return style
}

func statusTextSegments(ws workStatus, italic bool) []statusTextSegment {
	var segments []statusTextSegment
	label := strings.TrimSpace(ws.Label)
	if label != "" {
		segments = append(segments, statusTextSegment{
			Text:   label,
			Base:   italicizeStyle(waitingStatusLabelStyle, italic),
			Strong: italicizeStyle(waitingStatusLabelStrongStyle, italic),
			Bright: italicizeStyle(waitingStatusLabelBrightStyle, italic),
		})
	}
	if meta := strings.TrimSpace(ws.Meta); meta != "" && meta != ws.Label {
		segments = append(segments, statusTextSegment{
			Text:   " · " + trimToWidth(meta, 44),
			Base:   italicizeStyle(waitingStatusMetaStyle, italic),
			Strong: italicizeStyle(waitingStatusLabelStrongStyle, italic),
			Bright: italicizeStyle(waitingStatusLabelBrightStyle, italic),
		})
	}
	if detail := strings.TrimSpace(ws.Detail); detail != "" && detail != ws.Label && detail != ws.Meta {
		segments = append(segments, statusTextSegment{
			Text:   " · " + trimToWidth(detail, 36),
			Base:   italicizeStyle(waitingStatusMetaStyle, italic),
			Strong: italicizeStyle(waitingStatusLabelStrongStyle, italic),
			Bright: italicizeStyle(waitingStatusLabelBrightStyle, italic),
		})
	}
	return segments
}

func statusShimmerCycleLength(segments []statusTextSegment) int {
	runes := 0
	for _, segment := range segments {
		runes += len([]rune(segment.Text))
	}
	cycle := runes + statusShimmerPadding*2
	if cycle <= 0 {
		cycle = 1
	}
	return cycle
}

func renderShimmerText(segments []statusTextSegment, frame int, running bool, wave bool) string {
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

	cycle := statusShimmerCycleLength(segments)
	offset := frame % cycle
	if offset < 0 {
		offset += cycle
	}
	position := float64(offset)

	var styled strings.Builder
	runeIndex := 0
	for _, segment := range segments {
		for _, r := range []rune(segment.Text) {
			styled.WriteString(statusShimmerStyleAt(segment, runeIndex, position, wave).Render(string(r)))
			runeIndex++
		}
	}
	return styled.String()
}

func statusShimmerIntensity(idx int, position float64) float64 {
	dist := math.Abs(float64(idx+statusShimmerPadding) - position)
	if dist > statusShimmerBandHalfWidth {
		return 0
	}
	x := math.Pi * (dist / statusShimmerBandHalfWidth)
	return 0.5 * (1.0 + math.Cos(x))
}

func clampUnit(value float64) float64 {
	switch {
	case value < 0:
		return 0
	case value > 1:
		return 1
	default:
		return value
	}
}

func statusWavePhase(idx int, position float64) float64 {
	return math.Sin(float64(idx)*statusWaveFrequency - position*statusWaveSpeed)
}

func statusShimmerStyleAt(segment statusTextSegment, idx int, position float64, wave bool) lipgloss.Style {
	intensity := statusShimmerIntensity(idx, position)
	phase := 0.0
	if wave && intensity > 0 {
		phase = statusWavePhase(idx, position)
		ripple := phase * statusWaveAmplitude
		intensity = clampUnit(intensity + ripple*intensity)
	}

	var style lipgloss.Style
	switch {
	case intensity >= 0.72:
		style = segment.Bright
	case intensity >= 0.18:
		style = segment.Strong
	default:
		style = segment.Base
	}

	if wave && intensity >= 0.2 {
		switch {
		case phase >= 0.3:
			style = style.Underline(true)
		case phase <= -0.35:
			style = style.Faint(true)
		}
	}
	return style
}

func renderStatusHeader(ws workStatus, frame int) string {
	return renderStatusHeaderWithOptions(ws, frame, false, false)
}

func renderStatusHeaderWithOptions(ws workStatus, frame int, italic bool, wave bool) string {
	if ws.Phase == workPhaseIdle {
		return ""
	}
	segments := statusTextSegments(ws, italic)
	if len(segments) == 0 {
		return waitingStatusPrefixStyle.Render(statusGlyph(ws, frame))
	}
	return strings.Join([]string{
		waitingStatusPrefixStyle.Render(statusGlyph(ws, frame)),
		renderShimmerText(segments, frame, ws.Running, wave),
	}, " ")
}

func renderInlineWorkStatus(ws workStatus, frame int, width int) string {
	if ws.Phase == workPhaseIdle {
		return ""
	}
	line := renderStatusHeaderWithOptions(ws, frame, true, true)
	if width <= 0 {
		return line
	}
	return trimToWidth(line, width)
}

func renderInlineStatus(status string, frame int, width int) string {
	return renderInlineWorkStatus(deriveWorkStatus(status), frame, width)
}
