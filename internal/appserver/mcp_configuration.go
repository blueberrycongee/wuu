package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// MCP definitions use the selected configuration provider, including its revision
// fence. Plugin-owned definitions remain in their package and are never copied.
func (s *Server) mcpConfiguration(ctx context.Context) (config.SourceView, map[string]config.MCPServerConfig, error) {
	if s.rt == nil {
		return config.SourceView{}, nil, errors.New("runtime is not initialized")
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return config.SourceView{}, nil, err
	}
	defer release()
	view, err := services.Config.Read(ctx)
	if err != nil {
		return view, nil, err
	}
	source, err := editableConfigSource(view)
	if err != nil {
		return view, nil, err
	}
	var document struct {
		Servers map[string]config.MCPServerConfig `json:"mcp_servers"`
	}
	if err := json.Unmarshal(source, &document); err != nil {
		return view, nil, err
	}
	return view, document.Servers, nil
}

const mcpConnectorConfigurationUnavailable = "Connector preferences are unavailable in this host until domain-scoped configuration is supported."

// Active ownership is resolved from the admitted package, not from a namespaced
// configuration key. A disabled or replaced package cannot be enabled here.
func (s *Server) activePluginMCPDefinition(snapshot runtime.ExtensionSnapshot, name string) (config.MCPServerConfig, bool) {
	if s.rt == nil || s.rt.SafeMode {
		return config.MCPServerConfig{}, false
	}
	for _, installed := range snapshot.Plugins {
		for localName := range installed.MCPServers {
			if runtime.PluginMCPServerName(installed.ID, localName) != name {
				continue
			}
			for _, active := range snapshot.ActivePlugins {
				if active.ID == installed.ID && active.Source == installed.Source && active.Fingerprint == installed.Fingerprint {
					definition, exists := active.MCPServers[localName]
					return definition, exists
				}
			}
		}
	}
	return config.MCPServerConfig{}, false
}

// Domain-scoped hosts must not read or mutate the shared Coding connector's
// credentials, including cleanup endpoints. Existing non-connector plugin
// services keep their own contracts; unknown names fail closed after removal.
func (s *Server) checkMCPConnectorDomain(name string) error {
	if s.rt == nil || !s.rt.UserBinding().IsUser() || !runtime.IsPluginMCPServerName(name) {
		return nil
	}
	name = strings.TrimSpace(name)
	for _, item := range s.rt.ExtensionSnapshot().Plugins {
		for localName, definition := range item.MCPServers {
			if runtime.PluginMCPServerName(item.ID, localName) != name {
				continue
			}
			if definition.Connector != nil {
				return errors.New(mcpConnectorConfigurationUnavailable)
			}
			return nil
		}
	}
	return errors.New(mcpConnectorConfigurationUnavailable)
}

// Connection and OAuth entry points re-check admitted ownership. Cleanup reads,
// cancellation and sign-out deliberately remain available after disablement.
func (s *Server) admitMCPConnectorAction(ctx context.Context, name string) error {
	name = strings.TrimSpace(name)
	if !runtime.IsPluginMCPServerName(name) {
		return nil
	}
	definition, active := s.activePluginMCPDefinition(s.rt.ExtensionSnapshot(), name)
	if !active {
		return fmt.Errorf("MCP server %q is not owned by an active plugin", name)
	}
	if definition.Connector == nil {
		return nil
	}
	if s.rt.UserBinding().IsUser() {
		return errors.New(mcpConnectorConfigurationUnavailable)
	}
	if strings.TrimSpace(definition.Connector.SetupRequired) != "" {
		return fmt.Errorf("MCP connector %q requires setup before it can connect", name)
	}
	view, _, err := s.mcpConfiguration(ctx)
	if err != nil {
		return err
	}
	if override, exists := view.Config.MCPServers[name]; exists && override.Enabled != nil {
		definition.Enabled = override.Enabled
	}
	if !definition.ConnectorEnabled() {
		return fmt.Errorf("MCP connector %q is disabled; enable its startup preference before connecting", name)
	}
	return nil
}

