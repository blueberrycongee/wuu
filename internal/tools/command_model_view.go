package tools

import (
	"encoding/json"
	"fmt"
	"regexp"
	"strings"

	proc "github.com/blueberrycongee/wuu/internal/process"
)

// Command results reach the model as plain text shaped like a terminal: the
// output itself, followed by only the facts that change the next step (a
// non-zero exit code, a timeout hand-off, a sandbox denial, where the full log
// is). Audit metadata such as the classification, hashes, log sections and the
// workspace revision stays in the JSON envelope that clients and durable
// records consume. A view is rendered once at settlement and never rewritten,
// so cached request prefixes stay stable.

// renderBashModelView renders the model view for a bash envelope: a completed
// foreground run or a background start. It declines (ok=false) for anything
// else so malformed payloads keep their text. When the view exceeds
// budgetTokens it drops middle lines of stdout, then stderr; it declines when
// even the status lines exceed the budget.
func renderBashModelView(rawText string, budgetTokens int) (string, projectionOmission, bool) {
	if !json.Valid([]byte(rawText)) {
		return "", projectionOmission{}, false
	}
	var head struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(rawText), &head); err != nil {
		return "", projectionOmission{}, false
	}
	switch head.Action {
	case "run":
	case bashResultActionStart:
		var started proc.Process
		if err := json.Unmarshal([]byte(rawText), &started); err != nil || started.ID == "" {
			return "", projectionOmission{}, false
		}
		return fitView(fmt.Sprintf("Running in background as process %s. You will be notified when it exits; use process to read its output or stop it.", started.ID), budgetTokens)
	default:
		return "", projectionOmission{}, false
	}
	var r shellExecutionResult
	if err := json.Unmarshal([]byte(rawText), &r); err != nil {
		return "", projectionOmission{}, false
	}
	stdoutLines := viewLines(r.StdoutTail)
	stderrLines := viewLines(r.StderrTail)
	status := bashStatusLines(r)
	marker := omittedLinesMarker("full log: " + viewLogRef(r))
	build := func(keepOut, keepErr int) (string, int) {
		out, omittedOut := keepHeadTailLines(stdoutLines, keepOut, marker)
		errText, omittedErr := keepHeadTailLines(stderrLines, keepErr, marker)
		return joinViewParts(out, errText, status, r.ExitCode == 0 && !r.TimedOut), omittedOut + omittedErr
	}
	text, _ := build(len(stdoutLines), len(stderrLines))
	if budgetTokens <= 0 || estimateResultTokens(text) <= budgetTokens {
		return text, projectionOmission{}, true
	}
	size := func(keepOut, keepErr int) int {
		candidate, _ := build(keepOut, keepErr)
		return estimateResultTokens(candidate)
	}
	keepOut := largestFitting(len(stdoutLines), budgetTokens, func(k int) int { return size(k, len(stderrLines)) })
	keepErr := len(stderrLines)
	if size(keepOut, keepErr) > budgetTokens {
		keepOut = 0
		keepErr = largestFitting(len(stderrLines), budgetTokens, func(k int) int { return size(0, k) })
	}
	text, omitted := build(keepOut, keepErr)
	if estimateResultTokens(text) > budgetTokens {
		return "", projectionOmission{}, false
	}
	return text, projectionOmission{Lines: omitted}, true
}

// bashStatusLines lists what the output alone does not show, in the order a
// reader needs it: how the command was rewritten, where the rest of the
// output is, how it ended, and what the harness did about it.
func bashStatusLines(r shellExecutionResult) []string {
	var lines []string
	if r.RequestedCommand != "" && r.ResolvedCommand != "" && r.RequestedCommand != r.ResolvedCommand {
		lines = append(lines, "(ran as: "+r.ResolvedCommand+")")
	}
	if r.StdoutTailTruncated || r.StderrTailTruncated {
		lines = append(lines, "[full log: "+viewLogRef(r)+"]")
	}
	switch {
	case r.TimedOut && r.PromotedProcessID != "":
		lines = append(lines, fmt.Sprintf("Timed out after %s; still running in the background as process %s. You will be notified when it exits; do not rerun the command.", formatViewDuration(r.DurationMS), r.PromotedProcessID))
	case r.TimedOut:
		lines = append(lines, fmt.Sprintf("Timed out after %s; the command was stopped. Rerun a long-lived command with run_in_background.", formatViewDuration(r.DurationMS)))
	case r.ExitCode != 0:
		lines = append(lines, fmt.Sprintf("Exit code %d", r.ExitCode))
	}
	if r.Sandbox != nil && r.Sandbox.Denied {
		lines = append(lines, "The sandbox blocked a write outside the workspace boundary; do not retry it through another command. Use a workspace path, ask the user to add the directory as a workspace, or explain that the session must be switched to unconfined mode.")
	}
	if v := r.Verification; v != nil && (!v.Passed || v.FailureSummary.Failed) {
		// The failure summary is extracted from the output, so it only adds
		// information when part of that output was cut.
		if r.StdoutTailTruncated || r.StderrTailTruncated {
			var b strings.Builder
			writeTestFailureSummaryContext(&b, v.FailureSummary)
			if summary := strings.TrimRight(b.String(), "\n"); summary != "" {
				lines = append(lines, summary)
			}
		}
		failures := intJSONNumber(v.RepeatGuard["previous_failed_runs"]) + 1
		limit := intJSONNumber(v.RepeatGuard["max_failed_runs_without_revision_change"])
		if failures >= limit && limit > 0 {
			lines = append(lines, fmt.Sprintf("This check has failed %d times at the same workspace revision; rerunning it is blocked until files change.", failures))
		}
	}
	return lines
}

