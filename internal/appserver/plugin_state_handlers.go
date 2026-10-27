package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/plugincredentials"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func (s *Server) handlePluginSettingGet(req Request) error {
	var params PluginSettingGetParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer release()
	plugin, definition, key, err := s.activePluginSettingIn(services.Snapshot, params.ID, params.Fingerprint, params.Key)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if definition.Type == pluginpkg.SettingTypeSecret {
		return s.pluginSecretSetting(req, services, plugin, definition, key, nil)
	}
	scope := PluginValueScope(definition.Scope)
	document, err := services.Settings.Read(context.Background(), plugin.SubjectID, pluginsettings.Scope(scope))
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	value, ok := document.Values[key]
	if !ok {
		value, err = json.Marshal(definition.Default)
		if err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("encode plugin setting default: %w", err))
		}
	}
	return s.writeResponse(req.ID, PluginSettingResult{ID: plugin.SubjectID, Key: key, Scope: scope, Value: value}, nil)
}

func (s *Server) handlePluginSettingSet(req Request) error {
	var params PluginSettingSetParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer release()
	plugin, definition, key, err := s.activePluginSettingIn(services.Snapshot, params.ID, params.Fingerprint, params.Key)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := pluginpkg.ValidateSettingValue(definition, params.Value); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	patch := make(map[string]json.RawMessage, len(params.Patch))
	if len(params.Patch) > 255 {
		return s.writeResponse(req.ID, nil, errors.New("plugin setting patch exceeds 255 additional keys"))
	}
	for patchKey, value := range params.Patch {
		_, patchDefinition, localKey, err := s.activePluginSettingIn(services.Snapshot, params.ID, params.Fingerprint, patchKey)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		if localKey == key || patch[localKey] != nil {
			return s.writeResponse(req.ID, nil, errors.New("plugin setting patch contains a duplicate key"))
		}
		if patchDefinition.Scope != definition.Scope || patchDefinition.Type == pluginpkg.SettingTypeSecret || definition.Type == pluginpkg.SettingTypeSecret {
			return s.writeResponse(req.ID, nil, errors.New("plugin setting patch must contain non-secret keys in one scope"))
		}
		if err := pluginpkg.ValidateSettingValue(patchDefinition, value); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		patch[localKey] = value
	}
	if definition.Type == pluginpkg.SettingTypeSecret {
		var value string
		_ = json.Unmarshal(params.Value, &value)
		return s.pluginSecretSetting(req, services, plugin, definition, key, &value)
	}
	scope := PluginValueScope(definition.Scope)
	document, err := services.Settings.Update(context.Background(), plugin.SubjectID, pluginsettings.Scope(scope), plugin.Fingerprint, func(values map[string]json.RawMessage) error {
		values[key] = append(json.RawMessage(nil), params.Value...)
		for patchKey, value := range patch {
			values[patchKey] = append(json.RawMessage(nil), value...)
		}
		// Validate every declared value in this independently committed scope.
		// Removed declarations remain stored for downgrade compatibility, but
		// are never exposed as active settings.
		for qualified, candidateDefinition := range plugin.Settings {
			if candidateDefinition.Scope != definition.Scope || candidateDefinition.Type == pluginpkg.SettingTypeSecret {
				continue
			}
			candidateKey := strings.TrimPrefix(qualified, plugin.ID+".")
			candidate, exists := values[candidateKey]
			if !exists {
				var err error
				candidate, err = json.Marshal(candidateDefinition.Default)
				if err != nil {
					return err
				}
			}
			if err := pluginpkg.ValidateSettingValue(candidateDefinition, candidate); err != nil {
				return fmt.Errorf("setting %q: %w", candidateKey, err)
			}
		}
		return nil
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, PluginSettingResult{ID: plugin.SubjectID, Key: key, Scope: scope, Value: document.Values[key]}, nil)
}

func (s *Server) handlePluginRegistryIntrospect(req Request) error {
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	snapshot, err := s.rt.PluginServiceRegistrySnapshot()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, snapshot, nil)
}

// PluginExecutionsResult is the live execution table: open tool/capability
// executions across current and retained generations, with owning plugin and
// latest self-reported progress.
type PluginExecutionsResult struct {
	Executions []runtime.PluginExecutionSnapshot `json:"executions"`
}

func (s *Server) handlePluginExecutionsList(req Request) error {
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	executions := s.rt.PluginExecutionSnapshots()
	if executions == nil {
		executions = []runtime.PluginExecutionSnapshot{}
	}
	return s.writeResponse(req.ID, PluginExecutionsResult{Executions: executions}, nil)
}

