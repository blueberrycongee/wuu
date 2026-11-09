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

const mcpConfigurationUnavailable = "This MCP server is unavailable in this user space."

// Active ownership is resolved from the admitted package, not from a namespaced
// configuration key. A disabled or replaced package cannot be reached here.
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

// A shared user host reaches only the owner's global standalone servers.
// Cleanup reads, cancellation and sign-out use this check alone.
func (s *Server) checkMCPServerDomain(name string) error {
	if s.rt == nil || !s.rt.UserBinding().IsUser() {
		return nil
	}
	if !s.rt.SharedMCPConnectors() || runtime.IsPluginMCPServerName(name) {
		return errors.New(mcpConfigurationUnavailable)
	}
	return nil
}

// Connection and OAuth entry points also require an active plugin owner.
func (s *Server) admitMCPServerAction(name string) error {
	name = strings.TrimSpace(name)
	if err := s.checkMCPServerDomain(name); err != nil {
		return err
	}
	if runtime.IsPluginMCPServerName(name) {
		if _, active := s.activePluginMCPDefinition(s.rt.ExtensionSnapshot(), name); !active {
			return fmt.Errorf("MCP server %q is not owned by an active plugin", name)
		}
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
	user := s.rt.UserBinding().IsUser()
	effective := map[string]config.MCPServerConfig{}
	if !user || s.rt.SharedMCPConnectors() {
		for name, definition := range view.Config.MCPServers {
			if runtime.IsPluginMCPServerName(name) {
				continue
			}
			effective[name] = definition
			if _, exists := statuses[name]; !exists {
				statuses[name] = mcp.ServerStatus{Name: name, State: mcp.MCPServerStateConfigured}
			}
		}
	}
	owners := map[string]string{}
	labels := map[string]string{}
	active := map[string]bool{}
	snapshot := s.rt.ExtensionSnapshot()
	for _, item := range snapshot.Plugins {
		if user {
			break
		}
		for localName, definition := range item.MCPServers {
			name := runtime.PluginMCPServerName(item.ID, localName)
			owners[name], labels[name] = item.ID, localName
			admitted, enabled := s.activePluginMCPDefinition(snapshot, name)
			active[name] = enabled
			if enabled {
				definition = admitted
			}
			if override, exists := view.Config.MCPServers[name]; exists && override.Enabled != nil {
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
	toolNames := map[string][]string{}
	if mgr, err := s.mcpManager(); err == nil {
		for _, tool := range mgr.NativeTools() {
			name := mcp.NewMCPTool(tool.Client, tool.Definition).Name()
			if s.rt.Toolkit == nil || !s.rt.Toolkit.IsToolDisabled(name) {
				toolNames[tool.Client.Name()] = append(toolNames[tool.Client.Name()], name)
			}
		}
	}
	result := MCPListResult{Servers: mcpStatusList(statuses)}
	for index := range result.Servers {
		server := &result.Servers[index]
		server.PluginID, server.DisplayName = owners[server.Name], labels[server.Name]
		server.ToolNames = toolNames[server.Name]
		definition, exists := definitions[server.Name]
		server.Editable = !user && exists && server.PluginID == "" && (definition.Command != "" || definition.URL != "")
		if definition, exists := effective[server.Name]; exists {
			enabled := definition.Enabled == nil || *definition.Enabled
			server.ConfigurationEnabled = &enabled
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
		}
		if mgr, err := s.mcpManager(); err == nil {
			if auth, err := mgr.OAuthStatus(ctx, server.Name); err == nil {
				server.AuthorizationID = auth.AuthorizationID
			}
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
	if s.rt.UserBinding().IsUser() {
		return s.writeResponse(req.ID, nil, errors.New(mcpConfigurationUnavailable))
	}
	view, definitions, err := s.mcpConfiguration(ctx)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	definition, exists := definitions[params.Name]
	if runtime.IsPluginMCPServerName(params.Name) || !exists || (definition.Command == "" && definition.URL == "") {
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
	if s.rt.UserBinding().IsUser() {
		return s.writeResponse(req.ID, nil, errors.New(mcpConfigurationUnavailable))
	}
	for name, definition := range params.Servers {
		if name == "" || strings.TrimSpace(name) != name {
			return s.writeResponse(req.ID, nil, errors.New("MCP names must be nonempty and trimmed"))
		}
		if runtime.IsPluginMCPServerName(name) {
			return s.writeResponse(req.ID, nil, fmt.Errorf("MCP server %q is managed by its plugin", name))
		}
		if definition == nil {
			continue
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
	// Running turns keep their pinned generation, as when a plugin is enabled or
	// disabled; removal fences old connections without stopping those turns.
	releaseMutation, err := s.beginPluginGenerationMutation("configure MCP services in", pluginGenerationMutationLive)
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
	for name, definition := range params.Servers {
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
	s.rt.RetireRemovedMCPServers(committed.Config)
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
