package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// selectedToolExecutor has no default implementation. Its scope factory is
// inert unless the graph-selected provider chooses the native backend.
type selectedToolIdentity struct {
	threadID, cwd, actorID string
	processScope           *tools.ProcessScope
}
type selectedToolCallKey struct{}

type selectedToolExecutor struct {
	client           *toolservice.Client
	host             *pluginhost.Host
	scope            *toolservice.Scope
	identity         selectedToolIdentity
	rebind           func(*pluginhost.Host, *hooks.Dispatcher, string, string) *selectedToolExecutor
	mu               sync.Mutex
	lifecycleErr     error
	refreshAuthority func(*tools.Toolkit)
	results          map[string]toolservice.ExecutionResult
}

func newSelectedToolExecutor(host *pluginhost.Host, scope *toolservice.Scope, identities ...selectedToolIdentity) *selectedToolExecutor {
	e := &selectedToolExecutor{host: host, scope: scope, results: make(map[string]toolservice.ExecutionResult)}
	if len(identities) > 0 {
		e.identity = identities[0]
	}
	e.client = toolservice.NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		ctx = toolservice.WithScope(ctx, scope)
		var result json.RawMessage
		var err *pluginhost.HostServiceError
		if method == "execute" {
			input, ok := ctx.Value(selectedToolCallKey{}).(pluginhost.ToolExecuteInput)
			if !ok {
				return nil, errors.New("selected tool execution requires host call identity")
			}
			var scopeErr error
			e.mu.Lock()
			processScope := e.identity.processScope
			e.mu.Unlock()
			ctx, scopeErr = bindHostProcessScope(ctx, processScope, input)
			if scopeErr != nil {
				return nil, scopeErr
			}
			result, err = host.CallToolService(ctx, toolExecutionConsumer, toolservice.ServiceName, method, raw, input)
		} else {
			result, err = host.CallService(ctx, toolExecutionConsumer, toolservice.ServiceName, method, raw)
		}
		if err != nil {
			return nil, err
		}
		return result, nil
	})
	return e
}
func (e *selectedToolExecutor) DefinitionsContext(ctx context.Context) ([]providers.ToolDefinition, error) {
	e.mu.Lock()
	err := e.lifecycleErr
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	view, err := e.client.List(ctx)
	if err != nil {
		return nil, err
	}
	result := make([]providers.ToolDefinition, 0, len(view.Tools))
	for _, d := range view.Tools {
		result = append(result, d.ProviderDefinition())
	}
	return result, nil
}
func (e *selectedToolExecutor) Definitions() []providers.ToolDefinition {
	result, _ := e.DefinitionsContext(context.Background())
	return result
}
func (e *selectedToolExecutor) surface(ctx context.Context) (capability.Surface, error) {
	view, err := e.client.List(ctx)
	if err != nil {
		return capability.Surface{}, err
	}
	surface := view.Surface
	surface.SystemFragment = view.SystemFragment
	surface.DeferredToolCatalog = view.CatalogPrompt
	return surface, nil
}
func (e *selectedToolExecutor) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	result, err := e.ExecuteResult(ctx, call)
	return result.TextProjection(), err
}
func (e *selectedToolExecutor) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	e.mu.Lock()
	err := e.lifecycleErr
	e.mu.Unlock()
	if err != nil {
		return toolresult.Result{}, err
	}
	if _, err := e.client.Resolve(ctx, call); err != nil {
		return toolresult.Result{}, err
	}
	execution := loopdriver.ExecutionContextFromContext(ctx)
	threadID := e.identity.threadID
	if threadID == "" {
		threadID = execution.SessionID
	}
	actorID := e.identity.actorID
	if actorID == "" {
		actorID = threadID
	}
	arguments := strings.TrimSpace(call.Arguments)
	if arguments == "" {
		arguments = "{}"
	}
	if call.Kind == providers.ToolCallKindCustom {
		encoded, err := json.Marshal(map[string]string{"input": call.Arguments})
		if err != nil {
			return toolresult.Result{}, err
		}
		arguments = string(encoded)
	} else if !json.Valid([]byte(arguments)) {
		return toolresult.Result{}, errors.New("selected tool arguments must be valid JSON")
	}
	step, _ := toolctx.StepIndex(ctx)
	input := pluginhost.ToolExecuteInput{SessionID: threadID, ThreadID: threadID, TurnID: execution.ExecutionID, ActorID: actorID, CWD: e.identity.cwd, StepIndex: step, CallID: call.ID, Tool: call.Name, Arguments: json.RawMessage(arguments)}
	if threadID == "" || input.TurnID == "" || input.CallID == "" || input.CWD == "" {
		return toolresult.Result{}, errors.New("selected tool execution requires thread, turn, call and working directory")
	}
	ctx = context.WithValue(ctx, selectedToolCallKey{}, input)
	result, err := e.client.Execute(ctx, call)
	if err != nil {
		return toolresult.Result{}, err
	}
	e.mu.Lock()
	e.results[call.ID] = result
	e.mu.Unlock()
	return result.Result, nil
}
func (e *selectedToolExecutor) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	metadata, found, _ := e.ToolMetadataContext(context.Background(), call)
	return metadata, found
}
func (e *selectedToolExecutor) ToolMetadataContext(ctx context.Context, call providers.ToolCall) (agent.ToolMetadata, bool, error) {
	result, err := e.client.Resolve(ctx, call)
	if err != nil || !result.Found || result.Metadata == nil {
		return agent.ToolMetadata{}, false, err
	}
	return result.Metadata.AgentMetadata(), true, nil
}
func (e *selectedToolExecutor) ToolDisplay(call providers.ToolCall) (providers.ToolCallDisplay, bool) {
	display, found, _ := e.ToolDisplayContext(context.Background(), call)
	return display, found
}
func (e *selectedToolExecutor) ToolDisplayContext(ctx context.Context, call providers.ToolCall) (providers.ToolCallDisplay, bool, error) {
	result, err := e.client.Resolve(ctx, call)
	if err != nil || result.Display == nil {
		return providers.ToolCallDisplay{}, false, err
	}
	return *result.Display, true, nil
}
func (e *selectedToolExecutor) SupportsTool(name string) bool {
	supported, _ := e.SupportsToolContext(context.Background(), name)
	return supported
}
func (e *selectedToolExecutor) SupportsToolContext(ctx context.Context, name string) (bool, error) {
	result, err := e.client.Resolve(ctx, providers.ToolCall{Name: name})
	return err == nil && result.Supports, err
}
func (e *selectedToolExecutor) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	e.mu.Lock()
	defer e.mu.Unlock()
	return providers.CloneLoadableToolDefinitions(e.results[call.ID].DiscoveredTools)
}
func (e *selectedToolExecutor) TakeAdditionalContext(call providers.ToolCall) string {
	e.mu.Lock()
	defer e.mu.Unlock()
	result := e.results[call.ID]
	delete(e.results, call.ID)
	return result.AdditionalContext
}
func (e *selectedToolExecutor) FreezeToolSurface() {
	err := e.client.Freeze(context.Background())
	e.mu.Lock()
	e.lifecycleErr = err
	e.mu.Unlock()
}
func (e *selectedToolExecutor) UnfreezeToolSurface() {
	err := e.client.Unfreeze(context.Background())
	e.mu.Lock()
	e.lifecycleErr = err
	e.mu.Unlock()
}
func (e *selectedToolExecutor) PrepareModelStep(ctx context.Context, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
	e.mu.Lock()
	err := e.lifecycleErr
	e.mu.Unlock()
	if err != nil {
		return nil, err
	}
	return e.client.Prepare(ctx, history)
}

// The agent calls settlement after normalizing execution errors. Do not settle
// in ExecuteResult as well, or successes are finalized twice and failures skip it.
func (e *selectedToolExecutor) FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result {
	settled, err := e.client.Finalize(context.Background(), call, result)
	if err != nil {
		return toolresult.FromErrorText(fmt.Sprintf("selected tool finalization failed: %v", err))
	}
	return settled
}
func selectedToolSurface(ctx context.Context, executor agent.ToolExecutor) (capability.Surface, error) {
	if hooked, ok := executor.(*hooks.HookedExecutor); ok {
		executor = hooked.Inner()
	}
	if selected, ok := executor.(*selectedToolExecutor); ok {
		return selected.surface(ctx)
	}
	return capability.Surface{}, nil
}
