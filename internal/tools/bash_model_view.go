package tools

import (
	"encoding/json"
	"fmt"
	"strings"
)

// The model-facing view of a completed bash run is plain text: an exit line,
// the bounded stdout and stderr excerpts, and only the facts that change the
// next decision (truncation, sandbox denial, timeout promotion, verification
// outcome). Audit metadata such as the classification, hashes, log sections and
// the workspace revision stays in the JSON envelope that clients and durable
// records consume. The view is rendered once at settlement and never rewritten,
// so cached request prefixes stay stable.

const bashViewOmittedMarker = "... %d lines omitted; full log: %s ..."

// renderBashModelView renders the model view for a bash run envelope. It
// declines (ok=false) for anything other than a completed run envelope so
// legacy background-action results and malformed payloads keep their text.
// When the view exceeds budgetTokens it drops middle lines of stdout, then
// stderr, and reports how many lines were omitted; it declines when even the
// facts without any output exceed the budget.
func renderBashModelView(rawText string, budgetTokens int) (string, projectionOmission, bool) {
	if !json.Valid([]byte(rawText)) {
		return "", projectionOmission{}, false
	}
	var r shellExecutionResult
	if err := json.Unmarshal([]byte(rawText), &r); err != nil || r.Action != "run" {
		return "", projectionOmission{}, false
	}
	stdoutLines := splitViewLines(r.StdoutTail)
	stderrLines := splitViewLines(r.StderrTail)
	fullLog := r.FullLogRef
	if fullLog == "" {
		fullLog = "unavailable"
	}
	build := func(keepOut, keepErr int) (string, int) {
		out, omittedOut := keepHeadTailLines(stdoutLines, keepOut, fullLog)
		errText, omittedErr := keepHeadTailLines(stderrLines, keepErr, fullLog)
		return formatBashModelView(r, out, errText), omittedOut + omittedErr
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

func splitViewLines(s string) []string {
	s = strings.TrimRight(s, "\n")
	if s == "" {
		return nil
	}
	return strings.Split(s, "\n")
}

// keepHeadTailLines keeps the first third and last two thirds of keep lines so
// both the start of a stream and its final summary survive a budget cut.
func keepHeadTailLines(lines []string, keep int, fullLog string) (string, int) {
	if keep >= len(lines) {
		return strings.Join(lines, "\n"), 0
	}
	if keep <= 0 {
		return fmt.Sprintf(bashViewOmittedMarker, len(lines), fullLog), len(lines)
	}
	head := keep / 3
	tail := keep - head
	omitted := len(lines) - keep
	parts := make([]string, 0, keep+1)
	parts = append(parts, lines[:head]...)
	parts = append(parts, fmt.Sprintf(bashViewOmittedMarker, omitted, fullLog))
	parts = append(parts, lines[len(lines)-tail:]...)
	return strings.Join(parts, "\n"), omitted
}

func formatBashModelView(r shellExecutionResult, stdout, stderr string) string {
	var b strings.Builder
	duration := formatViewDuration(r.DurationMS)
	switch {
	case r.TimedOut && r.PromotedProcessID != "":
		fmt.Fprintf(&b, "timed out after %s · still running as background process %s\n", duration, r.PromotedProcessID)
	case r.TimedOut:
		fmt.Fprintf(&b, "timed out after %s · process stopped\n", duration)
	default:
		fmt.Fprintf(&b, "exit %d · %s\n", r.ExitCode, duration)
	}
	if r.RequestedCommand != "" && r.ResolvedCommand != "" && r.RequestedCommand != r.ResolvedCommand {
		fmt.Fprintf(&b, "ran: %s\n", r.ResolvedCommand)
	}
	if stdout == "" && stderr == "" {
		b.WriteString("(no output)\n")
	}
	if stdout != "" {
		b.WriteString(stdout)
		b.WriteString("\n")
	}
	if r.StdoutTailTruncated {
		fmt.Fprintf(&b, "[stdout: %d of %d bytes shown; full log: %s]\n", len(r.StdoutTail), r.StdoutBytes, viewLogRef(r))
	}
	if stderr != "" {
		b.WriteString("--- stderr ---\n")
		b.WriteString(stderr)
		b.WriteString("\n")
	}
	if r.StderrTailTruncated {
		fmt.Fprintf(&b, "[stderr: %d of %d bytes shown; full log: %s]\n", len(r.StderrTail), r.StderrBytes, viewLogRef(r))
	}
	if r.Sandbox != nil && r.Sandbox.Denied {
		b.WriteString("sandbox: a file write outside the current workspace boundary was denied; do not retry the same access through another command. Use a registered workspace path, ask the user to add the directory as a workspace, or explain that the session must be switched to unconfined mode.\n")
	}
	if r.Verification != nil {
		writeBashVerificationView(&b, r.Verification)
	}
	if r.ExitCode != 0 || r.TimedOut || r.PromotedProcessID != "" || (r.Sandbox != nil && r.Sandbox.Denied) {
		for _, suggestion := range r.NextSuggestions {
			if strings.TrimSpace(suggestion) == "" {
				continue
			}
			fmt.Fprintf(&b, "note: %s\n", suggestion)
		}
	}
	return strings.TrimRight(b.String(), "\n")
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

func writeBashVerificationView(b *strings.Builder, v *bashVerificationResult) {
	if v.Passed && !v.FailureSummary.Failed {
		fmt.Fprintf(b, "verification: passed (scope %s)\n", v.Scope)
		return
	}
	fmt.Fprintf(b, "verification: failed (scope %s)\n", v.Scope)
	writeTestFailureSummaryContext(b, v.FailureSummary)
	if previous := intJSONNumber(v.RepeatGuard["previous_failed_runs"]); previous > 0 {
		limit := intJSONNumber(v.RepeatGuard["max_failed_runs_without_revision_change"])
		fmt.Fprintf(b, "repeat guard: %d of %d failed runs at this workspace revision; rerunning without a change will be blocked\n", previous, limit)
	}
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
