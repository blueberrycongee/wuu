package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const fusionDelegateToolName = "fusion_delegate"

// SetFusionDelegate binds the native Lead tool between runs. Cloned worker
// toolkits deliberately do not inherit it, so Sidekicks cannot recurse.
func (t *Toolkit) SetFusionDelegate(delegate func(context.Context, string) (string, error)) {
	t.fusionDelegate = delegate
	t.rebuildRegistry()
	t.activeProfileMu.Lock()
	t.publishActiveSurfaceLocked()
	t.activeProfileMu.Unlock()
}

func (t *Toolkit) withFusionSurface(surface capability.Surface) capability.Surface {
	if t.fusionDelegate == nil || surface.ProfileName == "" {
		return surface
	}
	out := cloneSurface(surface)
	if out.Tools == nil {
		out.Tools = map[string]capability.Capability{}
	}
	out.Tools[fusionDelegateToolName] = capability.CapabilityFusion
	if !surfaceHasCapability(out.Capabilities, capability.CapabilityFusion) {
		out.Capabilities = append(out.Capabilities, capability.CapabilityFusion)
	}
	return out
}

type fusionDelegateTool struct {
	delegate func(context.Context, string) (string, error)
	toolkit  *Toolkit
}

// FusionHandoffError means a worker or its managed processes did not settle.
// Lead tools remain gated until a later delegation successfully drains them.
type FusionHandoffError struct{ Err error }

func (e *FusionHandoffError) Error() string { return "Fusion handoff did not settle: " + e.Err.Error() }
func (e *FusionHandoffError) Unwrap() error { return e.Err }

// ClearFusionHandoff allows Lead tools again after the host has confirmed that
// the previous Sidekick and its managed processes have stopped.
func (t *Toolkit) ClearFusionHandoff() {
	t.fusionExecution.Lock()
	defer t.fusionExecution.Unlock()
	t.fusionHandoffErr = nil
}

// BeginFusionToolCall serializes native and extension calls with delegation.
// Exec/wait orchestrators defer this gate to their actual leaf operations.
func (t *Toolkit) BeginFusionToolCall(ctx context.Context, name string) (func(), error) {
	if t.fusionDelegate == nil || name == codeModeExecToolName {
		return func() {}, nil
	}
	var release func()
	if name == fusionDelegateToolName {
		t.fusionExecution.Lock()
		release = t.fusionExecution.Unlock
	} else {
		t.fusionExecution.RLock()
		release = t.fusionExecution.RUnlock
	}
	if err := ctx.Err(); err != nil {
		release()
		return nil, err
	}
	if name != fusionDelegateToolName && t.fusionHandoffErr != nil {
		err := t.fusionHandoffErr
		release()
		return nil, err
	}
	return release, nil
}

func (*fusionDelegateTool) Name() string               { return fusionDelegateToolName }
func (t *fusionDelegateTool) IsReadOnly() bool         { return !t.toolkit.env.AllowMutations }
func (*fusionDelegateTool) IsConcurrencySafe() bool    { return false }
func (*fusionDelegateTool) IsOrchestrator(string) bool { return true }
func (*fusionDelegateTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name:        fusionDelegateToolName,
		Description: "Delegate a bounded task to your persistent Fusion Sidekick, or send review feedback to the same Sidekick. Supply a self-contained brief with objective, context, constraints and acceptance checks. Waits until execution and managed processes settle, then returns a structured report. Inspect actual changes and verify before accepting. On blocked, needs_decision or failed, give guidance or take over directly. Call this tool by itself; do not combine it with other tool calls.",
		InputSchema: map[string]any{
			"type": "object", "additionalProperties": false,
			"required":   []string{"brief"},
			"properties": map[string]any{"brief": map[string]any{"type": "string", "description": "Task brief or follow-up guidance, including acceptance checks."}},
		},
	}
}
func (t *fusionDelegateTool) Execute(ctx context.Context, args string) (string, error) {
	var input struct {
		Brief string `json:"brief"`
	}
	if err := json.Unmarshal([]byte(args), &input); err != nil {
		return "", err
	}
	if strings.TrimSpace(input.Brief) == "" {
		return "", fmt.Errorf("Fusion delegation requires a brief")
	}
	result, err := t.delegate(ctx, input.Brief)
	var handoffErr *FusionHandoffError
	if errors.As(err, &handoffErr) {
		t.toolkit.fusionHandoffErr = handoffErr
	} else if err == nil {
		t.toolkit.fusionHandoffErr = nil
	}
	return result, err
}
