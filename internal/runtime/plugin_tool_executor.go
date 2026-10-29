package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolerrors"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

type pluginToolExecutor struct {
	inner              agent.ToolExecutor
	host               *pluginhost.Host
	threadID           string
	cwd                string
	surfaceMu          sync.Mutex
	freezeDepth        int
	frozenDefinitions  []providers.ToolDefinition
	replacementTargets map[string]struct{}
}

func newPluginToolExecutor(inner agent.ToolExecutor, host *pluginhost.Host, threadID, cwd string) agent.ToolExecutor {
	if inner == nil || host == nil {
		if kit, ok := inner.(*tools.Toolkit); ok {
			kit.SetCodeModeAdditionalTools(nil)
			kit.SetSkillToolProvider(nil)
		}
		return inner
	}
	executor := &pluginToolExecutor{inner: inner, host: host, threadID: threadID, cwd: cwd}
	if kit, ok := inner.(*tools.Toolkit); ok {
		kit.SetCodeModeAdditionalTools(executor.pluginDefinitions)
		kit.SetSkillToolProvider(executor.livePluginDefinitions)
	}
	return executor
}

func newPluginAwareToolExecutor(inner agent.ToolExecutor, host *pluginhost.Host, dispatcher *hooks.Dispatcher, pluginThreadID, hookSessionID, cwd string) agent.ToolExecutor {
	var executor agent.ToolExecutor
	if host == nil {
		executor = newPluginToolExecutor(inner, host, pluginThreadID, cwd)
	} else {
		executor = newNativeToolScopeExecutor(inner, host, dispatcher, pluginThreadID, cwd, false)
	}
	if dispatcher != nil {
		executor = hooks.NewHookedExecutor(executor, dispatcher, hookSessionID, cwd)
	}
	return executor
}

func newNativeToolScopeExecutor(inner agent.ToolExecutor, host *pluginhost.Host, dispatcher *hooks.Dispatcher, threadID, cwd string, clone bool) *selectedToolExecutor {
	scope := toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
		backend := inner
		kit, _ := inner.(*tools.Toolkit)
		if clone && kit != nil {
			var err error
			kit, err = kit.CloneForRoot(cwd)
			if err != nil {
				return toolservice.Backend{}, err
			}
			kit.SetSessionID(threadID)
			configureToolkitSecurityExtensions(kit, host)
			bindToolkitHooks(kit, dispatcher)
			backend = kit
		}
		executor := newPluginToolExecutor(backend, host, threadID, cwd)
		if kit != nil {
			if err := configureToolReplacements(executor, kit, kit.ReplacementBindings()); err != nil {
				return toolservice.Backend{}, err
			}
		}
		return toolservice.Backend{Executor: executor, Toolkit: kit}, nil
	})
	actorID := threadID
	if provider, ok := inner.(interface{ ExecutionActor() (string, string) }); ok {
		actorID, _ = provider.ExecutionActor()
	}
	selected := newSelectedToolExecutor(host, scope, selectedToolIdentity{threadID: threadID, cwd: cwd, actorID: actorID})
	if kit, ok := inner.(*tools.Toolkit); ok && !clone {
		selected.refreshAuthority = kit.RefreshAuthorityFrom
	}
	selected.rebind = func(nextHost *pluginhost.Host, nextHooks *hooks.Dispatcher, nextThread, nextCWD string) *selectedToolExecutor {
		return newNativeToolScopeExecutor(inner, nextHost, nextHooks, nextThread, nextCWD, true)
	}
	return selected
}

func replacePluginToolBindings(executor agent.ToolExecutor, host *pluginhost.Host, dispatcher *hooks.Dispatcher, threadID, cwd string) agent.ToolExecutor {
	if executor == nil {
		return nil
	}
	if hooked, ok := executor.(*hooks.HookedExecutor); ok {
		executor = hooked.Inner()
	}
	if previous, ok := executor.(*selectedToolExecutor); ok {
		if previous.rebind != nil {
			executor = previous.rebind(host, dispatcher, threadID, cwd)
		} else {
			// Root generation factories capture service bindings. Reconstruct
			// them via candidateToolScope, never replay an old-host closure.
			scope := toolservice.NewScope(func(context.Context) (toolservice.Backend, error) {
				return toolservice.Backend{}, errors.New("native tool rebinding requires a fresh generation-owned scope")
			})
			executor = newSelectedToolExecutor(host, scope, selectedToolIdentity{threadID: threadID, cwd: cwd, actorID: previous.identity.actorID})
		}
		if dispatcher != nil {
			executor = hooks.NewHookedExecutor(executor, dispatcher, threadID, cwd)
		}
		return executor
	}
	if previous, ok := executor.(*pluginToolExecutor); ok {
		executor = previous.inner
	}
	return newPluginAwareToolExecutor(executor, host, dispatcher, threadID, threadID, cwd)
}

func (e *pluginToolExecutor) Definitions() []providers.ToolDefinition {
	inner := e.inner.Definitions()
	if kit, ok := e.inner.(*tools.Toolkit); ok && kit.CodeModeOnly() {
		for _, definition := range e.pluginDefinitions() {
			if definition.DirectOnly {
				inner = append(inner, definition)
			}
		}
		return inner
	}
	return append(inner, e.pluginDefinitions()...)
}