func (s *Server) mcpServerList(ctx context.Context) (MCPListResult, error) {
	view, definitions, err := s.mcpConfiguration(ctx)
	if err != nil {
		return MCPListResult{}, err
	}
	statuses := map[string]mcp.ServerStatus{}
	if mgr, err := s.mcpManager(); err == nil {
		statuses = mgr.Status()
	}
	effective := map[string]config.MCPServerConfig{}
	for name, definition := range view.Config.MCPServers {
		if s.rt.UserBinding().IsUser() || runtime.IsPluginMCPServerName(name) {
			continue
		}
		effective[name] = definition
		if _, exists := statuses[name]; !exists {
			statuses[name] = mcp.ServerStatus{Name: name, State: mcp.MCPServerStateConfigured}
		}
	}
	owners := map[string]string{}
	labels := map[string]string{}
	active := map[string]bool{}
	snapshot := s.rt.ExtensionSnapshot()
	for _, item := range snapshot.Plugins {
		for localName, definition := range item.MCPServers {
			name := runtime.PluginMCPServerName(item.ID, localName)
			owners[name], labels[name] = item.ID, localName
			admitted, enabled := s.activePluginMCPDefinition(snapshot, name)
			active[name] = enabled
			if enabled {
				definition = admitted
			}
			if override, exists := view.Config.MCPServers[name]; !s.rt.UserBinding().IsUser() && exists && override.Enabled != nil {
				definition.Enabled = override.Enabled
			}
			effective[name] = definition
			if !enabled {
				statuses[name] = mcp.ServerStatus{Name: name, State: mcp.MCPServerStateDisabled}
			} else if _, exists := statuses[name]; !exists {
				statuses[name] = mcp.ServerStatus{Name: name, State: mcp.MCPServerStateConfigured}
			}
		}
	}
	result := MCPListResult{Servers: mcpStatusList(statuses)}
	for index := range result.Servers {
		server := &result.Servers[index]
		server.PluginID, server.DisplayName = owners[server.Name], labels[server.Name]
		definition, exists := definitions[server.Name]
		server.Editable = exists && server.PluginID == "" && (definition.Command != "" || definition.URL != "")
		if definition, exists := effective[server.Name]; exists {
			enabled := definition.Enabled == nil || *definition.Enabled
			if server.PluginID != "" {
				enabled = definition.ConnectorEnabled()
			}
			server.ConfigurationEnabled = &enabled
			server.Connector = definition.Connector
			if !enabled && !server.Connected {
				server.State = string(mcp.MCPServerStateDisabled)
			}
			// URLs are display information only; never publish credentials or tokens.
			if u, err := url.Parse(definition.URL); err == nil && (u.Scheme == "http" || u.Scheme == "https") && u.Hostname() != "" {
				u.User, u.RawQuery, u.Fragment, u.RawFragment, u.ForceQuery = nil, "", "", "", false
				server.ResourceURL = u.String()
			}
		}
		if server.PluginID != "" {
			enabled := active[server.Name]
			server.PluginActive = &enabled
			server.Configurable = enabled && server.Connector != nil && !s.rt.UserBinding().IsUser()
			if server.Connector != nil && s.rt.UserBinding().IsUser() {
				server.ConfigurationUnavailableReason = mcpConnectorConfigurationUnavailable
			}
		}
		if mgr, err := s.mcpManager(); err == nil {
			if cfg, ok := mgr.Config(server.Name); ok && cfg.OAuth != nil {
				server.OAuthRedirectURI = cfg.OAuth.RedirectURI
			}
		}
	}
	return result, nil
}

