package tools

import (
	"context"
	"encoding/json"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type RequestHandoffTool struct{ env *Env }

func NewRequestHandoffTool(env *Env) *RequestHandoffTool { return &RequestHandoffTool{env: env} }
func (*RequestHandoffTool) Name() string                 { return "request_handoff" }
func (*RequestHandoffTool) IsReadOnly() bool             { return true }
func (*RequestHandoffTool) IsConcurrencySafe() bool      { return true }
func (*RequestHandoffTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        "request_handoff",
		Description: "Ask the user to hand this conversation to a new session. Supply an optional intent only. Do not choose a provider or model.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"properties": map[string]any{"intent": map[string]any{"type": "string", "description": "What the destination session should continue doing."}},
		},
	}
}

func (t *RequestHandoffTool) Execute(ctx context.Context, arguments string) (string, error) {
	result, err := t.ExecuteResultCall(ctx, providers.ToolCall{Arguments: arguments})
	return result.TextProjection(), err
}

func (t *RequestHandoffTool) ExecuteResultCall(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if err := ctx.Err(); err != nil {
		return toolresult.Result{}, err
	}
	if t.env == nil || strings.TrimSpace(t.env.SessionID) == "" {
		return toolresult.Result{}, errors.New("request_handoff requires a session")
	}
	var input struct {
		Intent   string `json:"intent"`
		Provider string `json:"provider"`
		Model    string `json:"model"`
	}
	if err := decodeArgs(call.Arguments, &input); err != nil {
		return toolresult.Result{}, err
	}
	if strings.TrimSpace(input.Provider) != "" || strings.TrimSpace(input.Model) != "" {
		return toolresult.Result{}, errors.New("request_handoff cannot select a provider or model")
	}
	payload, err := json.Marshal(map[string]any{
		"request_id":                  strings.TrimSpace(call.ID),
		"awaiting_user_configuration": true,
		"intent":                      strings.TrimSpace(input.Intent),
		"source_session_id":           t.env.SessionID,
	})
	return toolresult.FromText(string(payload)), err
}