func (e *pluginToolExecutor) pluginDefinitions() []providers.ToolDefinition {
	e.surfaceMu.Lock()
	defer e.surfaceMu.Unlock()
	if e.freezeDepth == 0 {
		return e.livePluginDefinitions()
	}
	definitions := append([]providers.ToolDefinition(nil), e.frozenDefinitions...)
	for i := range definitions {
		definitions[i].InputSchema = providers.CloneLoadableToolDefinition(providers.LoadableToolDefinition{InputSchema: definitions[i].InputSchema}).InputSchema
	}
	return definitions
}

func (e *pluginToolExecutor) livePluginDefinitions() []providers.ToolDefinition {
	registered := e.host.ToolDefinitions()
	plugin := make([]providers.ToolDefinition, 0, len(registered))
	for _, definition := range registered {
		if _, bound := e.replacementTargets[definition.Name]; bound {
			continue
		}
		if kit, ok := e.inner.(*tools.Toolkit); ok && kit.IsToolDisabled(definition.Name) {
			continue
		}
		if e.pluginToolAllowed(definition.Name) {
			plugin = append(plugin, definition)
		}
	}
	return plugin
}

func (e *pluginToolExecutor) Execute(ctx context.Context, call providers.ToolCall) (string, error) {
	result, err := e.ExecuteResult(ctx, call)
	return result.TextProjection(), err
}

func (e *pluginToolExecutor) FinalizeToolResult(call providers.ToolCall, result toolresult.Result) toolresult.Result {
	if finalizer, ok := e.inner.(agent.ToolResultFinalizer); ok {
		return finalizer.FinalizeToolResult(call, result)
	}
	return result
}

func (e *pluginToolExecutor) FinalizeToolOutcome(call providers.ToolCall, result toolresult.Result) agent.ToolFinalization {
	if finalizer, ok := e.inner.(agent.ToolOutcomeFinalizer); ok {
		return finalizer.FinalizeToolOutcome(call, result)
	}
	return agent.ToolFinalization{Result: e.FinalizeToolResult(call, result)}
}

func (e *pluginToolExecutor) TakeAdditionalContext(call providers.ToolCall) string {
	if provider, ok := e.inner.(agent.ToolCallContextProvider); ok {
		return provider.TakeAdditionalContext(call)
	}
	return ""
}

func (e *pluginToolExecutor) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if _, bound := e.replacementTargets[call.Name]; bound {
		return toolresult.Result{}, fmt.Errorf("plugin tool %q is bound as a replacement", call.Name)
	}
	directOnly := false
	if tool, ok := e.host.Tool(call.Name); ok {
		directOnly = tool.Registration.DirectOnly
	}
	if directOnly && toolctx.IsNestedCall(ctx) {
		return toolresult.Result{}, fmt.Errorf("tool %q must be called directly", call.Name)
	}
	if kit, ok := e.inner.(*tools.Toolkit); ok && kit.CodeModeOnly() && !directOnly && !kit.CodeModeDirectCallAllowed(call.Name) && !toolctx.IsNestedCall(ctx) {
		return toolresult.Result{}, errors.New("PTC mode requires calling tools inside run_code")
	}

	input, err := e.toolInput(ctx, call)
	if err != nil {
		return toolresult.Result{}, err
	}
	var result toolresult.Result
	var executeErr error
	if e.host.SupportsTool(call.Name) && !e.pluginToolAllowed(call.Name) {
		return toolresult.Result{}, toolerrors.New("tool_unavailable", fmt.Sprintf("plugin tool %q is unavailable in this execution scope", call.Name))
	}
	if e.host.SupportsTool(call.Name) {
		metadata, _ := e.ToolMetadata(call)
		if gate, ok := e.inner.(agent.ToolAuthorizationGate); ok {
			if err := gate.AuthorizeTool(ctx, call, metadata); err != nil {
				return toolresult.Result{}, err
			}
		}
		if kit, ok := e.inner.(*tools.Toolkit); ok {
			ctx = context.WithValue(ctx, pluginProcessToolkitKey{}, kit)
		}
		result, executeErr = e.host.ExecuteTool(ctx, call.Name, input)
	} else if rich, ok := e.inner.(agent.RichToolExecutor); ok {
		result, executeErr = rich.ExecuteResult(ctx, call)
	} else {
		var text string
		text, executeErr = e.inner.Execute(ctx, call)
		result = toolresult.FromText(text)
	}
	if executeErr != nil {
		result.IsError = true
	}
	return result, executeErr
}

