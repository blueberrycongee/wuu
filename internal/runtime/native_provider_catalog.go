package runtime

import (
	"errors"
	"fmt"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/compactionservice"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/defaultpolicy"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/permissionpolicy"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/titleservice"
	"github.com/blueberrycongee/wuu/internal/toolbatch"
	"github.com/blueberrycongee/wuu/internal/toolservice"
)

// The catalog contains constructors, not live providers. Resolution selects
// entries before any provider resources are constructed. Native defaults and
// embedding replacements then enter exactly the same graph/lifecycle path.
func defaultNativeProviderCatalog() []NativeProviderFactory {
	catalog := []NativeProviderFactory{
		{ID: loopdriver.DefaultDriverID, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return loopdriver.NewService(loopdriver.DefaultDriverID, DriverServicePrefix+"default", agent.DefaultDriver{}, input.Registry, []pluginhost.ServiceRequirement{
				{Name: pluginprotocol.DriverTurnService, MajorVersion: pluginprotocol.DriverTurnMajor, Required: true},
				{Name: loopdriver.DriverCheckpointService, MajorVersion: 1, Required: true},
			}), nil
		}},
		{ID: defaultpolicy.Module, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) { return defaultpolicy.NewService(), nil }},
		{ID: compactionservice.NativeModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) { return compactionservice.NewService(), nil }},
		{ID: titleservice.NativeModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) { return titleservice.NewService(), nil }},
		{ID: promptservice.NativeModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) { return promptservice.NewService(), nil }},
		{ID: toolbatch.AdapterModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return toolbatch.NewService(input.Registry), nil
		}},
		{ID: toolservice.DefaultModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) { return toolservice.NewService(), nil }},
		{ID: session.DefaultModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return newNativeSessionDomain(input.Registry, statepath.SessionsDir(input.WuuHome), input.Host), nil
		}},
		{ID: credentialstore.OSModule, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
			return credentialstore.NewService(credentialstore.OSModule, credentialstore.NewDesktopStore), nil
		}},
		{ID: credentialstore.FileModule, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return credentialstore.NewService(credentialstore.FileModule, func() (credentialstore.Store, error) {
				return credentialstore.NewFileStore(filepath.Join(input.WuuHome, "credentials.json")), nil
			}), nil
		}},
		{ID: pluginsettings.Module, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return pluginsettings.NewService(pluginsettings.Module, input.WuuHome, input.RootDir), nil
		}},
		{ID: config.Module, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return config.NewService(config.Module, filepath.Join(input.WuuHome, "config.json"), input.RootDir), nil
		}},
		{ID: permissionpolicy.Module, DefaultEnabled: true, Build: func(NativeProviderContext) (pluginhost.Client, error) {
			return permissionpolicy.NewService(permissionpolicy.Module), nil
		}},
		{ID: ArtifactModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return NewArtifactService(ArtifactModule, input.WuuHome, input.WorkspaceStateDir), nil
		}},
		{ID: DataQueryModule, DefaultEnabled: true, Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			return NewDataQueryService(DataQueryModule, input.WorkspaceStateDir), nil
		}},
		nativeModelProviderFactory(), nativeEngineProviderFactory(), resourceAccessConsumerFactory(), resourcePolicyProviderFactory(), sessionSharingProviderFactory(), sessionSharingConsumerFactory(),
	}
	for _, backend := range []struct{ id, name string }{{persistence.SQLiteModule, "sqlite"}, {persistence.JSONLModule, "jsonl"}} {
		catalog = append(catalog, NativeProviderFactory{ID: backend.id, DefaultEnabled: backend.name == "sqlite", Build: func(input NativeProviderContext) (pluginhost.Client, error) {
			if strings.TrimSpace(input.WuuHome) == "" {
				return nil, errors.New("state provider requires a resolved Wuu home")
			}
			return persistence.NewService(backend.id, filepath.Join(statepath.SessionsDir(input.WuuHome), "documents"), backend.name), nil
		}})
	}
	return catalog
}

func nativeProviderCatalog(factories []NativeProviderFactory) ([]NativeProviderFactory, error) {
	result := append([]NativeProviderFactory(nil), factories...)
	present := map[string]bool{}
	for _, factory := range factories {
		if factory.ID == "" || factory.Build == nil {
			return nil, fmt.Errorf("native provider requires an identity and constructor")
		}
		if present[factory.ID] {
			return nil, fmt.Errorf("duplicate native provider %q", factory.ID)
		}
		present[factory.ID] = true
	}
	for _, factory := range defaultNativeProviderCatalog() {
		if !present[factory.ID] {
			result = append(result, factory)
		}
	}
	return result, nil
}