// renderProcessModelView renders the model view for a process tool envelope.
func renderProcessModelView(rawText string, budgetTokens int) (string, projectionOmission, bool) {
	if !json.Valid([]byte(rawText)) {
		return "", projectionOmission{}, false
	}
	var head struct {
		Action string `json:"action"`
	}
	if err := json.Unmarshal([]byte(rawText), &head); err != nil {
		return "", projectionOmission{}, false
	}
	switch head.Action {
	case processActionRead:
		return renderProcessRead(rawText, budgetTokens)
	case processActionList:
		var r struct {
			Processes []proc.Process `json:"processes"`
		}
		if err := json.Unmarshal([]byte(rawText), &r); err != nil {
			return "", projectionOmission{}, false
		}
		if len(r.Processes) == 0 {
			return fitView("No background processes.", budgetTokens)
		}
		lines := make([]string, 0, len(r.Processes))
		for _, p := range r.Processes {
			line := fmt.Sprintf("%s %s · %s", p.ID, processStateText(p), p.Command)
			if p.CompletionMode == proc.CompletionModeDetached {
				line += " (detached)"
			}
			lines = append(lines, line)
		}
		return fitView(strings.Join(lines, "\n"), budgetTokens)
	case processActionWrite:
		var r struct {
			ProcessID    string `json:"process_id"`
			BytesWritten int    `json:"bytes_written"`
		}
		if err := json.Unmarshal([]byte(rawText), &r); err != nil {
			return "", projectionOmission{}, false
		}
		return fitView(fmt.Sprintf("Sent %d bytes to process %s.", r.BytesWritten, r.ProcessID), budgetTokens)
	case processActionStop:
		var p proc.Process
		if err := json.Unmarshal([]byte(rawText), &p); err != nil {
			return "", projectionOmission{}, false
		}
		return fitView(fmt.Sprintf("Process %s %s.", p.ID, processStateText(p)), budgetTokens)
	case processActionUpdate:
		var r struct {
			ProcessID      string              `json:"process_id"`
			CompletionMode proc.CompletionMode `json:"completion_mode"`
			RecheckMinutes int                 `json:"recheck_minutes"`
		}
		if err := json.Unmarshal([]byte(rawText), &r); err != nil {
			return "", projectionOmission{}, false
		}
		exit := "its exit starts a new turn"
		if r.CompletionMode == proc.CompletionModeDetached {
			exit = "its exit does not resume this task"
		}
		recheck := "no progress checks"
		if r.RecheckMinutes > 0 {
			recheck = fmt.Sprintf("progress check every %d min", r.RecheckMinutes)
		}
		return fitView(fmt.Sprintf("Process %s: %s; %s.", r.ProcessID, exit, recheck), budgetTokens)
	default:
		return "", projectionOmission{}, false
	}
}

func renderProcessRead(rawText string, budgetTokens int) (string, projectionOmission, bool) {
	var r struct {
		ProcessID       string       `json:"process_id"`
		Output          string       `json:"output"`
		StartOffset     int64        `json:"start_offset"`
		EndOffset       int64        `json:"end_offset"`
		TotalBytes      int64        `json:"total_bytes"`
		Process         proc.Process `json:"process"`
		NextSuggestions []string     `json:"next_suggestions"`
	}
	if err := json.Unmarshal([]byte(rawText), &r); err != nil {
		return "", projectionOmission{}, false
	}
	header := fmt.Sprintf("Process %s %s.", r.ProcessID, processStateText(r.Process))
	var footer []string
	if live := processIsLive(r.Process.Status); live || r.StartOffset > 0 || r.EndOffset < r.TotalBytes {
		footer = append(footer, fmt.Sprintf("[output bytes %d-%d of %d; continue with offset_bytes=%d]", r.StartOffset, r.EndOffset, r.TotalBytes, r.EndOffset))
	}
	footer = append(footer, r.NextSuggestions...)
	lines := viewLines(r.Output)
	marker := omittedLinesMarker("page with offset_bytes")
	build := func(keep int) (string, int) {
		out, omitted := keepHeadTailLines(lines, keep, marker)
		if out == "" {
			out = "(no new output)"
		}
		parts := append([]string{header, out}, footer...)
		return strings.Join(parts, "\n"), omitted
	}
	text, _ := build(len(lines))
	if budgetTokens <= 0 || estimateResultTokens(text) <= budgetTokens {
		return text, projectionOmission{}, true
	}
	keep := largestFitting(len(lines), budgetTokens, func(k int) int {
		candidate, _ := build(k)
		return estimateResultTokens(candidate)
	})
	text, omitted := build(keep)
	if estimateResultTokens(text) > budgetTokens {
		return "", projectionOmission{}, false
	}
	return text, projectionOmission{Lines: omitted}, true
}