func (e *pluginToolExecutor) toolInput(ctx context.Context, call providers.ToolCall) (pluginhost.ToolExecuteInput, error) {
	arguments := strings.TrimSpace(call.Arguments)
	if arguments == "" {
		arguments = "{}"
	}
	if !json.Valid([]byte(arguments)) {
		return pluginhost.ToolExecuteInput{}, toolerrors.New(
			toolerrors.InvalidArguments,
			fmt.Sprintf("tool %q arguments are invalid JSON", call.Name),
		)
	}
	stepIndex, _ := toolctx.StepIndex(ctx)
	execution := loopdriver.ExecutionContextFromContext(ctx)
	actorID := ""
	if provider, ok := e.inner.(interface{ ExecutionActor() (string, string) }); ok {
		actorID, _ = provider.ExecutionActor()
	}
	return pluginhost.ToolExecuteInput{
		SourceMessage:   execution.SourceMessage,
		MessageRelation: providers.CloneMessageRelation(execution.MessageRelation),
		SessionID:       e.threadID,
		ThreadID:        e.threadID,
		TurnID:          execution.ExecutionID,
		ActorID:         actorID,
		CWD:             e.cwd,
		StepIndex:       stepIndex,
		CallID:          call.ID,
		Tool:            call.Name,
		Arguments:       json.RawMessage(arguments),
	}, nil
}

func (e *pluginToolExecutor) SupportsTool(name string) bool {
	if _, bound := e.replacementTargets[name]; bound {
		return false
	}
	if e.host.SupportsTool(name) {
		return e.pluginToolAllowed(name)
	}
	provider, ok := e.inner.(agent.ToolSupportProvider)
	return ok && provider.SupportsTool(name)
}

func (e *pluginToolExecutor) pluginToolAllowed(name string) bool {
	tool, ok := e.host.Tool(name)
	if !ok {
		return false
	}
	if len(tool.Registration.ExecutionScopes) == 0 {
		return true
	}
	actorPath := ""
	if provider, ok := e.inner.(interface{ ExecutionActor() (string, string) }); ok {
		_, actorPath = provider.ExecutionActor()
	}
	scope := "child"
	if strings.TrimSpace(actorPath) == "" || strings.TrimSpace(actorPath) == agentthread.RootPath {
		scope = "root"
	}
	for _, allowed := range tool.Registration.ExecutionScopes {
		if strings.TrimSpace(allowed) == scope {
			return true
		}
	}
	return false
}

func (e *pluginToolExecutor) ToolMetadata(call providers.ToolCall) (agent.ToolMetadata, bool) {
	if tool, ok := e.host.Tool(call.Name); ok {
		if tool.Registration.Activity == nil {
			return agent.ToolMetadata{}, false
		}
		activity := tool.Registration.Activity
		return agent.ToolMetadata{
			Orchestrator:    activity.Orchestrator,
			ReadOnly:        activity.ReadOnly,
			ConcurrencySafe: activity.ConcurrencySafe,
			Destructive:     activity.Destructive,
			Risk:            activity.Risk,
			Reason:          activity.Reason,
		}, true
	}
	provider, ok := e.inner.(agent.ToolMetadataProvider)
	if !ok {
		return agent.ToolMetadata{}, false
	}
	return provider.ToolMetadata(call)
}

func (e *pluginToolExecutor) ToolDisplay(call providers.ToolCall) (providers.ToolCallDisplay, bool) {
	if kit, ok := e.inner.(*tools.Toolkit); ok {
		if replacement, found := kit.LookupTool(call.Name).(*replacementTool); found {
			if _, native := replacement.NativeToolForCall(call.Arguments); native {
				return kit.ToolDisplay(call)
			}
			if replacement.registration.Display == nil {
				return providers.ToolCallDisplay{}, false
			}
			return *replacement.registration.Display, true
		}
	}
	if tool, ok := e.host.Tool(call.Name); ok {
		if tool.Registration.Display == nil {
			return providers.ToolCallDisplay{}, false
		}
		return *tool.Registration.Display, true
	}
	provider, ok := e.inner.(agent.ToolDisplayProvider)
	if !ok {
		return providers.ToolCallDisplay{}, false
	}
	return provider.ToolDisplay(call)
}

func (e *pluginToolExecutor) DiscoveredTools(call providers.ToolCall) []providers.LoadableToolDefinition {
	provider, ok := e.inner.(agent.ToolDiscoveryProvider)
	if !ok {
		return nil
	}
	return provider.DiscoveredTools(call)
}

func (e *pluginToolExecutor) FreezeToolSurface() {
	if freezer, ok := e.inner.(agent.ToolSurfaceFreezer); ok {
		freezer.FreezeToolSurface()
	}
	e.surfaceMu.Lock()
	defer e.surfaceMu.Unlock()
	if e.freezeDepth == 0 {
		e.frozenDefinitions = e.livePluginDefinitions()
	}
	e.freezeDepth++
}
func (e *pluginToolExecutor) UnfreezeToolSurface() {
	e.surfaceMu.Lock()
	if e.freezeDepth > 0 {
		e.freezeDepth--
	}
	if e.freezeDepth == 0 {
		e.frozenDefinitions = nil
	}
	e.surfaceMu.Unlock()
	if freezer, ok := e.inner.(agent.ToolSurfaceFreezer); ok {
		freezer.UnfreezeToolSurface()
	}
}

func (e *pluginToolExecutor) PrepareModelStep(ctx context.Context, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
	if preparer, ok := e.inner.(agent.ModelStepPreparer); ok {
		return preparer.PrepareModelStep(ctx, history)
	}
	return nil, nil
}