func (s *Server) handleMCPDefinitionRead(ctx context.Context, req Request) error {
	params, err := parseMCPServerActionParams(req)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	view, definitions, err := s.mcpConfiguration(ctx)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if runtime.IsPluginMCPServerName(params.Name) {
		if s.rt.UserBinding().IsUser() {
			return s.writeResponse(req.ID, nil, errors.New(mcpConnectorConfigurationUnavailable))
		}
		definition, active := s.activePluginMCPDefinition(s.rt.ExtensionSnapshot(), params.Name)
		if !active || definition.Connector == nil {
			return s.writeResponse(req.ID, nil, errors.New("this MCP connector is not owned by an active plugin"))
		}
		if override, exists := view.Config.MCPServers[params.Name]; exists && override.Enabled != nil {
			definition.Enabled = override.Enabled
		}
		enabled := definition.ConnectorEnabled()
		return s.writeResponse(req.ID, MCPDefinitionResult{Config: config.MCPServerConfig{Enabled: &enabled}, Revision: view.Revision}, nil)
	}
	definition, exists := definitions[params.Name]
	if !exists || (definition.Command == "" && definition.URL == "") {
		return s.writeResponse(req.ID, nil, errors.New("this MCP definition is managed by its project or plugin"))
	}
	return s.writeResponse(req.ID, MCPDefinitionResult{Config: definition, Revision: view.Revision}, nil)
}

