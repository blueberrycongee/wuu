package agentcontrol

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/harness"
)

type ReportEvidence struct {
	Type    string `json:"type,omitempty"`
	Path    string `json:"path,omitempty"`
	Line    int    `json:"line,omitempty"`
	Command string `json:"command,omitempty"`
	Output  string `json:"output,omitempty"`
	Note    string `json:"note,omitempty"`
}

type AgentReportRequest struct {
	Outcome      string           `json:"outcome"`
	Summary      string           `json:"summary"`
	ChangedFiles []string         `json:"changed_files,omitempty"`
	WorkDone     []string         `json:"work_done,omitempty"`
	Blockers     []string         `json:"blockers,omitempty"`
	Risks        []string         `json:"risks,omitempty"`
	Verification []string         `json:"verification,omitempty"`
	NextSteps    []string         `json:"next_steps,omitempty"`
	Evidence     []ReportEvidence `json:"evidence,omitempty"`
	Artifacts    []string         `json:"artifacts,omitempty"`
}

type AgentReportResult struct {
	Action     string   `json:"action"`
	TaskID     string   `json:"task_id"`
	AgentID    string   `json:"agent_id"`
	AgentPath  string   `json:"agent_path,omitempty"`
	Outcome    string   `json:"outcome"`
	ReportPath string   `json:"report_path,omitempty"`
	Artifacts  []string `json:"artifacts,omitempty"`
}

type AgentReportDetails struct {
	Outcome      string           `json:"outcome,omitempty"`
	Summary      string           `json:"summary,omitempty"`
	ChangedFiles []string         `json:"changed_files,omitempty"`
	WorkDone     []string         `json:"work_done,omitempty"`
	Blockers     []string         `json:"blockers,omitempty"`
	Risks        []string         `json:"risks,omitempty"`
	Verification []string         `json:"verification,omitempty"`
	NextSteps    []string         `json:"next_steps,omitempty"`
	Evidence     []ReportEvidence `json:"evidence,omitempty"`
	Artifacts    []string         `json:"artifacts,omitempty"`
	ReportPath   string           `json:"report_path,omitempty"`
}

func (c *AgentControl) AgentReportDetailsForTask(taskID string) (AgentReportDetails, bool) {
	report, ok := c.harnessReportDetailsForTask(taskID)
	if !ok {
		return AgentReportDetails{}, false
	}
	evidence := make([]ReportEvidence, 0, len(report.Evidence))
	for _, ref := range report.Evidence {
		evidence = append(evidence, ReportEvidence{
			Type:    ref.Type,
			Path:    ref.Path,
			Line:    ref.Line,
			Command: ref.Command,
			Output:  ref.Output,
			Note:    ref.Note,
		})
	}
	return AgentReportDetails{
		Outcome:      report.Outcome,
		Summary:      report.Summary,
		ChangedFiles: trimStringSlice(report.ChangedFiles),
		WorkDone:     trimStringSlice(report.WorkDone),
		Blockers:     trimStringSlice(report.Blockers),
		Risks:        trimStringSlice(report.Risks),
		Verification: trimStringSlice(report.Verification),
		NextSteps:    trimStringSlice(report.NextSteps),
		Evidence:     evidence,
		Artifacts:    c.sessionArtifactRefs(report.Artifacts),
		ReportPath:   c.sessionArtifactRef(report.ReportPath),
	}, true
}

