package runtime

import (
	"context"
	"encoding/json"
	"path/filepath"
	"slices"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
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
func configurationProviderFactory(path, root string) NativeProviderFactory {
	return NativeProviderFactory{ID: config.Module, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
		return config.NewService(config.Module, path, root), nil
	}}
}
