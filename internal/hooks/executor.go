package hooks

import (
	"context"
	"encoding/json"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ToolExecutor matches agent.ToolExecutor so HookedExecutor can decorate
// the real Toolkit without importing the agent package.
type ToolExecutor interface {
	Definitions() []providers.ToolDefinition
	Execute(ctx context.Context, call providers.ToolCall) (string, error)
}

// HookedExecutor decorates a ToolExecutor with PreToolUse / PostToolUse /
// PostToolUseFailure hooks.  It implements ToolExecutor and can be used as
// a drop-in replacement wherever a Toolkit is expected.
type HookedExecutor struct {
	inner      ToolExecutor
	dispatcher *Dispatcher
	sessionID  string
	cwd        string
}

// NewHookedExecutor wraps inner with hook dispatch.
func NewHookedExecutor(inner ToolExecutor, d *Dispatcher, sessionID, cwd string) *HookedExecutor {
	return &HookedExecutor{
		inner:      inner,
		dispatcher: d,
		sessionID:  sessionID,
		cwd:        cwd,
	}
}

// Definitions delegates to the inner executor.
func (h *HookedExecutor) Definitions() []providers.ToolDefinition {
	return h.inner.Definitions()
}

// Execute fires PreToolUse hooks, delegates to the inner executor, then
// fires PostToolUse (on success) or PostToolUseFailure (on error).
//
// If a PreToolUse hook blocks, the inner executor is never called.
// If a PreToolUse hook returns UpdatedInput, the tool call arguments are
// replaced before delegation.
//
// PostToolUse and PostToolUseFailure hooks are fire-and-forget: their
// errors are not propagated so they cannot mask the real tool outcome.
func (h *HookedExecutor) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	input := &Input{
		SessionID: h.sessionID,
		CWD:       h.cwd,
		ToolName:  call.Name,
		ToolInput: json.RawMessage(call.Arguments),
	}

	// PreToolUse
	out, err := h.dispatcher.Dispatch(ctx, PreToolUse, input)
	if err != nil {
		return "", fmt.Errorf("hook blocked %s: %w", call.Name, err)
	}
	if len(out.UpdatedInput) > 0 {
		call.Arguments = string(out.UpdatedInput)
	}

	// Delegate to real tool.
	result, execErr := h.inner.Execute(ctx, call)

	// PostToolUse / PostToolUseFailure
	if execErr != nil {
		failInput := &Input{
			SessionID: h.sessionID,
			CWD:       h.cwd,
			ToolName:  call.Name,
			ToolInput: json.RawMessage(call.Arguments),
			Error:     execErr.Error(),
		}
		_, _ = h.dispatcher.Dispatch(ctx, PostToolUseFailure, failInput)
		return result, execErr
	}

	postInput := &Input{
		SessionID:    h.sessionID,
		CWD:          h.cwd,
		ToolName:     call.Name,
		ToolInput:    json.RawMessage(call.Arguments),
		ToolResponse: result,
	}
	_, _ = h.dispatcher.Dispatch(ctx, PostToolUse, postInput)

	return result, nil
}
