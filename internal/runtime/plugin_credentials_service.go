package runtime

import (
	"context"
	"encoding/json"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/siwc"

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
	if s.credentials == nil {
		return nil, serviceError("service_unavailable", "selected credential provider is unavailable")
	}
	result, err := plugincredentials.Request(ctx, s.credentials, binding, params)
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
	GenerationID string
	Snapshot     ExtensionSnapshot
	Credentials  *credentialstore.Client
	Settings     *pluginsettings.Client
	Config       *config.Client
	Artifacts    *ArtifactClient
	Index        *IndexClient
	ChatGPT      *siwc.Manager
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
	s.pluginGenerationMu.Unlock()
	var once sync.Once
	release := func() { once.Do(func() { s.ReleasePluginGeneration(generation) }) }
	return stateServicesForGeneration(generation), release, nil
}

// Services uses this retained state's generation, never the Session's latest
// publication. The caller must keep the State alive throughout the operation.
func (s *State) Services() (*StateServices, error) {
	if s == nil {
		return nil, serviceError("service_unavailable", "state is unavailable")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed || s.Generation == nil || s.Host == nil {
		return nil, serviceError("service_unavailable", "state service generation is unavailable")
	}
	return stateServicesForGeneration(s.Generation), nil
}

func stateServicesForGeneration(generation *PluginGeneration) *StateServices {
	generation.revokedMu.Lock()
	snapshot := ExtensionSnapshot{GenerationID: generation.id, Plugins: append([]pluginpkg.Plugin(nil), generation.plugins...), ExtensionSettings: generation.settings.Extensions, PluginHost: generation.host}
	for _, item := range generation.active {
		if !generation.revokedPlugins[item.ID] {
			snapshot.ActivePlugins = append(snapshot.ActivePlugins, item)
		}
	}
	generation.revokedMu.Unlock()
	call := func(service string) func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
		return func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
			result, err := snapshot.PluginHost.CallService(ctx, StateServicesConsumer, service, method, raw)
			if err != nil {
				return nil, err
			}
			return result, nil
		}
	}
	return &StateServices{GenerationID: generation.id, ChatGPT: generation.chatGPTManager, Snapshot: snapshot, Credentials: credentialstore.NewClient(call(credentialstore.ServiceName)), Settings: pluginsettings.NewClient(call(pluginsettings.ServiceName)), Config: config.NewClient(call(config.ServiceName)), Artifacts: NewArtifactClient(call(ArtifactServiceName)), Index: NewIndexClient(call(pluginhost.KernelDataQueryService))}
}

// ModelClient routes through the captured generation. Its owning State or
// RetainStateServices handle must outlive all invocations and receipt cleanup.
func (s *StateServices) ModelClient(cfg config.ProviderConfig, provider string) (providers.StreamClient, error) {
	if s == nil || s.Snapshot.PluginHost == nil {
		return nil, serviceError("service_unavailable", "selected model provider graph is unavailable")
	}
	return (&State{Host: s.Snapshot.PluginHost}).ModelClient(cfg, provider)
}

// ValidateModelConfiguration checks an unsaved candidate using the captured
// selected adapter without publishing configuration or submitting inference.
func (s *StateServices) ValidateModelConfiguration(ctx context.Context, cfg config.ProviderConfig, provider string) error {
	if s == nil || s.Snapshot.PluginHost == nil {
		return serviceError("service_unavailable", "selected model provider graph is unavailable")
	}
	return ValidateSelectedModelConfiguration(ctx, s.Snapshot.PluginHost, cfg, provider)
}
