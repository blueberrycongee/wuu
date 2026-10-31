package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"fmt"
	"sort"
	"strings"

	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/pluginsettings"
)

type pluginSettingsOperationKey struct{}

func settingsRevision(value any) string {
	raw, _ := json.Marshal(value)
	digest := sha256.Sum256(raw)
	return hex.EncodeToString(digest[:])
}

// A candidate reads each independently committed scope once, then validates all
// declared values before publishing any. No schema-specific state-store method
// is required, so external state.plugin-settings v1 providers remain compatible.
func (s *pluginHostServices) readSettingsCandidate(ctx context.Context) (map[string]json.RawMessage, map[string]string, error) {
	documents := map[pluginsettings.Scope]pluginsettings.Document{}
	values := map[string]json.RawMessage{}
	byScope := map[string]map[string]json.RawMessage{}
	keys := make([]string, 0, len(s.settings))
	for key := range s.settings {
		keys = append(keys, key)
	}
	sort.Strings(keys)
	for _, qualified := range keys {
		definition := s.settings[qualified]
		key := strings.TrimPrefix(qualified, s.pluginID+".")
		scope := pluginsettings.Scope(definition.Scope)
		value := json.RawMessage(`""`)
		if definition.Type != pluginpkg.SettingTypeSecret {
			document, ok := documents[scope]
			if !ok {
				var err error
				document, err = s.settingsClient.Read(ctx, s.storageSubject(), scope)
				if err != nil {
					return nil, nil, err
				}
				documents[scope] = document
			}
			var err error
			value, ok = document.Values[key]
			if !ok {
				value, err = json.Marshal(definition.Default)
			}
			if err != nil {
				return nil, nil, fmt.Errorf("encode setting %q default: %w", key, err)
			}
			if err := pluginpkg.ValidateSettingValue(definition, value); err != nil {
				return nil, nil, serviceError("settings_invalid", fmt.Sprintf("persisted setting %q: %s", key, err))
			}
			// Canonical JSON avoids false revisions from whitespace-only edits.
			var decoded any
			decoder := json.NewDecoder(bytes.NewReader(value))
			decoder.UseNumber()
			_ = decoder.Decode(&decoded)
			value, _ = json.Marshal(decoded)
		}
		values[key] = append(json.RawMessage(nil), value...)
		if byScope[string(scope)] == nil {
			byScope[string(scope)] = map[string]json.RawMessage{}
		}
		byScope[string(scope)][key] = values[key]
	}
	revisions := map[string]string{}
	for scope, entries := range byScope {
		revisions[scope] = settingsRevision(entries)
	}
	return values, revisions, nil
}

func (s *pluginHostServices) prepareSettings(ctx context.Context) error {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	if s.settingsBaseline != nil {
		return nil
	}
	values, _, err := s.readSettingsCandidate(ctx)
	if err != nil {
		return err
	}
	s.settingsBaseline = values
	return nil
}

func (s *pluginHostServices) settingsSnapshot(ctx context.Context, scope string) (pluginhost.SettingsListResult, error) {
	if scope != "" && scope != "user" && scope != "workspace" {
		return pluginhost.SettingsListResult{}, serviceError("invalid_params", "settings scope must be user or workspace")
	}
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	operation, _ := ctx.Value(pluginSettingsOperationKey{}).(*pluginhost.ExecutionScope)
	if operation != nil {
		if err := operation.Context.Err(); err != nil {
			return pluginhost.SettingsListResult{}, err
		}
	}
	candidate, revisions, err := s.readSettingsCandidate(ctx)
	if err != nil {
		return pluginhost.SettingsListResult{}, err
	}
	if err := ctx.Err(); err != nil {
		return pluginhost.SettingsListResult{}, err
	}
	if s.settingsBaseline == nil {
		s.settingsBaseline = candidate
	}
	operationValues := candidate
	if operation != nil {
		if err := operation.Context.Err(); err != nil {
			return pluginhost.SettingsListResult{}, err
		}
		if pinned := s.settingsOperations[operation.ID]; pinned != nil {
			operationValues = pinned
		} else {
			if s.settingsOperations == nil {
				s.settingsOperations = map[string]map[string]json.RawMessage{}
				s.settingsOperationStops = map[string]func() bool{}
			}
			s.settingsOperations[operation.ID] = candidate
			id := operation.ID
			s.settingsOperationStops[id] = context.AfterFunc(operation.Context, func() {
				s.settingsMu.Lock()
				delete(s.settingsOperations, id)
				delete(s.settingsOperationStops, id)
				s.settingsMu.Unlock()
			})
		}
	}
	result := pluginhost.SettingsListResult{Entries: map[string]json.RawMessage{}, ScopeRevisions: map[string]string{}, RestartRequired: []string{}}
	for key, configured := range candidate {
		definition := s.settings[s.pluginID+"."+key]
		if scope != "" && string(definition.Scope) != scope {
			continue
		}
		effective := configured
		switch definition.Apply {
		case pluginpkg.SettingApplyRestart:
			effective = s.settingsBaseline[key]
			if !bytes.Equal(effective, configured) {
				result.RestartRequired = append(result.RestartRequired, key)
			}
		case pluginpkg.SettingApplyNextOperation:
			effective = operationValues[key]
		}
		result.Entries[key] = append(json.RawMessage(nil), effective...)
		result.ScopeRevisions[string(definition.Scope)] = revisions[string(definition.Scope)]
	}
	sort.Strings(result.RestartRequired)
	result.Revision = settingsRevision(result)
	return result, nil
}

func (s *pluginHostServices) closeSettings() {
	s.settingsMu.Lock()
	defer s.settingsMu.Unlock()
	for _, stop := range s.settingsOperationStops {
		stop()
	}
	s.settingsOperationStops = nil
	s.settingsOperations = nil
	s.settingsBaseline = nil
}
