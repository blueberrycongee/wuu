package hooks

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// ToolExecutor matches agent.ToolExecutor so HookedExecutor can decorate
// the real Toolkit without importing the agent package.
type ToolExecutor interface {
	Definitions() []providers.ToolDefinition
	Execute(ctx context.Context, call providers.ToolCall) (string, error)
}

type RichToolExecutor interface {
	ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error)
}

// HookedExecutor decorates a ToolExecutor with PreToolUse / PostToolUse /
// PostToolUseFailure hooks.  It implements ToolExecutor and can be used as
// a drop-in replacement wherever a Toolkit is expected.
//
// It also implements agent.ToolContextProvider: after each Execute call,
// LastAdditionalContext() returns any additional_context from PostToolUse
// hooks. The agent loop projects this into the next provider request as
// request-only context so the model sees hook-provided context without
// adding it to durable conversation history.
type HookedExecutor struct {
	inner             ToolExecutor
	dispatcher        *Dispatcher
	sessionID         string
	cwd               string
	contextMu         sync.Mutex
	lastAdditionalCtx string            // legacy serial consumer support
	additionalByCall  map[string]string // populated by PostToolUse hooks
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

func (h *HookedExecutor) FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result {
	if finalizer, ok := h.inner.(agent.ToolResultFinalizer); ok {
		return finalizer.FinalizeToolResult(call, result)
	}
	return result
}

// SupportsTool forwards tool-surface checks through the hook layer. This keeps
// deferred tool support visible to the agent loop even when hooks decorate the
// real Toolkit.
func (h *HookedExecutor) SupportsTool(name string) bool {
	if h == nil || h.inner == nil {
		return false
	}
	if sp, ok := h.inner.(agent.ToolSupportProvider); ok {
		return sp.SupportsTool(name)
	}
	name = strings.TrimSpace(name)
	if name == "" {
		return false
	}
	for _, def := range h.inner.Definitions() {
		if strings.EqualFold(strings.TrimSpace(def.Name), name) {
			return true
		}
	}
	return false
}

// ToolMetadata forwards inner metadata, but disables speculative and concurrent
// execution when pre-tool hooks can change the arguments after classification.
func (h *HookedExecutor) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	if mp, ok := h.inner.(agent.ToolMetadataProvider); ok {
		meta, found := mp.ToolMetadata(call)
		if found && h.dispatcher != nil && h.dispatcher.hasMatchingHooks(PreToolUse, call.Name) {
			meta.ReadOnly = false
			meta.ConcurrencySafe = false
		}
		return meta, found
	}
	return agent.ToolMetadata{}, false
}

func (h *HookedExecutor) ToolDisplay(call providers.ToolCall) (providers.ToolCallDisplay, bool) {
	if dp, ok := h.inner.(agent.ToolDisplayProvider); ok {
		return dp.ToolDisplay(call)
	}
	return providers.ToolCallDisplay{}, false
}

// Inner exposes the decorated executor so host-owned composition layers can be
// replaced without dropping this hook lifecycle.
func (h *HookedExecutor) Inner() ToolExecutor { return h.inner }

// WithInner returns the same hook configuration around a replacement executor.
func (h *HookedExecutor) WithInner(inner ToolExecutor) *HookedExecutor {
	if h == nil {
		return nil
	}
	return NewHookedExecutor(inner, h.dispatcher, h.sessionID, h.cwd)
}

func (h *HookedExecutor) AuthorizeTool(ctx context.Context, call providers.ToolCall, metadata agent.ToolMetadata) error {
	if gate, ok := h.inner.(agent.ToolAuthorizationGate); ok {
		return gate.AuthorizeTool(ctx, call, metadata)
	}
	return nil
}

// DiscoveredTools forwards provider-native deferred tool discovery through the
// hook layer. Without this, successful tools can activate deferred schemas in
// the inner Toolkit while the agent loop cannot attach them to the tool result.
func (h *HookedExecutor) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	if h == nil || h.inner == nil {
		return nil
	}
	if dp, ok := h.inner.(agent.ToolDiscoveryProvider); ok {
		return providers.CloneLoadableToolDefinitions(dp.DiscoveredTools(call))
	}
	return nil
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
	result, err := h.ExecuteResult(ctx, call)
	return result.TextProjection(), err
}

