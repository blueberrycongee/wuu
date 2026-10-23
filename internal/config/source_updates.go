package config

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/grokbuildspec"
)

// Source transformations are pure. Callers commit their returned bytes with
// the selected configuration provider's revision CAS; they do not write files.
func UpdateProviderModelSource(data []byte, providerName, newModel string) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	providers, ok := raw["providers"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("providers section not found")
	}
	provider, ok := providers[providerName].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("provider %q not found", providerName)
	}
	provider["model"] = newModel

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return append(out, '\n'), nil
}

func AddProviderIfMissingSource(data []byte, providerName string, provider ProviderConfig) ([]byte, error) {
	providerName = strings.TrimSpace(providerName)
	if providerName == "" {
		return nil, errors.New("provider name is required")
	}
	var err error
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}
	var providers map[string]json.RawMessage
	if err := json.Unmarshal(raw["providers"], &providers); err != nil || providers == nil {
		return nil, errors.New("providers section not found")
	}
	if _, exists := providers[providerName]; exists {
		return data, nil
	}
	providers[providerName], err = json.Marshal(provider)
	if err != nil {
		return nil, fmt.Errorf("marshal provider: %w", err)
	}
	raw["providers"], err = json.Marshal(providers)
	if err != nil {
		return nil, fmt.Errorf("marshal providers: %w", err)
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return append(out, '\n'), nil
}

func UseCodexCredentialsSource(data []byte, providerName string) ([]byte, error) {
	var err error
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}
	var providers map[string]map[string]json.RawMessage
	if err := json.Unmarshal(raw["providers"], &providers); err != nil {
		return nil, err
	}
	provider, exists := providers[providerName]
	if !exists {
		return nil, fmt.Errorf("provider %q not found", providerName)
	}
	var providerType string
	if err := json.Unmarshal(provider["type"], &providerType); err != nil {
		return nil, err
	}
	if !isCodexSubscriptionProvider(providerType) {
		return nil, errors.New("local Codex login requires a Codex subscription provider")
	}
	provider["reuse_codex_credentials"] = json.RawMessage("true")
	delete(provider, "api_key")
	delete(provider, "api_key_env")
	raw["providers"], err = json.Marshal(providers)
	if err != nil {
		return nil, err
	}
	data, err = json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, err
	}
	return append(data, '\n'), nil
}