func (s *Server) handlePluginDiagnosticsList(req Request) error {
	var params PluginDiagnosticsParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer release()
	params.ID, params.Fingerprint = strings.TrimSpace(params.ID), strings.TrimSpace(params.Fingerprint)
	var pluginID string
	for _, module := range services.Snapshot.NativeRendererModules() {
		if module.SubjectID != params.ID {
			continue
		}
		if params.Fingerprint != module.Fingerprint {
			return s.writeResponse(req.ID, nil, errors.New("plugin generation is no longer active"))
		}
		pluginID = module.ID
		break
	}
	if pluginID == "" {
		plugin, err := s.requireActiveDesktopPluginIn(services.Snapshot, params.ID, params.Fingerprint)
		if err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
		pluginID = plugin.ID
	}
	host := services.Snapshot.PluginHost
	diagnostics := host.ContributionDiagnostics(pluginID)
	result := make([]PluginContributionDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, PluginContributionDiagnostic{
			Contribution: diagnostic.Contribution,
			Message:      diagnostic.Message,
		})
	}
	result = append(result, pluginRendererDiagnostics(host, pluginID)...)
	return s.writeResponse(req.ID, PluginDiagnosticsResult{ID: params.ID, Diagnostics: result}, nil)
}

func pluginRendererDiagnostics(host *pluginhost.Host, pluginID string) []PluginContributionDiagnostic {
	registry := host.ServiceRegistry()
	if registry == nil {
		return nil
	}
	composition := registry.Snapshot(0).Composition
	if composition == nil {
		return nil
	}
	var diagnostics []PluginContributionDiagnostic
	for _, module := range composition.Modules {
		if module.ID != pluginID || module.Renderer == nil {
			continue
		}
		for _, client := range module.Renderer.Clients {
			for _, phase := range []struct {
				name  string
				state *pluginhost.RendererPhaseSnapshot
			}{{"registration", client.Registration}, {"render", client.Render}, {"cleanup", client.Cleanup}} {
				if phase.state != nil && phase.state.Status == "failed" {
					diagnostics = append(diagnostics, PluginContributionDiagnostic{
						Contribution: "renderer/" + client.RendererID + "/" + phase.name,
						Message:      phase.state.Message,
					})
				}
			}
		}
	}
	return diagnostics
}

// handlePluginGenerationDiagnosticsList exposes retired plugin generation
// revocation reports: per-resource, per-phase outcomes with plugin and
// generation attribution, so failed revocations stay publicly visible.
func (s *Server) handlePluginGenerationDiagnosticsList(req Request) error {
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	var params PluginGenerationDiagnosticsParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	reports, err := s.rt.PluginGenerationRevocations(params.Limit)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if reports == nil {
		reports = []runtime.GenerationRevocationReport{}
	}
	return s.writeResponse(req.ID, PluginGenerationDiagnosticsResult{Reports: reports}, nil)
}

func (s *Server) handlePluginStorageGet(req Request) error {
	var params PluginStorageGetParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer release()
	plugin, err := s.requireActiveDesktopPluginIn(services.Snapshot, params.ID, params.Fingerprint)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	scope, err := pluginStorageScope(params.Scope)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	document, err := services.Settings.ReadState(context.Background(), plugin.SubjectID, scope)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var value *string
	if stored, ok := document.Values[params.Key]; ok {
		value = &stored
	}
	return s.writeResponse(req.ID, PluginStorageResult{ID: plugin.SubjectID, Scope: params.Scope, Key: params.Key, Value: value}, nil)
}

