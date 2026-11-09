package tools

import (
	"fmt"

	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func (record ToolExecutionRecord) ResultEnvelope() toolresult.Envelope {
	envelope := toolresult.Envelope{
		OK:         record.Success,
		ToolCallID: record.CallID,
		Revision:   record.RevisionAfter,
		Summary:    toolResultSummary(record),
		Data: map[string]any{
			"name":                  record.Name,
			"kind":                  string(record.Kind),
			"exposure":              string(record.Exposure),
			"risk":                  string(record.Risk),
			"arguments_sha256":      record.ArgumentsSHA256,
			"result_action":         record.ResultAction,
			"classification_reason": record.ClassificationReason,
			"policy_action":         string(record.PolicyAction),
			"policy_reason":         record.PolicyReason,
			"read_only":             record.ReadOnly,
			"concurrency_safe":      record.ConcurrencySafe,
			"duration_ms":           record.DurationMS,
			"revision_before":       record.RevisionBefore,
			"revision_after":        record.RevisionAfter,
			"raw_output_bytes":      record.RawOutputBytes,
			"returned_output_bytes": record.ReturnedOutputBytes,
			"result_budgeted":       record.ResultBudgeted,
			"tool_surface_changed":  record.ToolSurfaceChanged,
			"error_present":         record.Error != "",
			"error_kind":            record.ErrorKind,
		},
		DataRef:   record.ResultRef,
		Truncated: record.ResultBudgeted && record.ResultRef == "",
	}
	if record.ResultBudgeted && record.ResultRef != "" {
		envelope.Warnings = append(envelope.Warnings, "tool output was persisted outside the model context")
	}
	if record.ResultBudgeted && record.ResultRef == "" {
		envelope.Warnings = append(envelope.Warnings, "tool output was truncated because no result artifact was available")
	}
	if len(record.ArtifactRefs) > 0 {
		envelope.Data["artifact_refs"] = append([]string(nil), record.ArtifactRefs...)
	}
	if len(record.LoadedDeferredTools) > 0 {
		envelope.Data["loaded_deferred_tools"] = append([]string(nil), record.LoadedDeferredTools...)
	}
	if len(record.NewlyLoadedDeferredTools) > 0 {
		envelope.Data["newly_loaded_deferred_tools"] = append([]string(nil), record.NewlyLoadedDeferredTools...)
	}
	if len(record.AlreadyLoadedDeferredTools) > 0 {
		envelope.Data["already_loaded_deferred_tools"] = append([]string(nil), record.AlreadyLoadedDeferredTools...)
	}
	if record.PatchRiskSummary != nil {
		envelope.Data["patch_risk_summary"] = record.PatchRiskSummary
	}
	envelope.NextSuggestions = toolResultNextSuggestions(record)
	return envelope
}

func toolResultSummary(record ToolExecutionRecord) string {
	status := "succeeded"
	if !record.Success {
		status = "failed"
	}
	if record.DurationMS > 0 {
		return fmt.Sprintf("%s %s in %dms", record.Name, status, record.DurationMS)
	}
	return fmt.Sprintf("%s %s", record.Name, status)
}

func toolResultNextSuggestions(record ToolExecutionRecord) []string {
	if !record.Success {
		if record.ErrorKind == "repeated_tool_input" {
			return []string{"use prior observations instead of repeating the same tool input, or change the input/workspace before retrying"}
		}
		switch record.PolicyAction {
		case ToolPolicyDeny:
			return []string{"explain the workspace boundary denial or use a path/action inside the allowed boundary"}
		}
		return []string{"inspect the redacted error summary and retry with corrected inputs or a safer tool"}
	}
	if record.ResultBudgeted && record.ResultRef != "" {
		return []string{"read the persisted result artifact if the compact preview is insufficient"}
	}
	if record.ReadOnly {
		return []string{"use the returned observation as evidence for the next action"}
	}
	return []string{"inspect the resulting diff or run the relevant validation before finishing"}
}