func UpdateAdvancedRuntimeSource(data []byte, providerName string, update AdvancedRuntimeUpdate) ([]byte, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	agent, _ := raw["agent"].(map[string]any)
	if agent == nil {
		agent = make(map[string]any)
		raw["agent"] = agent
	}
	setOptionalInt(agent, "max_steps", update.MaxSteps)
	setOptionalInt(agent, "max_context_tokens", update.MaxContextTokens)
	setOptionalFloat(agent, "temperature", update.Temperature, 0)
	setOptionalFloat(agent, "compact_threshold_pct", update.CompactThresholdPct, 0)
	setOptionalInt(agent, "compact_keep_recent_tokens", update.CompactKeepRecentTokens)
	setOptionalBool(agent, "disable_auto_compact", update.DisableAutoCompact)
	if update.ProjectModels != nil {
		if *update.ProjectModels == (ProjectModelsConfig{}) {
			delete(agent, "project_models")
		} else {
			agent["project_models"] = *update.ProjectModels
		}
	}
	if update.ModelAliases != nil {
		aliases := make(map[string]any, len(update.ModelAliases))
		for rawName, alias := range update.ModelAliases {
			name := normalizeModelAliasName(rawName)
			if name == "" || alias == nil {
				continue
			}
			entry := map[string]any{
				"provider": strings.TrimSpace(alias.Provider),
				"model":    strings.TrimSpace(alias.Model),
			}
			if effort := strings.TrimSpace(alias.Effort); effort != "" {
				entry["effort"] = effort
			}
			if variant := strings.TrimSpace(alias.Variant); variant != "" {
				entry["variant"] = variant
			}
			aliases[name] = entry
		}
		if len(aliases) == 0 {
			delete(agent, "model_aliases")
		} else {
			agent["model_aliases"] = aliases
		}
	}
	if update.VerificationModel != nil {
		roles, _ := agent["model_roles"].(map[string]any)
		if roles == nil {
			roles = make(map[string]any)
		}
		setRole := func(name string, selection *ModelRoleConfig) {
			if selection == nil {
				return
			}
			provider := strings.TrimSpace(selection.Provider)
			model := strings.TrimSpace(selection.Model)
			effort := strings.TrimSpace(selection.Effort)
			variant := strings.TrimSpace(selection.Variant)
			if provider == "" && model == "" && effort == "" && variant == "" {
				delete(roles, name)
				return
			}
			entry := make(map[string]any)
			if provider != "" {
				entry["provider"] = provider
			}
			if model != "" {
				entry["model"] = model
			}
			if effort != "" {
				entry["effort"] = effort
			}
			if variant != "" {
				entry["variant"] = variant
			}
			roles[name] = entry
		}
		setRole("verification", update.VerificationModel)
		if len(roles) == 0 {
			delete(agent, "model_roles")
		} else {
			agent["model_roles"] = roles
		}
	}
	if len(agent) == 0 {
		delete(raw, "agent")
	}

	if update.ProviderContextWindow != nil {
		providers, _ := raw["providers"].(map[string]any)
		if providers == nil {
			return nil, errors.New("providers is required")
		}
		name := strings.TrimSpace(providerName)
		if name == "" {
			name, _ = raw["default_provider"].(string)
			name = strings.TrimSpace(name)
		}
		if name == "" {
			return nil, errors.New("provider is required")
		}
		provider, _ := providers[name].(map[string]any)
		if provider == nil {
			return nil, fmt.Errorf("provider %q not found", name)
		}
		setOptionalInt(provider, "context_window", update.ProviderContextWindow)
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(out, &cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func UpdateGeneralSettingsSource(data []byte, update GeneralSettingsUpdate) ([]byte, error) {
	if update.ExecutionEnvironments != nil {
		if err := update.ExecutionEnvironments.Validate(); err != nil {
			return nil, err
		}
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, err
	}

	if update.ExecutionEnvironments != nil {
		raw["execution_environments"] = update.ExecutionEnvironments
	}
	if update.PTC != nil {
		delete(raw, "code_mode")
		ptc, _ := raw["ptc"].(map[string]any)
		if ptc == nil {
			ptc = make(map[string]any)
			raw["ptc"] = ptc
		}
		ptc["enabled"] = update.PTC.Enabled
		delete(ptc, "families")
	}

	if update.GitAttributionEnabled != nil {
		agent, _ := raw["agent"].(map[string]any)
		if agent == nil {
			agent = make(map[string]any)
			raw["agent"] = agent
		}
		if *update.GitAttributionEnabled {
			delete(agent, "git_attribution_enabled")
		} else {
			agent["git_attribution_enabled"] = false
		}
		if len(agent) == 0 {
			delete(raw, "agent")
		}
	}

	if len(update.MCPEnabledToggles) > 0 {
		mcpServers, _ := raw["mcp_servers"].(map[string]any)
		if mcpServers == nil {
			mcpServers = make(map[string]any)
			raw["mcp_servers"] = mcpServers
		}
		for name, enabled := range update.MCPEnabledToggles {
			if enabled == nil {
				continue
			}
			server, _ := mcpServers[name].(map[string]any)
			if server == nil {
				server = make(map[string]any)
				mcpServers[name] = server
			}
			if *enabled {
				delete(server, "enabled")
			} else {
				server["enabled"] = false
			}
			// An entry with nothing left is not a definition; keeping it would
			// shadow a same-named server defined elsewhere.
			if len(server) == 0 {
				delete(mcpServers, name)
			}
		}
		if len(mcpServers) == 0 {
			delete(raw, "mcp_servers")
		}
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(out, &cfg); err != nil {
		return nil, err
	}
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, err
	}
	return append(out, '\n'), nil
}

func UpdateProviderSource(data []byte, providerName string, change ProviderChange) ([]byte, error) {
	if !change.Select && (change.Effort != nil || change.Variant != nil || change.PermissionMode != nil) {
		return nil, errors.New("agent selection fields require selecting the provider")
	}
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, fmt.Errorf("parse config: %w", err)
	}

	providers, ok := raw["providers"].(map[string]any)
	if !ok {
		return nil, fmt.Errorf("providers section not found")
	}
	newModel := change.Model
	baseURL := change.BaseURL
	provider, ok := providers[providerName].(map[string]any)
	if change.Create {
		if ok {
			return nil, fmt.Errorf("provider %q already exists", providerName)
		}
		if strings.TrimSpace(providerName) == "" {
			return nil, fmt.Errorf("provider name is required")
		}
		providerTypeValue := "openai-compatible"
		if requested := strings.ToLower(strings.TrimSpace(change.Type)); requested != "" {
			providerTypeValue = requested
		}
		if baseURL == nil || strings.TrimSpace(*baseURL) == "" {
			if !IsGrokBuildProvider(providerTypeValue) {
				return nil, fmt.Errorf("base_url is required")
			}
			value := grokbuildspec.DefaultBaseURL
			baseURL = &value
		}
		if IsGrokBuildProvider(providerTypeValue) {
			defaults := ApplyGrokBuildProviderDefaults(ProviderConfig{
				Type:    providerTypeValue,
				BaseURL: strings.TrimSpace(*baseURL),
				Model:   strings.TrimSpace(newModel),
			})
			encoded, marshalErr := json.Marshal(defaults)
			if marshalErr != nil {
				return nil, fmt.Errorf("marshal Grok Build provider defaults: %w", marshalErr)
			}
			if unmarshalErr := json.Unmarshal(encoded, &provider); unmarshalErr != nil {
				return nil, fmt.Errorf("prepare Grok Build provider defaults: %w", unmarshalErr)
			}
		} else {
			provider = map[string]any{
				"type":     providerTypeValue,
				"base_url": strings.TrimSpace(*baseURL),
			}
		}
		if IsXAISubscriptionProvider(providerTypeValue) {
			provider["wire_api"] = "responses"
		}
		if change.APIKey != nil && strings.TrimSpace(*change.APIKey) != "" {
			provider["api_key"] = strings.TrimSpace(*change.APIKey)
		}
		if change.AuthToken != nil && strings.TrimSpace(*change.AuthToken) != "" {
			provider["auth_token"] = strings.TrimSpace(*change.AuthToken)
		}
		providers[providerName] = provider
	} else if !ok {
		return nil, fmt.Errorf("provider %q not found", providerName)
	}
	if change.Select {
		raw["default_provider"] = providerName
	}
	provider["model"] = newModel
	for _, id := range change.RemovedModels {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		if id == strings.TrimSpace(newModel) {
			return nil, fmt.Errorf("select another model before removing %q", id)
		}
		model := providerModelEntry(provider, id)
		// Keep an override so catalog refreshes cannot resurrect the choice.
		model["disabled"] = true
	}
	for _, id := range change.AddedModels {
		id = strings.TrimSpace(id)
		if id == "" {
			continue
		}
		// An entry of its own keeps an ID the catalog does not list.
		delete(providerModelEntry(provider, id), "disabled")
	}
	if baseURL != nil {
		provider["base_url"] = strings.TrimSpace(*baseURL)
	}
	if apiKey := change.APIKey; apiKey != nil {
		if key := strings.TrimSpace(*apiKey); key != "" {
			provider["api_key"] = key
		}
		if strings.TrimSpace(*apiKey) == "" {
			delete(provider, "api_key")
		}
		delete(provider, "api_key_env")
	}
	if authToken := change.AuthToken; authToken != nil {
		if token := strings.TrimSpace(*authToken); token != "" {
			provider["auth_token"] = token
		}
		if strings.TrimSpace(*authToken) == "" {
			delete(provider, "auth_token")
		}
		delete(provider, "auth_token_env")
	}
	if effort := change.Effort; effort != nil {
		agent, _ := raw["agent"].(map[string]any)
		if agent == nil {
			agent = make(map[string]any)
			raw["agent"] = agent
		}
		if strings.TrimSpace(*effort) == "" {
			delete(agent, "effort")
		} else {
			agent["effort"] = strings.TrimSpace(*effort)
		}
	}
	if variant := change.Variant; variant != nil {
		agent, _ := raw["agent"].(map[string]any)
		if agent == nil {
			agent = make(map[string]any)
			raw["agent"] = agent
		}
		if strings.TrimSpace(*variant) == "" {
			delete(agent, "variant")
		} else {
			agent["variant"] = strings.TrimSpace(*variant)
		}
	}
	if change.ChatGPTModels != nil {
		provider["chatgpt_models"] = *change.ChatGPTModels
	}
	if change.ChatGPTAccountID != nil {
		provider["chatgpt_account_id"] = strings.TrimSpace(*change.ChatGPTAccountID)
	}
	if change.ReuseCodexCredentials != nil {
		provider["reuse_codex_credentials"] = *change.ReuseCodexCredentials
	}
	if permissionMode := change.PermissionMode; permissionMode != nil {
		mode := NormalizePermissionMode(*permissionMode)
		if err := validatePermissionMode(mode); err != nil {
			return nil, err
		}
		agent, _ := raw["agent"].(map[string]any)
		if agent == nil {
			agent = make(map[string]any)
			raw["agent"] = agent
		}
		agent["permission_mode"] = mode
		delete(agent, "permission_profile")
		delete(agent, "approval_policy")
		delete(agent, "approvals_reviewer")
		delete(agent, "tool_policy")
		delete(agent, "permission_rules")
	}
	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, fmt.Errorf("marshal config: %w", err)
	}
	return append(out, '\n'), nil
}

func RemoveProviderSource(data []byte, providerName, fallbackName, fallbackModel string) ([]byte, string, error) {
	var newDefault string
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, "", fmt.Errorf("parse config: %w", err)
	}

	providers, ok := raw["providers"].(map[string]any)
	if !ok {
		return nil, "", fmt.Errorf("providers section not found")
	}
	if _, exists := providers[providerName]; !exists {
		return nil, "", fmt.Errorf("provider %q not found", providerName)
	}

	currentDefault, _ := raw["default_provider"].(string)
	removedWasDefault := currentDefault == providerName

	delete(providers, providerName)

	if removedWasDefault {
		if strings.TrimSpace(fallbackName) == "" {
			// No fallback supplied — pick any remaining provider to keep
			// the runtime consistent. Callers should normally pick a
			// sensible default, but this fallback keeps the config
			// usable even if the caller skips that step.
			for name := range providers {
				fallbackName = name
				break
			}
		}
		if strings.TrimSpace(fallbackName) == "" {
			// No providers left to fall back to; refuse the deletion so
			// the runtime never ends up with an empty default_provider.
			return nil, "", errors.New("cannot remove the last configured provider")
		}
		if _, ok := providers[fallbackName].(map[string]any); !ok {
			return nil, "", fmt.Errorf("fallback provider %q not found", fallbackName)
		}
		raw["default_provider"] = fallbackName
		if strings.TrimSpace(fallbackModel) != "" {
			fb, _ := providers[fallbackName].(map[string]any)
			if fb != nil {
				fb["model"] = strings.TrimSpace(fallbackModel)
			}
		}
		newDefault = fallbackName
	}

	// Selections that used the removed provider must inherit completely;
	// keeping its model or variant would route them through an unrelated
	// provider. Aliases cannot inherit, so remove their entire entry too.
	if agent, ok := raw["agent"].(map[string]any); ok {
		for _, section := range []string{"model_roles", "project_models", "model_aliases"} {
			selections, _ := agent[section].(map[string]any)
			for key, rawSelection := range selections {
				selection, _ := rawSelection.(map[string]any)
				name, _ := selection["provider"].(string)
				name = strings.TrimSpace(name)
				if name == providerName || (removedWasDefault && section == "model_roles" && name == "") {
					delete(selections, key)
				}
			}
		}
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, "", fmt.Errorf("marshal config: %w", err)
	}
	return append(out, '\n'), newDefault, nil
}

