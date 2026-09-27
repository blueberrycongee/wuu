package tools

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// A narrow subset of bash run arguments keeps fusion an ordered action, rather
// than another workflow language or process-control interface.
type applyPatchThenRun struct {
	Command        string `json:"command"`
	CWD            string `json:"cwd,omitempty"`
	TimeoutSeconds *int   `json:"timeout_seconds,omitempty"`
	Purpose        string `json:"purpose,omitempty"`
	Scope          string `json:"scope,omitempty"`
}

func applyPatchThenRunSchema(env *Env) map[string]any {
	bashProperties := NewBashTool(env).Definition().InputSchema["properties"].(map[string]any)
	properties := make(map[string]any)
	for _, name := range []string{"command", "cwd", "timeout_seconds", "purpose", "scope"} {
		properties[name] = bashProperties[name]
	}
	return map[string]any{
		"type": "object", "additionalProperties": false,
		"description": "Optional non-interactive bash run after the entire patch succeeds. Uses bash permissions, logging and timeout-to-background behavior. Cannot be combined with dry_run. Command failure does not roll back the patch.",
		"properties":  properties, "required": []string{"command"},
	}
}

func (args applyPatchArgs) followUp() (*applyPatchThenRun, error) {
	raw := bytes.TrimSpace(args.ThenRun)
	if len(raw) == 0 || bytes.Equal(raw, []byte("null")) {
		return nil, nil
	}
	var next applyPatchThenRun
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&next); err != nil {
		return nil, fmt.Errorf("invalid then_run: %w", err)
	}
	if args.DryRun || args.DryRun2 {
		return nil, errors.New("then_run cannot be combined with dry_run")
	}
	if strings.TrimSpace(next.Command) == "" {
		return nil, errors.New("then_run requires command")
	}
	if next.TimeoutSeconds != nil && (*next.TimeoutSeconds < 1 || *next.TimeoutSeconds > maxShellTimeoutSeconds) {
		return nil, fmt.Errorf("then_run timeout_seconds must be between 1 and %d", maxShellTimeoutSeconds)
	}
	switch next.Scope {
	case "", "targeted", "affected", "full":
	default:
		return nil, errors.New("then_run scope must be targeted, affected, or full")
	}
	return &next, nil
}

func (*ApplyPatchTool) IsOrchestrator(argsJSON string) bool {
	var args applyPatchArgs
	if decodeArgs(argsJSON, &args) != nil {
		return false
	}
	next, err := args.followUp()
	return err == nil && next != nil
}

func (t *ApplyPatchTool) executeFusedPatch(ctx context.Context, args applyPatchArgs, next applyPatchThenRun) (toolresult.Result, error) {
	executor, ok := toolctx.Nested(ctx)
	if !ok {
		return toolresult.Result{}, errors.New("then_run requires the agent tool execution runtime")
	}
	// Both leaves retain their normal policy, scheduling, hooks and durable
	// records. Only the parent result is projected into provider history.
	args.ThenRun = nil
	patchArguments, err := json.Marshal(args)
	if err != nil {
		return toolresult.Result{}, err
	}
	commandArguments, err := json.Marshal(next)
	if err != nil {
		return toolresult.Result{}, err
	}
	patch, err := executor.Invoke(ctx, providers.ToolCall{ID: "patch", Name: t.Name(), Arguments: string(patchArguments)})
	if err != nil {
		return toolresult.FromErrorText(fmt.Sprintf("Patch outcome unavailable: %v. then_run was not started. Recover the tool records and inspect files before retrying the patch.", err)), nil
	}
	if patch.IsError {
		patch.ModelText = nil
		patch.Content = append(patch.Content, toolresult.ContentPart{Type: toolresult.ContentTypeText, Text: "[then_run:skipped] The patch failed; the command was not run."})
		return patch, nil
	}
	command, err := executor.Invoke(ctx, providers.ToolCall{ID: "then_run", Name: "bash", Arguments: string(commandArguments)})
	if err != nil {
		command = toolresult.FromErrorText(fmt.Sprintf("Follow-up outcome unavailable: %v. Inspect process and tool records before retrying the command.", err))
	}
	status := fusedCommandStatus(command)
	// The patch child is already settled. This parent adds command evidence,
	// so its model view must be settled again from the combined content, not
	// inherited from the child; otherwise the model never sees the command.
	patch.ModelText = nil
	patch.Content = append(patch.Content, toolresult.ContentPart{
		Type: toolresult.ContentTypeText,
		Text: fmt.Sprintf("[then_run:%s] Patch applied and retained.\n%s", status, command.TextProjection()),
	})
	patch.IsError = status == "failed"
	// Retain the command's full settled result alongside the existing file
	// details, including log continuations, verification and process handles.
	var details map[string]json.RawMessage
	if err := json.Unmarshal(patch.StructuredContent, &details); err != nil || details == nil {
		details = make(map[string]json.RawMessage)
	}
	details["then_run"], _ = json.Marshal(struct {
		Status string            `json:"status"`
		Result toolresult.Result `json:"result"`
	}{status, command})
	patch.StructuredContent, _ = json.Marshal(details)
	return patch, nil
}

func fusedCommandStatus(result toolresult.Result) string {
	if result.IsError {
		return "failed"
	}
	var outcome struct {
		ExitCode          *int   `json:"exit_code"`
		TimedOut          bool   `json:"timed_out"`
		PromotedProcessID string `json:"promoted_process_id"`
		Verification      *struct {
			Passed         bool `json:"passed"`
			FailureSummary struct {
				Failed bool `json:"failed"`
			} `json:"failure_summary"`
		} `json:"verification"`
	}
	if err := json.Unmarshal([]byte(producerText(result)), &outcome); err != nil {
		return "failed"
	}
	if outcome.TimedOut && outcome.PromotedProcessID != "" {
		return "running"
	}
	if outcome.ExitCode == nil || *outcome.ExitCode != 0 || outcome.TimedOut ||
		(outcome.Verification != nil && (!outcome.Verification.Passed || outcome.Verification.FailureSummary.Failed)) {
		return "failed"
	}
	return "completed"
}

// producerText returns the tool's own text payload, ignoring any settled model
// view, for callers that need the structured envelope.
func producerText(result toolresult.Result) string {
	for _, part := range result.Content {
		if part.Type == toolresult.ContentTypeText {
			return part.Text
		}
	}
	return ""
}