// ExecuteResult preserves the canonical rich result while exposing only a
// deterministic projection to legacy hook payloads.
func (h *HookedExecutor) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	h.clearAdditionalContext(call)
	input := &Input{
		SessionID: h.sessionID,
		CWD:       h.cwd,
		ToolName:  call.Name,
		ToolInput: json.RawMessage(call.Arguments),
	}

	// PreToolUse
	out, err := h.dispatcher.Dispatch(ctx, PreToolUse, input)
	if err != nil {
		return toolresult.Result{}, fmt.Errorf("hook blocked %s: %w", call.Name, err)
	}
	if len(out.UpdatedInput) > 0 {
		call.Arguments = string(out.UpdatedInput)
	}

	// Delegate to real tool.
	var result toolresult.Result
	var execErr error
	if rich, ok := h.inner.(RichToolExecutor); ok {
		result, execErr = rich.ExecuteResult(ctx, call)
	} else {
		var text string
		text, execErr = h.inner.Execute(ctx, call)
		result = toolresult.FromText(text)
	}
	if execErr != nil {
		result.IsError = true
	}
	if validationErr := result.Validate(); validationErr != nil {
		if execErr == nil {
			execErr = fmt.Errorf("tool %s returned invalid rich result: %w", call.Name, validationErr)
		}
		result.IsError = true
	}

	// Rich tools can report a domain failure without a Go execution error.
	// Preserve that distinction while routing both forms to the failure event.
	if execErr != nil || result.IsError {
		errorText := result.HookProjection()
		if execErr != nil {
			errorText = execErr.Error()
		}
		failInput := &Input{
			SessionID: h.sessionID,
			CWD:       h.cwd,
			ToolName:  call.Name,
			ToolInput: json.RawMessage(call.Arguments),
			Error:     errorText,
		}
		_, _ = h.dispatcher.Dispatch(ctx, PostToolUseFailure, failInput)
		return result, execErr
	}

	postInput := &Input{
		SessionID:    h.sessionID,
		CWD:          h.cwd,
		ToolName:     call.Name,
		ToolInput:    json.RawMessage(call.Arguments),
		ToolResponse: result.HookProjection(),
	}
	postOut, _ := h.dispatcher.Dispatch(ctx, PostToolUse, postInput)
	if postOut != nil && postOut.Context != "" {
		h.storeAdditionalContext(call, postOut.Context)
	}

	return result, nil
}

// LastAdditionalContext returns the additional context from the most
// recent PostToolUse hook execution, if any. The value is cleared on
// each Execute call so it can only be consumed once. Implements
// agent.ToolContextProvider.
func (h *HookedExecutor) LastAdditionalContext() string {
	if h == nil {
		return ""
	}
	h.contextMu.Lock()
	defer h.contextMu.Unlock()
	context := h.lastAdditionalCtx
	h.lastAdditionalCtx = ""
	return context
}

// TakeAdditionalContext returns and clears PostToolUse context for one tool
// call. Keying context by provider call ID keeps concurrent results isolated
// even when they finish out of order. Implements agent.ToolCallContextProvider.
func (h *HookedExecutor) TakeAdditionalContext(call providers.ToolCall) string {
	if h == nil || strings.TrimSpace(call.ID) == "" {
		return ""
	}
	h.contextMu.Lock()
	defer h.contextMu.Unlock()
	context := h.additionalByCall[call.ID]
	delete(h.additionalByCall, call.ID)
	return context
}

func (h *HookedExecutor) clearAdditionalContext(call providers.ToolCall) {
	if h == nil {
		return
	}
	h.contextMu.Lock()
	defer h.contextMu.Unlock()
	h.lastAdditionalCtx = ""
	if strings.TrimSpace(call.ID) != "" {
		delete(h.additionalByCall, call.ID)
	}
}

func (h *HookedExecutor) storeAdditionalContext(call providers.ToolCall, context string) {
	if h == nil {
		return
	}
	h.contextMu.Lock()
	defer h.contextMu.Unlock()
	h.lastAdditionalCtx = context
	if strings.TrimSpace(call.ID) == "" {
		return
	}
	if h.additionalByCall == nil {
		h.additionalByCall = make(map[string]string)
	}
	h.additionalByCall[call.ID] = context
}