func (c *AgentControl) RecordAgentReport(agentID, agentPath string, req AgentReportRequest) (AgentReportResult, error) {
	if c == nil || c.harnessStore == nil {
		return AgentReportResult{}, errors.New("agent_report: harness store not configured")
	}
	id := strings.TrimSpace(agentID)
	if id == "" {
		id = c.agentIDForPath(agentPath)
	}
	if id == "" || id == c.sessionID || id == c.rootThreadID {
		return AgentReportResult{}, errors.New("agent_report is only available to child agents")
	}
	if c.manager.Get(id) == nil {
		return AgentReportResult{}, fmt.Errorf("agent_report: agent %q not found", id)
	}
	outcome, err := normalizeReportOutcome(req.Outcome)
	if err != nil {
		return AgentReportResult{}, err
	}
	summary := strings.TrimSpace(req.Summary)
	if summary == "" {
		return AgentReportResult{}, errors.New("agent_report: summary is required")
	}
	path := strings.TrimSpace(agentPath)
	if path == "" {
		if meta, ok := c.threads.Resolve(id); ok {
			path = meta.Path
		}
	}
	evidence := make([]harness.EvidenceRef, 0, len(req.Evidence))
	for _, ref := range req.Evidence {
		evidence = append(evidence, harness.EvidenceRef{
			Type:    strings.TrimSpace(ref.Type),
			Path:    strings.TrimSpace(ref.Path),
			Line:    ref.Line,
			Command: strings.TrimSpace(ref.Command),
			Output:  strings.TrimSpace(ref.Output),
			Note:    strings.TrimSpace(ref.Note),
		})
	}
	artifacts, err := c.importReportedArtifacts(id, req.Artifacts)
	if err != nil {
		return AgentReportResult{}, err
	}
	report, err := c.harnessStore.SubmitReport(harness.Report{
		ID:           id + "-agent-report",
		TaskID:       id,
		RunID:        harnessRunID(id),
		AgentID:      id,
		AgentPath:    path,
		Kind:         harness.ReportKindStructured,
		Outcome:      outcome,
		Summary:      summary,
		ChangedFiles: trimStringSlice(req.ChangedFiles),
		WorkDone:     trimStringSlice(req.WorkDone),
		Blockers:     trimStringSlice(req.Blockers),
		Risks:        trimStringSlice(req.Risks),
		Verification: trimStringSlice(req.Verification),
		NextSteps:    trimStringSlice(req.NextSteps),
		Evidence:     evidence,
		Artifacts:    artifacts,
		SubmittedAt:  time.Now().UTC(),
	})
	if err != nil {
		return AgentReportResult{}, err
	}
	// The report is durable metadata on the run, not a lifecycle verdict. The
	// self-reported outcome is archived below as the agent's claim, but it does
	// not adjudicate the task status; runtime facts own the lifecycle.
	if err := c.recordAgentReport(AgentReport{
		Source:       "harness_report",
		TaskID:       id,
		RunID:        report.RunID,
		AgentID:      id,
		AgentPath:    path,
		Outcome:      outcome,
		Summary:      report.Summary,
		ReportPath:   report.ReportPath,
		Blockers:     report.Blockers,
		ChangedFiles: report.ChangedFiles,
		Verification: report.Verification,
		Artifacts:    report.Artifacts,
		NextSteps:    report.NextSteps,
		CreatedAt:    report.SubmittedAt,
	}); err != nil {
		return AgentReportResult{}, err
	}
	if agentReportNeedsFailureSync(outcome, report.Blockers) {
		if err := c.recordAgentFailure(AgentFailure{
			Source:       "harness_report",
			TaskID:       id,
			RunID:        report.RunID,
			AgentID:      id,
			AgentPath:    path,
			Outcome:      outcome,
			Message:      strings.Join(trimStringSlice(report.Blockers), "; "),
			ReportPath:   report.ReportPath,
			Blockers:     report.Blockers,
			ChangedFiles: report.ChangedFiles,
			Verification: report.Verification,
			CreatedAt:    report.SubmittedAt,
		}); err != nil {
			return AgentReportResult{}, err
		}
	}
	if report.ReportPath != "" && !stringSliceContains(artifacts, report.ReportPath) {
		artifacts = append(artifacts, report.ReportPath)
	}
	return AgentReportResult{
		Action:     "agent_report",
		TaskID:     id,
		AgentID:    id,
		AgentPath:  path,
		Outcome:    outcome,
		ReportPath: c.sessionArtifactRef(report.ReportPath),
		Artifacts:  c.sessionArtifactRefs(artifacts),
	}, nil
}

// harnessStatusFromReportOutcome classifies a self-reported outcome for
// bookkeeping only (e.g. whether a report should sync a failure record). It
// no longer drives task or run status: the lifecycle is owned by runtime facts.
func harnessStatusFromReportOutcome(outcome string) harness.TaskStatus {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "completed":
		return harness.TaskStatusCompleted
	case "cancelled":
		return harness.TaskStatusCancelled
	case "stuck", "error":
		return harness.TaskStatusFailed
	default:
		return ""
	}
}

func isTerminalHarnessStatus(status harness.TaskStatus) bool {
	switch status {
	case harness.TaskStatusCompleted, harness.TaskStatusFailed, harness.TaskStatusCancelled, harness.TaskStatusInterrupted:
		return true
	default:
		return false
	}
}

func isActiveHarnessStatus(status harness.TaskStatus) bool {
	switch status {
	case harness.TaskStatusPending, harness.TaskStatusQueued, harness.TaskStatusRunning:
		return true
	default:
		return false
	}
}

func normalizeReportOutcome(outcome string) (string, error) {
	switch strings.ToLower(strings.TrimSpace(outcome)) {
	case "completed", "complete":
		return "completed", nil
	case "stuck":
		return "stuck", nil
	case "error", "failed":
		return "error", nil
	case "cancelled", "canceled":
		return "cancelled", nil
	default:
		return "", errors.New("agent_report: outcome must be completed, stuck, error, or cancelled")
	}
}

func trimStringSlice(values []string) []string {
	out := make([]string, 0, len(values))
	for _, value := range values {
		if trimmed := strings.TrimSpace(value); trimmed != "" {
			out = append(out, trimmed)
		}
	}
	return out
}

func sanitizeArtifactID(path string) string {
	var b strings.Builder
	for _, r := range strings.TrimSpace(path) {
		switch {
		case r >= 'a' && r <= 'z':
			b.WriteRune(r)
		case r >= 'A' && r <= 'Z':
			b.WriteRune(r + ('a' - 'A'))
		case r >= '0' && r <= '9':
			b.WriteRune(r)
		default:
			b.WriteByte('_')
		}
	}
	id := strings.Trim(b.String(), "_")
	if id == "" {
		return "artifact"
	}
	return id
}