func processIsLive(status proc.Status) bool {
	return status == proc.StatusStarting || status == proc.StatusRunning || status == proc.StatusStopping
}

func processStateText(p proc.Process) string {
	switch {
	case processIsLive(p.Status):
		return string(p.Status)
	case p.TerminalCause == proc.EventCauseRequestedStop:
		return "was stopped"
	default:
		return fmt.Sprintf("exited with code %d", p.ExitCode)
	}
}

func fitView(text string, budgetTokens int) (string, projectionOmission, bool) {
	if budgetTokens > 0 && estimateResultTokens(text) > budgetTokens {
		return "", projectionOmission{}, false
	}
	return text, projectionOmission{}, true
}

func joinViewParts(stdout, stderr string, status []string, succeeded bool) string {
	parts := make([]string, 0, 2+len(status))
	for _, part := range []string{stdout, stderr} {
		if part != "" {
			parts = append(parts, part)
		}
	}
	if len(parts) == 0 && succeeded && len(status) == 0 {
		return "(no output)"
	}
	parts = append(parts, status...)
	return strings.Join(parts, "\n")
}

// viewLines normalizes one output stream for the model: terminal control
// codes and carriage-return redraws are removed, surrounding blank lines are
// trimmed, and the rest is split into lines.
func viewLines(s string) []string {
	s = StripTerminalControls(s)
	s = strings.TrimRight(s, " \t\n")
	s = strings.TrimLeft(s, "\n")
	if strings.TrimSpace(s) == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

func omittedLinesMarker(recovery string) func(int) string {
	return func(n int) string { return fmt.Sprintf("... %d lines omitted; %s ...", n, recovery) }
}

// keepHeadTailLines keeps the first third and last two thirds of keep lines so
// both the start of a stream and its final summary survive a budget cut.
func keepHeadTailLines(lines []string, keep int, marker func(int) string) (string, int) {
	if keep >= len(lines) {
		return strings.Join(lines, "\n"), 0
	}
	if keep <= 0 {
		return marker(len(lines)), len(lines)
	}
	head := keep / 3
	tail := keep - head
	omitted := len(lines) - keep
	parts := make([]string, 0, keep+1)
	parts = append(parts, lines[:head]...)
	parts = append(parts, marker(omitted))
	parts = append(parts, lines[len(lines)-tail:]...)
	return strings.Join(parts, "\n"), omitted
}

var terminalControlPattern = regexp.MustCompile(`\x1b\[[0-?]*[ -/]*[@-~]|\x1b\][^\x07\x1b]*(?:\x07|\x1b\\)|\x1b[()*+][0-9A-Za-z]|\x1b[@-Z\\-_]`)

// StripTerminalControls removes ANSI escape sequences and carriage-return
// redraws from terminal output so only the text a person would finally read
// remains. Background processes run in a pseudo-terminal, so their logs carry
// colors and progress-bar redraws that cost tokens without adding meaning.
func StripTerminalControls(s string) string {
	if !strings.ContainsAny(s, "\x1b\r") {
		return s
	}
	s = terminalControlPattern.ReplaceAllString(s, "")
	s = strings.ReplaceAll(s, "\r\n", "\n")
	if !strings.Contains(s, "\r") {
		return s
	}
	lines := strings.Split(s, "\n")
	for i, line := range lines {
		line = strings.TrimRight(line, "\r")
		if idx := strings.LastIndex(line, "\r"); idx >= 0 {
			line = line[idx+1:]
		}
		lines[i] = line
	}
	return strings.Join(lines, "\n")
}

func viewLogRef(r shellExecutionResult) string {
	if r.FullLogRef != "" {
		return r.FullLogRef
	}
	if r.FullLogError != "" {
		return "unavailable (" + r.FullLogError + ")"
	}
	return "unavailable"
}

func formatViewDuration(ms int64) string {
	switch {
	case ms < 1000:
		return fmt.Sprintf("%dms", ms)
	case ms < 60_000:
		return fmt.Sprintf("%.1fs", float64(ms)/1000)
	default:
		return fmt.Sprintf("%dm%02ds", ms/60_000, (ms%60_000)/1000)
	}
}
