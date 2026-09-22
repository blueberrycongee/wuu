package runtime

import (
	"context"
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// ExternalPluginTools holds the current generation for one external tool
// request. Reacquiring for every request makes live plugin disable effective
// even when an external engine retains an older tool catalog.
type ExternalPluginTools struct {
	agent.ToolExecutor
	executor *pluginToolExecutor
	release  func()
}

func (s *Session) AcquireExternalPluginTools(sessionID, cwd, permissionMode string) (*ExternalPluginTools, error) {
	s.pluginGenerationMu.Lock()
	generation := s.pluginGeneration
	host, dispatcher := s.PluginHost, s.HookDispatcher
	if generation != nil {
		generation.retain()
		host, dispatcher = generation.host, generation.hooks
	}
	s.pluginGenerationMu.Unlock()
	release := func() { s.ReleasePluginGeneration(generation) }
	kit, err := tools.New(cwd)
	if err != nil {
		release()
		return nil, err
	}
	kit.SetSessionID(sessionID)
	ConfigureToolkitPermissions(kit, config.ResolvedPermissions{Mode: permissionMode})
	if host != nil {
		configureToolkitSecurityExtensions(kit, host.ServiceRegistry())
	}
	executor := &pluginToolExecutor{inner: kit, host: host, threadID: sessionID, cwd: cwd, scope: "external"}
	var toolExecutor agent.ToolExecutor = executor
	if dispatcher != nil {
		kit.SetPermissionRequestHook(func(ctx context.Context, active *tools.Toolkit, _ tools.ToolInfo, call providers.ToolCall) error {
			_, err := dispatcher.Dispatch(ctx, hooks.PermissionRequest, &hooks.Input{
				SessionID: active.SessionID(), CWD: active.RootDir(), ToolName: call.Name,
				ToolInput: json.RawMessage(call.Arguments),
			})
			return err
		})
		toolExecutor = hooks.NewHookedExecutor(executor, dispatcher, sessionID, cwd)
	}
	return &ExternalPluginTools{ToolExecutor: toolExecutor, executor: executor, release: release}, nil
}

func (t *ExternalPluginTools) Close() { t.release() }

func (t *ExternalPluginTools) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	if rich, ok := t.ToolExecutor.(agent.RichToolExecutor); ok {
		return rich.ExecuteResult(ctx, call)
	}
	text, err := t.Execute(ctx, call)
	return toolresult.FromText(text), err
}

// Instructions includes only prompt contributions owned by plugins that
// explicitly expose tools to this engine.
func (t *ExternalPluginTools) Instructions(ctx context.Context, provider, model string) (string, error) {
	host := t.executor.host
	if host == nil {
		return "", nil
	}
	owners := make(map[string]bool)
	for _, definition := range t.Definitions() {
		if tool, ok := host.Tool(definition.Name); ok {
			owners[tool.PluginID] = true
		}
	}
	var sections []string
	for _, capability := range host.Capabilities(pluginhost.CapabilityAgentSystemPromptSection) {
		if !owners[capability.PluginID] {
			continue
		}
		var output pluginhost.SystemPromptSectionOutput
		if err := host.InvokeCapability(ctx, capability, pluginhost.SystemPromptSectionInput{CWD: t.executor.cwd, Provider: provider, Model: model}, &output); err != nil {
			if policyErr := host.HandleCapabilityError(capability, err); policyErr != nil {
				return "", policyErr
			}
			continue
		}
		if text := strings.TrimSpace(output.Text); text != "" {
			sections = append(sections, text)
		}
	}
	return strings.Join(sections, "\n\n"), nil
}
