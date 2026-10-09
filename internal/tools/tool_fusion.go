package tools

import (
	"context"
	"encoding/json"
	"errors"
	"io"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type FusionDelegateRequest struct {
	Action    string `json:"action,omitempty"`
	Message   string `json:"message,omitempty"`
	Block     *bool  `json:"block,omitempty"`
	TaskID    string `json:"task_id,omitempty"`
	ReportID  string `json:"report_id,omitempty"`
	Revision  int    `json:"revision,omitempty"`
	Verdict   string `json:"verdict,omitempty"`
	Reason    string `json:"reason,omitempty"`
	ReadOnly  *bool  `json:"read_only,omitempty"`
	TimeoutMS int    `json:"timeout_ms,omitempty"`
}

func (r FusionDelegateRequest) Validate() error {
	action := strings.TrimSpace(r.Action)
	if action == "" {
		action = "delegate"
	}
	if r.TimeoutMS < 0 {
		return errors.New("timeout_ms must be positive")
	}
	if action != "delegate" && r.ReadOnly != nil {
		return errors.New("read_only is selected only when delegating a new task")
	}
	if action != "review" && (r.ReportID != "" || r.Revision != 0 || r.Verdict != "") {
		return errors.New("report_id, revision and verdict belong to review")
	}
	if action != "stop" && r.Reason != "" {
		return errors.New("reason belongs to stop")
	}
	if action != "delegate" && action != "review" && action != "wait" && (r.Block != nil || r.TimeoutMS != 0) {
		return errors.New("this action does not wait; omit block and timeout_ms")
	}
	switch action {
	case "delegate":
		if strings.TrimSpace(r.Message) == "" || r.TaskID != "" {
			return errors.New("delegate needs a self-contained message and creates its own task_id")
		}
	case "update":
		if r.TaskID == "" || strings.TrimSpace(r.Message) == "" {
			return errors.New("update needs task_id and message")
		}
	case "wait":
		if r.TaskID == "" || r.Message != "" || r.Block != nil {
			return errors.New("wait needs task_id; omit message and block")
		}
	case "inspect":
		if r.Message != "" {
			return errors.New("inspect cannot send a message; use update")
		}
	case "stop":
		if r.Message != "" {
			return errors.New("stop immediately cancels work; use update to request wrap-up and a report")
		}
		if strings.TrimSpace(r.Reason) == "" {
			return errors.New("stop needs an explicit cancellation reason")
		}
		if r.TaskID == "" {
			return errors.New("stop needs the current task_id; it cannot cancel another task")
		}
	case "review":
		if r.TaskID == "" || r.ReportID == "" || r.Revision < 1 || strings.TrimSpace(r.Message) == "" || (r.Verdict != "accept" && r.Verdict != "request_changes") {
			return errors.New("review needs task_id, current report_id, revision, verdict and evidence or consolidated feedback")
		}
		if r.Verdict == "accept" && (r.Block != nil || r.TimeoutMS != 0) {
			return errors.New("accept does not start or wait for work")
		}
	default:
		return errors.New("unknown Fusion action")
	}
	return nil
}

type FusionDelegateHandler func(context.Context, string, FusionDelegateRequest) (any, error)
type FusionDelegateTool struct{ env *Env }

func (t *FusionDelegateTool) Name() string            { return "fusion_delegate" }
func (t *FusionDelegateTool) IsReadOnly() bool        { return false }
func (t *FusionDelegateTool) IsConcurrencySafe() bool { return false }
func (t *FusionDelegateTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{Name: t.Name(), DirectOnly: true, Description: "Give your persistent Sidekick one complete implementation or investigation task, then review its evidence. delegate(message) creates a task; include settled decisions, relevant inputs, constraints and targeted checks. It blocks by default; use block=false only for independent Lead work. Omit timeout_ms to wait until completion or new user input; explicit timeouts are capped at 300000 ms and leave Sidekick running. wait(task_id) resumes a blocking wait without sending work. report_delivered means the report text was already sent as a background notification. inspect retrieves current state, progress and the full report; do not poll it. update(task_id,message) steers existing work or requests a wrap-up. review(task_id,report_id,revision,verdict,message) accepts inspected work or sends one consolidated request_changes brief to the same Sidekick. read_only narrows a new task's permissions. stop(task_id,reason) cancels immediately; confirm idle before taking over writes. Reports require explicit acceptance of their latest revision.", InputSchema: map[string]any{"type": "object", "additionalProperties": false, "properties": map[string]any{
		"action":  map[string]any{"type": "string", "enum": []string{"delegate", "update", "wait", "inspect", "review", "stop"}},
		"message": map[string]any{"type": "string"}, "block": map[string]any{"type": "boolean", "default": true, "description": "For delegate or request_changes review. Omit for a blocking handoff; false requires independent work."},
		"task_id": map[string]any{"type": "string"}, "report_id": map[string]any{"type": "string"}, "revision": map[string]any{"type": "integer", "minimum": 1}, "verdict": map[string]any{"type": "string", "enum": []string{"accept", "request_changes"}}, "reason": map[string]any{"type": "string"}, "read_only": map[string]any{"type": "boolean"}, "timeout_ms": map[string]any{"type": "integer", "minimum": 1, "description": "Optional bounded wait, capped at 300000 ms. Omit to wait for completion, cancellation or new user input."},
	}}}
}
func (t *FusionDelegateTool) Execute(ctx context.Context, raw string) (string, error) {
	result, err := t.ExecuteResultCall(ctx, providers.ToolCall{ID: session.NewID(), Name: t.Name(), Arguments: raw})
	return result.TextProjection(), err
}
func (t *FusionDelegateTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if t.env == nil || t.env.FusionDelegate == nil {
		return toolresult.Result{}, errors.New("Fusion delegation is available only to its Lead")
	}
	var request FusionDelegateRequest
	decoder := json.NewDecoder(strings.NewReader(call.Arguments))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&request); err != nil {
		return toolresult.Result{}, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return toolresult.Result{}, errors.New("Fusion request must contain one JSON object")
	}
	if err := request.Validate(); err != nil {
		return toolresult.Result{}, err
	}
	result, err := t.env.FusionDelegate(ctx, call.ID, request)
	if err != nil {
		return toolresult.Result{}, err
	}
	data, err := json.Marshal(result)
	return toolresult.FromText(string(data)), err
}
