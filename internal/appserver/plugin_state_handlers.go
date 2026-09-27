package appserver

import (
	"bytes"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func (s *Server) handlePluginSettingGet(req Request) error {
	var params PluginSettingGetParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	plugin, definition, key, err := s.activePluginSetting(params.ID, params.Fingerprint, params.Key)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	scope := PluginValueScope(definition.Scope)
	document, err := pluginsettings.Read(s.rt.WuuHome, s.rt.RootDir, plugin.SubjectID, pluginsettings.Scope(scope))
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
	plugin, definition, key, err := s.activePluginSetting(params.ID, params.Fingerprint, params.Key)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if err := validatePluginSettingValue(definition, params.Value); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	scope := PluginValueScope(definition.Scope)
	document, err := pluginsettings.Update(s.rt.WuuHome, s.rt.RootDir, plugin.SubjectID, pluginsettings.Scope(scope), plugin.Fingerprint, func(values map[string]json.RawMessage) error {
		values[key] = append(json.RawMessage(nil), params.Value...)
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
// executions with their owning plugin and latest self-reported progress.
type PluginExecutionsResult struct {
	Executions []pluginhost.ExecutionSnapshot `json:"executions"`
}

func (s *Server) handlePluginExecutionsList(req Request) error {
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("runtime is not initialized"))
	}
	executions := s.rt.PluginExecutionSnapshots()
	if executions == nil {
		executions = []pluginhost.ExecutionSnapshot{}
	}
	return s.writeResponse(req.ID, PluginExecutionsResult{Executions: executions}, nil)
}

func (s *Server) handlePluginDiagnosticsList(req Request) error {
	var params PluginDiagnosticsParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	plugin, err := s.requireActiveDesktopPlugin(params.ID, params.Fingerprint)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	diagnostics := s.rt.PluginHost.ContributionDiagnostics(plugin.ID)
	result := make([]PluginContributionDiagnostic, 0, len(diagnostics))
	for _, diagnostic := range diagnostics {
		result = append(result, PluginContributionDiagnostic{
			Contribution: diagnostic.Contribution,
			Message:      diagnostic.Message,
		})
	}
	return s.writeResponse(req.ID, PluginDiagnosticsResult{ID: plugin.SubjectID, Diagnostics: result}, nil)
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
	plugin, err := s.requireActiveDesktopPlugin(params.ID, params.Fingerprint)
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
	document, err := pluginsettings.ReadState(s.rt.WuuHome, s.rt.RootDir, plugin.SubjectID, scope)
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
	plugin, err := s.requireActiveDesktopPlugin(params.ID, params.Fingerprint)
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
	document, err := pluginsettings.UpdateState(s.rt.WuuHome, s.rt.RootDir, plugin.SubjectID, scope, func(values map[string]string) error {
		values[params.Key] = params.Value
		return nil
	})
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	value := document.Values[params.Key]
	return s.writeResponse(req.ID, PluginStorageResult{ID: plugin.SubjectID, Scope: params.Scope, Key: params.Key, Value: &value}, nil)
}

func (s *Server) activePluginSetting(id, fingerprint, key string) (pluginpkg.Plugin, pluginpkg.SettingDefinition, string, error) {
	plugin, err := s.requireActiveDesktopPlugin(id, fingerprint)
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

func (s *Server) requireActiveDesktopPlugin(id, fingerprint string) (pluginpkg.Plugin, error) {
	id, fingerprint = strings.TrimSpace(id), strings.TrimSpace(fingerprint)
	if id == "" || fingerprint == "" {
		return pluginpkg.Plugin{}, errors.New("plugin id and fingerprint are required")
	}
	for _, plugin := range s.rt.Plugins {
		if plugin.SubjectID != id {
			continue
		}
		if plugin.Fingerprint != fingerprint {
			return pluginpkg.Plugin{}, errors.New("plugin generation is no longer active")
		}
		settings := extensions.Settings{}
		if s.rt.ExtensionSettings != nil {
			settings = *s.rt.ExtensionSettings
		} else if cfg := s.currentExtensionConfig(); cfg.Extensions != nil {
			settings = *cfg.Extensions
		}
		approval, state, _, enabled := pluginPackageInventoryState(settings, plugin)
		if !enabled || (approval != ExtensionApprovalGranted && approval != ExtensionApprovalOfficial) || (state != ExtensionStateGranted && state != ExtensionStateActive) {
			return pluginpkg.Plugin{}, errors.New("desktop plugin is not approved and enabled")
		}
		return plugin, nil
	}
	return pluginpkg.Plugin{}, fmt.Errorf("plugin %q is not available in this workspace", id)
}

func pluginStorageScope(scope PluginValueScope) (pluginsettings.Scope, error) {
	switch scope {
	case PluginValueScopeUser, PluginValueScopeWorkspace:
		return pluginsettings.Scope(scope), nil
	default:
		return "", fmt.Errorf("unsupported plugin storage scope %q", scope)
	}
}

func validatePluginSettingValue(definition pluginpkg.SettingDefinition, raw json.RawMessage) error {
	if len(raw) == 0 || len(raw) > 64<<10 || !json.Valid(raw) {
		return errors.New("plugin setting value must be valid JSON within 65536 bytes")
	}
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.UseNumber()
	var value any
	if err := decoder.Decode(&value); err != nil {
		return errors.New("plugin setting value is invalid")
	}
	valid := false
	switch definition.Type {
	case pluginpkg.SettingTypeBoolean:
		_, valid = value.(bool)
	case pluginpkg.SettingTypeString:
		_, valid = value.(string)
	case pluginpkg.SettingTypeNumber:
		_, valid = value.(json.Number)
	case pluginpkg.SettingTypeEnum:
		text, ok := value.(string)
		for _, candidate := range definition.Enum {
			valid = valid || (ok && text == candidate)
		}
	}
	if !valid {
		return fmt.Errorf("plugin setting value does not match declared type %q", definition.Type)
	}
	return nil
}