func (s *Server) handlePluginStorageSet(req Request) error {
	var params PluginStorageSetParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer release()
	plugin, err := s.requireActiveDesktopPluginIn(services.Snapshot, params.ID, params.Fingerprint)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := pluginsettings.ValidateStateKey(params.Key); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	scope, err := pluginStorageScope(params.Scope)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	document, err := services.Settings.UpdateState(context.Background(), plugin.SubjectID, scope, func(values map[string]string) error {
		values[params.Key] = params.Value
		return nil
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	value := document.Values[params.Key]
	return s.writeResponse(req.ID, PluginStorageResult{ID: plugin.SubjectID, Scope: params.Scope, Key: params.Key, Value: &value}, nil)
}

func (s *Server) activePluginSettingIn(snapshot runtime.ExtensionSnapshot, id, fingerprint, key string) (pluginpkg.Plugin, pluginpkg.SettingDefinition, string, error) {
	plugin, err := s.requireActiveDesktopPluginIn(snapshot, id, fingerprint)
	if err != nil {
		return pluginpkg.Plugin{}, pluginpkg.SettingDefinition{}, "", err
	}
	key = strings.TrimSpace(key)
	// Inventory descriptors expose manifest setting IDs in their qualified
	// form (for example, "example.display_mode"), while
	// plugin host calls use the manifest-local key. Accept both at this desktop
	// boundary and persist only the local key.
	key = strings.TrimPrefix(key, plugin.ID+".")
	definition, ok := plugin.Settings[plugin.ID+"."+key]
	if !ok {
		return pluginpkg.Plugin{}, pluginpkg.SettingDefinition{}, "", fmt.Errorf("plugin %q does not own setting %q", id, key)
	}
	return plugin, definition, key, nil
}

// Admission and dispatch must use the same generation snapshot. Reading the
// mutable Session.PluginHost again can route old-fingerprint input to new code.
func (s *Server) requireActiveDesktopPluginHost(id, fingerprint string) (pluginpkg.Plugin, *pluginhost.Host, error) {
	snapshot := s.rt.ExtensionSnapshot()
	plugin, err := s.requireActiveDesktopPluginIn(snapshot, id, fingerprint)
	return plugin, snapshot.PluginHost, err
}

func (s *Server) requireActiveDesktopPluginIn(snapshot runtime.ExtensionSnapshot, id, fingerprint string) (pluginpkg.Plugin, error) {
	id, fingerprint = strings.TrimSpace(id), strings.TrimSpace(fingerprint)
	if id == "" || fingerprint == "" {
		return pluginpkg.Plugin{}, errors.New("plugin id and fingerprint are required")
	}
	for _, plugin := range snapshot.Plugins {
		if plugin.SubjectID != id {
			continue
		}
		if plugin.Fingerprint != fingerprint {
			return pluginpkg.Plugin{}, errors.New("plugin generation is no longer active")
		}
		settings := extensions.Settings{}
		if snapshot.ExtensionSettings != nil {
			settings = *snapshot.ExtensionSettings

		}
		approval, state, _, enabled := pluginPackageInventoryState(settings, plugin)
		if !enabled || (approval != ExtensionApprovalGranted && approval != ExtensionApprovalOfficial) || (state != ExtensionStateGranted && state != ExtensionStateActive) {
			return pluginpkg.Plugin{}, errors.New("desktop plugin is not approved and enabled")
		}
		if err := desktopPluginRuntimeError(snapshot.PluginHost, plugin.ID); err != nil {
			return pluginpkg.Plugin{}, err
		}
		return plugin, nil
	}
	return pluginpkg.Plugin{}, fmt.Errorf("plugin %q is not available in this workspace", id)
}

func desktopPluginRuntimeError(host *pluginhost.Host, id string) error {
	if host == nil {
		return nil
	}
	for _, status := range host.Statuses() {
		if status.ID == id && (status.State == pluginhost.StateFailed || status.State == pluginhost.StateStopped) {
			return errors.New("desktop plugin runtime is no longer active")
		}
	}
	return nil
}

func pluginStorageScope(scope PluginValueScope) (pluginsettings.Scope, error) {
	switch scope {
	case PluginValueScopeUser, PluginValueScopeWorkspace:
		return pluginsettings.Scope(scope), nil
	default:
		return "", fmt.Errorf("unsupported plugin storage scope %q", scope)
	}
}

// Secret settings share the existing fingerprint-gated UI route but never enter
// pluginsettings files, broadcasts or read responses. Empty input removes a token.
func (s *Server) pluginSecretSetting(req Request, services *runtime.StateServices, plugin pluginpkg.Plugin, definition pluginpkg.SettingDefinition, key string, value *string) error {
	binding, err := plugincredentials.BindingFor(s.rt.WuuHome, s.rt.RootDir, plugin.SubjectID, key, definition.CredentialOrigins)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	store := services.Credentials
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	if value != nil {
		if err := plugincredentials.Set(ctx, store, binding, *value); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	configured, err := plugincredentials.Configured(ctx, store, binding)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	return s.writeResponse(req.ID, PluginSettingResult{ID: plugin.SubjectID, Key: key, Scope: PluginValueScopeUser, Value: json.RawMessage(`""`), Configured: &configured}, nil)
}
