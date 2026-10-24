package runtime

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type pluginProcessScopeKey struct{}

// newHostProcessScope binds the graph-selected security services to host-owned
// process resources. This allocation has no default tool-provider side effects.
func newHostProcessScope(host *pluginhost.Host, config tools.ProcessScopeConfig) *tools.ProcessScope {
	config.Authorizer = &pluginAuthorizer{host: host}
	config.SandboxProvider = nil
	if host != nil && host.ServiceRegistry() != nil && host.ServiceRegistry().HasProvider(pluginhost.ProcessSandboxService, pluginhost.SecurityServiceMajor) {
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
