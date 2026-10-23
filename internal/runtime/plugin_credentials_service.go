package runtime

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"strings"
	"sync"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/plugincredentials"
)

func (s *pluginHostServices) credentialRequest(ctx context.Context, raw json.RawMessage) (json.RawMessage, error) {
	ctx, cancel := context.WithCancel(ctx)
	stop := context.AfterFunc(s.lockContext, cancel)
	defer stop()
	defer cancel()
	var params plugincredentials.RequestParams
	if err := decodeServiceParams(raw, &params); err != nil {
		return nil, err
	}
	key, definition, err := s.ownedSetting(strings.TrimPrefix(params.Credential, s.pluginID+"."))
	if err != nil {
		return nil, err
	}
	if definition.Type != pluginpkg.SettingTypeSecret {
		return nil, serviceError("credential_not_owned", "credential must name this plugin's declared secret setting")
	}
	binding, err := plugincredentials.BindingFor(s.wuuHome, s.projectRoot, s.subjectID, key, definition.CredentialOrigins)
	if err != nil {
		return nil, serviceError("credential_unavailable", err.Error())
	}
	store, err := plugincredentials.Open()
	if err != nil {
		return nil, serviceError("credential_unavailable", err.Error())
	}
	result, err := plugincredentials.Request(ctx, store, binding, params)
	if err != nil {
		return nil, serviceError("credential_request_failed", err.Error())
	}
	return marshalServiceResult(result)
}

// StateServicesConsumer is the native consumer used by direct shell settings
// and credential operations. Its declarations belong to the shared graph.
const StateServicesConsumer = "wuu.state-consumer"

// StateServices captures both admission metadata and service routing from one
// generation. Release its owning handle only after the complete operation,
// including any credential refresh or cleanup, has finished.
type StateServices struct {
	Snapshot    ExtensionSnapshot
	Credentials *credentialstore.Client
	Settings    *pluginsettings.Client
	Config      *config.Client
	Artifacts   *ArtifactClient
}

func (s *Session) RetainStateServices() (*StateServices, func(), error) {
	if s == nil {
		return nil, nil, serviceError("service_unavailable", "runtime is unavailable")
	}
	s.pluginGenerationMu.Lock()
	if s.PluginHost == nil {
		s.pluginGenerationMu.Unlock()
		return nil, nil, serviceError("service_unavailable", "state service graph is unavailable")
	}
	if s.pluginGeneration == nil {
		s.pluginGeneration = s.capturePluginGeneration()
	}
	generation := s.pluginGeneration
	generation.retain()
	snapshot := ExtensionSnapshot{Plugins: append([]pluginpkg.Plugin(nil), generation.plugins...), ExtensionSettings: generation.settings.Extensions, PluginHost: generation.host}
	generation.revokedMu.Lock()
	for _, item := range generation.active {
		if !generation.revokedPlugins[item.ID] {
			snapshot.ActivePlugins = append(snapshot.ActivePlugins, item)
		}
	}
	generation.revokedMu.Unlock()
	s.pluginGenerationMu.Unlock()
	call := func(service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := snapshot.PluginHost.CallService(ctx, StateServicesConsumer, service, method, raw)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}
	var once sync.Once
	release := func() { once.Do(func() { s.ReleasePluginGeneration(generation) }) }
	return &StateServices{Snapshot: snapshot, Credentials: credentialstore.NewClient(call(credentialstore.ServiceName)), Settings: pluginsettings.NewClient(call(pluginsettings.ServiceName)), Config: config.NewClient(call(config.ServiceName)), Artifacts: NewArtifactClient(call(ArtifactServiceName))}, release, nil
}