func UpdateExtensionSettingsSource(data []byte, update func(*extensions.Settings) error) ([]byte, extensions.Settings, error) {
	var raw map[string]any
	if err := json.Unmarshal(data, &raw); err != nil {
		return nil, extensions.Settings{}, err
	}
	settings := extensions.Settings{}
	if extensionRaw, ok := raw["extensions"]; ok {
		encoded, err := json.Marshal(extensionRaw)
		if err != nil {
			return nil, extensions.Settings{}, fmt.Errorf("marshal current extension settings: %w", err)
		}
		if err := json.Unmarshal(encoded, &settings); err != nil {
			return nil, extensions.Settings{}, fmt.Errorf("decode current extension settings: %w", err)
		}
	}
	if update == nil {
		return nil, extensions.Settings{}, errors.New("extension settings update is required")
	}
	if err := update(&settings); err != nil {
		return nil, extensions.Settings{}, err
	}
	encoded, err := json.Marshal(settings)
	if err != nil {
		return nil, extensions.Settings{}, fmt.Errorf("marshal extension settings: %w", err)
	}
	var extensionRaw map[string]any
	if err := json.Unmarshal(encoded, &extensionRaw); err != nil {
		return nil, extensions.Settings{}, fmt.Errorf("decode extension settings: %w", err)
	}
	if len(extensionRaw) == 0 {
		delete(raw, "extensions")
	} else {
		raw["extensions"] = extensionRaw
	}

	out, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return nil, extensions.Settings{}, fmt.Errorf("marshal config: %w", err)
	}
	var cfg Config
	if err := json.Unmarshal(out, &cfg); err != nil {
		return nil, extensions.Settings{}, err
	}
	applyDefaults(&cfg)
	if err := cfg.Validate(); err != nil {
		return nil, extensions.Settings{}, err
	}
	return append(out, '\n'), settings, nil
}
