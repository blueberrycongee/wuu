package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func (s *Server) configureSessionToolPolicy(sessionID string, threadRuntime *runtime.ThreadRuntime) error {
	if s == nil || s.rt == nil || threadRuntime == nil {
		return nil
	}
	metadata, ok, err := session.Find(s.rt.SessionDir, sessionID)
	if err != nil || !ok {
		return err
	}
	encoded := strings.TrimSpace(metadata.ToolPolicyJSON)
	if encoded == "" {
		return nil
	}
	var policy pluginhost.SessionToolPolicy
	if err := json.Unmarshal([]byte(encoded), &policy); err != nil {
		return fmt.Errorf("decode session tool policy: %w", err)
	}
	if threadRuntime.StreamRunner == nil {
		return errors.New("the selected agent engine cannot enforce session tool_policy")
	}
	guarded := newSessionToolPolicyExecutor(threadRuntime.StreamRunner.Tools, policy)
	threadRuntime.StreamRunner.Tools = guarded
	if guard, ok := guarded.(*sessionToolPolicyExecutor); ok && threadRuntime.Toolkit != nil {
		for _, definition := range threadRuntime.Toolkit.Definitions() {
			if !guard.allowed(definition.Name) {
				threadRuntime.Toolkit.DisableTools(definition.Name)
			}
		}
	}
	return nil
}

// sessionToolPolicyExecutor is an attenuation-only decorator. It filters the
// model-visible definitions and repeats the same check at execution time, so a
// guessed or deferred tool name cannot bypass the policy.
type sessionToolPolicyExecutor struct {
	base    agent.ToolExecutor
	allow   map[string]struct{}
	deny    map[string]struct{}
	denyAll bool
}

func newSessionToolPolicyExecutor(base agent.ToolExecutor, policy pluginhost.SessionToolPolicy) agent.ToolExecutor {
	if base == nil || (len(policy.Allow) == 0 && len(policy.Deny) == 0) {
		return base
	}
	executor := &sessionToolPolicyExecutor{base: base}
	if len(policy.Allow) > 0 {
		executor.allow = make(map[string]struct{}, len(policy.Allow))
		for _, name := range policy.Allow {
			executor.allow[strings.TrimSpace(name)] = struct{}{}
		}
	}
	executor.deny = make(map[string]struct{}, len(policy.Deny))
	for _, name := range policy.Deny {
		name = strings.TrimSpace(name)
		if name == "*" {
			executor.denyAll = true
		}
		executor.deny[name] = struct{}{}
	}
	return executor
}

func (e *sessionToolPolicyExecutor) allowed(name string) bool {
	name = strings.TrimSpace(name)
	if e == nil || e.base == nil || e.denyAll {
		return false
	}
	if _, denied := e.deny[name]; denied {
		return false
	}
	if e.allow == nil {
		return true
	}
	_, allowed := e.allow[name]
	return allowed
}

func (e *sessionToolPolicyExecutor) Definitions() []providers.ToolDefinition {
	definitions := e.base.Definitions()
	out := make([]providers.ToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if e.allowed(definition.Name) {
			out = append(out, definition)
		}
	}
	return out
}

func (e *sessionToolPolicyExecutor) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	if !e.allowed(call.Name) {
		return "", fmt.Errorf("tool %q is disabled by the session tool policy", call.Name)
	}
	return e.base.Execute(ctx, call)
}

func (e *sessionToolPolicyExecutor) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if !e.allowed(call.Name) {
		return toolresult.Result{}, fmt.Errorf("tool %q is disabled by the session tool policy", call.Name)
	}
	if rich, ok := e.base.(agent.RichToolExecutor); ok {
		return rich.ExecuteResult(ctx, call)
	}
	text, err := e.base.Execute(ctx, call)
	return toolresult.FromText(text), err
}

func (e *sessionToolPolicyExecutor) FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result {
	if !e.allowed("read_file") {
		// A restricted session cannot follow archive cursors. Preserve its full
		// evidence instead of advertising recovery that the policy forbids.
		result = result.Clone()
		result.ModelText = nil
		return result
	}
	if finalizer, ok := e.base.(agent.ToolResultFinalizer); ok {
		return finalizer.FinalizeToolResult(call, result)
	}
	return result
}

func (e *sessionToolPolicyExecutor) SupportsTool(name string) bool {
	if !e.allowed(name) {
		return false
	}
	provider, ok := e.base.(agent.ToolSupportProvider)
	return !ok || provider.SupportsTool(name)
}

func (e *sessionToolPolicyExecutor) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	if !e.allowed(call.Name) {
		return agent.ToolMetadata{}, false
	}
	provider, ok := e.base.(agent.ToolMetadataProvider)
	if !ok {
		return agent.ToolMetadata{}, false
	}
	return provider.ToolMetadata(call)
}

func (e *sessionToolPolicyExecutor) AuthorizeTool(ctx context.Context, call providers.ToolCall, metadata agent.ToolMetadata) error {
	if !e.allowed(call.Name) {
		return fmt.Errorf("tool %q is disabled by the session tool policy", call.Name)
	}
	if gate, ok := e.base.(agent.ToolAuthorizationGate); ok {
		return gate.AuthorizeTool(ctx, call, metadata)
	}
	return nil
}

func (e *sessionToolPolicyExecutor) ToolDisplay(call providers.ToolCall) (providers.ToolCallDisplay, bool) {
	provider, ok := e.base.(agent.ToolDisplayProvider)
	if !ok {
		return providers.ToolCallDisplay{}, false
	}
	return provider.ToolDisplay(call)
}

func (e *sessionToolPolicyExecutor) LastAdditionalContext() string {
	provider, _ := e.base.(agent.ToolContextProvider)
	if provider == nil {
		return ""
	}
	return provider.LastAdditionalContext()
}

func (e *sessionToolPolicyExecutor) TakeAdditionalContext(call providers.ToolCall) string {
	provider, _ := e.base.(agent.ToolCallContextProvider)
	if provider == nil {
		return ""
	}
	return provider.TakeAdditionalContext(call)
}

func (e *sessionToolPolicyExecutor) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	provider, _ := e.base.(agent.ToolDiscoveryProvider)
	if provider == nil {
		return nil
	}
	definitions := provider.DiscoveredTools(call)
	out := make([]providers.LoadableToolDefinition, 0, len(definitions))
	for _, definition := range definitions {
		if e.allowed(definition.Name) {
			out = append(out, definition)
		}
	}
	return out
}
