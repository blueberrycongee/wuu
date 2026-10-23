package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/siwc"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func stateServiceModules() []string {
	return []string{StateServicesConsumer, credentialstore.OSModule, credentialstore.FileModule, pluginsettings.Module, config.Module, permissionpolicy.Module, ArtifactModule, DataQueryModule}
}

func nativeStateProviders(home, root, stateDir string, factoryIDs []string) ([]pluginhost.Client, error) {
	configPath, err := statepath.ConfigPath(home)
	if err != nil {
		return nil, err
	}
	modules := []pluginhost.Client{
		credentialstore.NewService(credentialstore.OSModule, credentialstore.NewDesktopStore),
		credentialstore.NewService(credentialstore.FileModule, func() (credentialstore.Store, error) {
			return credentialstore.NewFileStore(filepath.Join(home, "credentials.json")), nil
		}),
		pluginsettings.NewService(pluginsettings.Module, home, root),
		config.NewService(config.Module, configPath, root),
		permissionpolicy.NewService(permissionpolicy.Module),
		NewArtifactService(ArtifactModule, home, stateDir),
		NewDataQueryService(DataQueryModule, stateDir),
	}
	result := make([]pluginhost.Client, 0, len(modules))
	for _, module := range modules {
		if !slices.Contains(factoryIDs, module.ID()) {
			result = append(result, module)
		}
	}
	return result, nil
}

func boundServiceCall(host *pluginhost.Host, consumer, service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		result, err := host.CallService(ctx, consumer, service, method, raw)
		if err != nil {
			return nil, err
		}
		return result, nil
	}
}

// The source path is bootstrap metadata; ordinary reads and writes still go
// through the selected state.config binding, including external providers.
func configurationProviderFactory(path, root string, cfg config.Config) NativeProviderFactory {
	return NativeProviderFactory{ID: config.Module, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		if path == "" {
			return config.NewMemoryService(config.Module, cfg), nil
		}
		return config.NewService(config.Module, path, root), nil
	}}
}

// runtimeServices owns generation-scoped authentication and captured clients.
// Its lifecycle consumer keeps providers alive until model/login cleanup ends.
type runtimeServices struct {
	credentials credentialstore.Store
	config      *config.Client
	artifacts   *ArtifactClient
	chatGPT     *siwc.Manager
	oauth       *mcp.OAuthManager
}

func newRuntimeServices(host *pluginhost.Host, home string) (*runtimeServices, error) {
	credentials := credentialstore.NewClient(boundServiceCall(host, StateServicesConsumer, credentialstore.ServiceName))
	manager, err := siwc.NewBoundManager(home, credentials)
	if err != nil {
		return nil, err
	}
	return &runtimeServices{credentials: credentials, config: config.NewClient(boundServiceCall(host, StateServicesConsumer, config.ServiceName)), artifacts: NewArtifactClient(boundServiceCall(host, StateServicesConsumer, ArtifactServiceName)), chatGPT: manager, oauth: mcp.NewOAuthManager(credentials, nil)}, nil
}

// A kernel adapter continues the provider callback admitted by the registry.
// The ID is attached only by the invoker; domain input cannot supply authority.
type kernelServiceExecutionKey struct{}

func kernelServiceCall(host *pluginhost.Host, registry *pluginhost.ServiceRegistry, service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		if execution, ok := ctx.Value(kernelServiceExecutionKey{}).(string); ok && execution != "" {
			result, err := registry.Call(ctx, kernelHostModule, pluginhost.ServiceCallParams{Service: service, Method: method, Params: raw, ExecutionID: execution})
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		return boundServiceCall(host, kernelHostModule, service)(ctx, method, raw)
	}
}

func withConfigurationProvider(factories []NativeProviderFactory, path, root string, cfg config.Config) []NativeProviderFactory {
	result := append([]NativeProviderFactory(nil), factories...)
	for _, factory := range result {
		if factory.ID == config.Module {
			return result
		}
	}
	return append(result, configurationProviderFactory(path, root, cfg))
}
