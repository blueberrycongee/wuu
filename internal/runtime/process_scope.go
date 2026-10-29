package runtime

import (
	"context"
	"encoding/json"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type pluginProcessScopeKey struct{}

// newHostProcessScope binds the graph-selected security services to host-owned
// process resources. This allocation has no default tool-provider side effects.
func newHostProcessScope(host *pluginhost.Host, config tools.ProcessScopeConfig) *tools.ProcessScope {
	config.Authorizer = &pluginAuthorizer{host: host}
	config.SandboxProvider = nil
	if host != nil && host.ServiceRegistry() != nil && host.ServiceRegistry().HasProviderFor(toolExecutionConsumer, pluginhost.ProcessSandboxService, pluginhost.SecurityServiceMajor) {
		config.SandboxProvider = &pluginSandboxProvider{host: host}
	}
	return tools.NewProcessScope(config)
}

func bindHostProcessScope(ctx context.Context, processScope *tools.ProcessScope, input pluginhost.ToolExecuteInput) (context.Context, error) {
	if processScope == nil {
		return ctx, nil
	}
	bound, err := processScope.Bind(input.ThreadID, input.ActorID, input.CWD)
	if err != nil {
		return nil, err
	}
	return context.WithValue(ctx, pluginProcessScopeKey{}, bound), nil
}

// ConfigureExecutionPermissions captures current turn authority for both native
// toolkits and independent selected providers before their execution begins.
func (t *ThreadRuntime) ConfigureExecutionPermissions(permissions config.ResolvedPermissions, approve bool, reviewer tools.Reviewer) {
	if t == nil {
		return
	}
	if t.Toolkit != nil {
		ConfigureToolkitPermissions(t.Toolkit, permissions)
		t.Toolkit.SetApproveForMe(approve)
		if !approve {
			reviewer = nil
		}
		t.Toolkit.SetReviewer(reviewer)
		return
	}
	cfg := t.processScopeConfig
	cfg.PermissionMode = config.NormalizePermissionMode(permissions.Mode)
	cfg.Boundary = BoundaryForMode(permissions.Mode)
	cfg.ApproveForMe = approve
	if approve {
		cfg.Reviewer = reviewer
	}
	if dispatcher := t.processHooks; dispatcher != nil {
		cfg.BeforePermission = func(ctx context.Context, _ tools.ToolInfo, call providers.ToolCall) error {
			_, err := dispatcher.Dispatch(ctx, hooks.PermissionRequest, &hooks.Input{SessionID: cfg.ThreadID, AgentID: cfg.ActorID, CWD: cfg.RootDir, ToolName: call.Name, ToolInput: json.RawMessage(call.Arguments)})
			return err
		}
	}
	executor := t.CapabilityTools
	if t.StreamRunner != nil {
		executor = t.StreamRunner.Tools
	}
	var selected *selectedToolExecutor
	// Only host-owned decorators can expose their underlying executor. Bound
	// traversal prevents a malformed decorator from blocking turn admission.
	for depth := 0; depth < 32 && executor != nil; depth++ {
		if target, ok := executor.(*selectedToolExecutor); ok {
			selected = target
			break
		}
		if wrapped, ok := executor.(*hooks.HookedExecutor); ok {
			executor = wrapped.Inner()
			continue
		}
		if wrapped, ok := executor.(interface{ UnwrapToolExecutor() agent.ToolExecutor }); ok {
			executor = wrapped.UnwrapToolExecutor()
			continue
		}
		break
	}
	if selected != nil {
		t.processScopeTarget.Store(selected)
	} else {
		selected = t.processScopeTarget.Load()
	}
	if selected != nil {
		selected.mu.Lock()
		selected.identity.processScope = newHostProcessScope(t.PluginHost, cfg)
		selected.mu.Unlock()
	}
}

func processGitAttribution(generation *PluginGeneration) bool {
	settings := config.AgentConfig{}
	if generation != nil {
		settings = generation.settings.Agent
	}
	return settings.GitAttributionEnabledValue()
}