func (s *Server) handleMCPConfigure(ctx context.Context, req Request) error {
	var params MCPConfigureParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if len(params.Servers) == 0 {
		return s.writeResponse(req.ID, nil, errors.New("at least one MCP definition is required"))
	}
	var rawParams struct {
		Servers map[string]json.RawMessage `json:"servers"`
	}
	if err := json.Unmarshal(req.Params, &rawParams); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	for name, definition := range params.Servers {
		if name == "" || strings.TrimSpace(name) != name {
			return s.writeResponse(req.ID, nil, errors.New("MCP names must be nonempty and trimmed"))
		}
		if runtime.IsPluginMCPServerName(name) {
			var preference map[string]json.RawMessage
			if err := json.Unmarshal(rawParams.Servers[name], &preference); err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			if definition == nil || definition.Enabled == nil || len(preference) != 1 || preference["enabled"] == nil {
				return s.writeResponse(req.ID, nil, fmt.Errorf("plugin MCP connector %q accepts only an enabled preference", name))
			}
			if params.Revision == "" {
				return s.writeResponse(req.ID, nil, errors.New("read the MCP connector preference before saving"))
			}
			continue
		}
		if definition == nil {
			continue
		}
		if err := definition.Connector.Validate(); err != nil {
			return s.writeResponse(req.ID, nil, fmt.Errorf("mcp_servers.%s.connector: %w", name, err))
		}
		if (strings.TrimSpace(definition.Command) == "") == (strings.TrimSpace(definition.URL) == "") {
			return s.writeResponse(req.ID, nil, fmt.Errorf("MCP server %q requires either a command or a URL", name))
		}
		if definition.URL != "" {
			u, err := url.Parse(definition.URL)
			if err != nil || (u.Scheme != "http" && u.Scheme != "https") || u.Host == "" || u.User != nil || u.Fragment != "" {
				return s.writeResponse(req.ID, nil, fmt.Errorf("MCP server %q requires an absolute HTTP(S) URL without credentials or a fragment", name))
			}
		}
		switch definition.Transport {
		case "", "http", "streamable-http", "sse":
		default:
			return s.writeResponse(req.ID, nil, fmt.Errorf("MCP server %q has an unsupported transport", name))
		}
		if _, err := definition.ToolTimeoutMS.Duration(); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	releaseMutation, err := s.beginPluginGenerationMutation("configure MCP services in", pluginGenerationMutationExclusive)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer releaseMutation()
	view, definitions, err := s.mcpConfiguration(ctx)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if params.Revision != "" && params.Revision != view.Revision {
		return s.writeResponse(req.ID, nil, errors.New("configuration changed; reopen the MCP editor before saving"))
	}
	snapshot := s.rt.ExtensionSnapshot()
	for name, definition := range params.Servers {
		if runtime.IsPluginMCPServerName(name) {
			if s.rt.UserBinding().IsUser() {
				return s.writeResponse(req.ID, nil, errors.New(mcpConnectorConfigurationUnavailable))
			}
			declared, active := s.activePluginMCPDefinition(snapshot, name)
			if !active || declared.Connector == nil {
				return s.writeResponse(req.ID, nil, fmt.Errorf("MCP connector %q is not owned by an active plugin", name))
			}
			if *definition.Enabled && strings.TrimSpace(declared.Connector.SetupRequired) != "" {
				return s.writeResponse(req.ID, nil, fmt.Errorf("MCP connector %q requires setup before it can be enabled", name))
			}
			continue
		}
		_, effectiveExists := view.Config.MCPServers[name]
		original, editable := definitions[name]
		editable = editable && (original.Command != "" || original.URL != "")
		if params.Revision == "" && (effectiveExists || definition == nil) {
			return s.writeResponse(req.ID, nil, fmt.Errorf("MCP server %q already exists or cannot be removed without a revision", name))
		}
		if params.Revision != "" && !editable {
			return s.writeResponse(req.ID, nil, fmt.Errorf("MCP server %q is managed elsewhere; reopen its definition", name))
		}
	}
	source, err := editableConfigSource(view)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var raw map[string]json.RawMessage
	if err := json.Unmarshal(source, &raw); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	var rawDefinitions map[string]json.RawMessage
	if data := raw["mcp_servers"]; len(data) > 0 {
		if err := json.Unmarshal(data, &rawDefinitions); err != nil {
			return s.writeResponse(req.ID, nil, err)
		}
	}
	if rawDefinitions == nil {
		rawDefinitions = map[string]json.RawMessage{}
	}
	for name, definition := range params.Servers {
		if runtime.IsPluginMCPServerName(name) {
			// Preserve existing JSON fields; this operation is not an editor for
			// the package or its credential settings.
			var preference map[string]json.RawMessage
			if previous := rawDefinitions[name]; len(previous) > 0 {
				if err := json.Unmarshal(previous, &preference); err != nil {
					return s.writeResponse(req.ID, nil, err)
				}
			}
			if preference == nil {
				preference = map[string]json.RawMessage{}
			}
			preference["enabled"], err = json.Marshal(*definition.Enabled)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			rawDefinitions[name], err = json.Marshal(preference)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			continue
		}
		if definition == nil {
			delete(rawDefinitions, name)
		} else {
			data, err := json.Marshal(definition)
			if err != nil {
				return s.writeResponse(req.ID, nil, err)
			}
			rawDefinitions[name] = data
		}
	}
	raw["mcp_servers"], err = json.Marshal(rawDefinitions)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	next, err := json.MarshalIndent(raw, "", "  ")
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	services, release, err := s.rt.RetainStateServices()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer release()
	committed, err := services.Config.CompareExchange(ctx, view.Revision, append(next, '\n'))
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	// The provider resolves project overlays after committing. Build from that
	// authoritative result, so removing an override can reveal a project server.
	activationErr := configActivationError(committed)
	if activationErr == nil {
		candidate, prepareErr := s.rt.PreflightExtensionPolicy(committed.Config)
		if prepareErr != nil {
			activationErr = prepareErr
		} else {
			activationErr = s.rt.ActivatePluginGeneration(candidate, nil)
		}
	}
	if activationErr != nil {
		activationErr = fmt.Errorf("MCP configuration was saved but could not be activated: %w", activationErr)
	} else {
		activationErr = s.recordPublishedConfig(committed.Config)
		s.scheduleCurrentPluginTurnLifecycleReplay()
	}
	result, listErr := s.mcpServerList(ctx)
	return s.writePluginGenerationResponse(req, result, errors.Join(activationErr, listErr), releaseMutation)
}
