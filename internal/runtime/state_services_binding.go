package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/authstorage"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/siwc"
)

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
func configurationProviderFactory(path, root string, cfg config.Config, mode ConfigLoadMode) NativeProviderFactory {
	if mode == ConfigLoadFile {
		root = ""
	}
	return NativeProviderFactory{ID: config.Module, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		if mode == ConfigLoadProject {
			return config.NewProjectService(config.Module, root), nil
		}
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

// Set only by the kernel lifecycle callback; registry preparation admission
// still checks the consumer and each declared read-only method.
type kernelServicePreparationKey struct{}

func kernelServiceCall(host *pluginhost.Host, registry *pluginhost.ServiceRegistry, service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
	return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		execution, _ := ctx.Value(kernelServiceExecutionKey{}).(string)
		if execution != "" || ctx.Value(kernelServicePreparationKey{}) == true {
			result, err := registry.Call(ctx, kernelHostModule, pluginhost.ServiceCallParams{Service: service, Method: method, Params: raw, ExecutionID: execution})
			if err != nil {
				return nil, err
			}
			return result, nil
		}
		return boundServiceCall(host, kernelHostModule, service)(ctx, method, raw)
	}
}

func withConfigurationProvider(factories []NativeProviderFactory, path, root string, cfg config.Config, mode ConfigLoadMode) []NativeProviderFactory {
	result := append([]NativeProviderFactory(nil), factories...)
	for _, factory := range result {
		if factory.ID == config.Module {
			return result
		}
	}
	return append(result, configurationProviderFactory(path, root, cfg, mode))
}

// Native compatibility sources belong to the chosen native provider. External
// replacements receive no wrapper and never read the user's local auth files.
func withCredentialProviders(factories []NativeProviderFactory, userHome string) ([]NativeProviderFactory, error) {
	var err error
	if strings.TrimSpace(userHome) == "" {
		userHome, err = os.UserHomeDir()
		if err != nil {
			return nil, err
		}
	}
	userHome, err = filepath.Abs(userHome)
	if err != nil {
		return nil, err
	}
	result := append([]NativeProviderFactory(nil), factories...)
	for _, id := range []string{credentialstore.OSModule, credentialstore.FileModule} {
		if slices.ContainsFunc(result, func(factory NativeProviderFactory) bool { return factory.ID == id }) {
			continue
		}
		result = append(result, NativeProviderFactory{ID: id, DefaultEnabled: id == credentialstore.OSModule, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return credentialstore.NewService(id, func() (credentialstore.Store, error) {
				var backend credentialstore.Store
				if id == credentialstore.OSModule {
					var err error
					backend, err = credentialstore.NewDesktopStore()
					if err != nil {
						return nil, err
					}
				} else {
					backend = credentialstore.NewFileStore(filepath.Join(input.WuuHome, "credentials.json"))
				}
				return authstorage.NewNativeStore(backend, input.WuuHome, userHome)
			}), nil
		}})
	}
	return result, nil
}
