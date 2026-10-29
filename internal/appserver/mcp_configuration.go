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

func (s *Server) mcpServerList(ctx context.Context) (MCPListResult, error) {
	return s.mcpServerListWithConfiguration(ctx, s.mcpConfiguration)
}

func (s *Server) mcpServerListWithConfiguration(ctx context.Context, read func(context.Context) (config.SourceView, map[string]config.MCPServerConfig, error)) (MCPListResult, error) {
	if s == nil || s.rt == nil {
		return MCPListResult{}, errors.New("mcp manager is unavailable")
	}
	generation := s.rt.RetainPluginGeneration()
	if generation != nil {
		defer s.rt.ReleasePluginGeneration(generation)
	}
	view, definitions, err := read(ctx)
	if err != nil {
		return MCPListResult{}, err
	}
	statuses := map[string]mcp.ServerStatus{}
	mgr, current, release, managerErr := s.mcpManager()
	if managerErr == nil {
		defer release()
		statuses = mgr.Status()
	}
	for name, definition := range view.Config.MCPServers {
		if runtime.IsPluginMCPServerName(name) {
			continue
		}
		if _, exists := statuses[name]; !exists {
			state := mcp.MCPServerStateConfigured
			if definition.Enabled != nil && !*definition.Enabled {
				state = mcp.MCPServerStateDisabled
			}
			statuses[name] = mcp.ServerStatus{Name: name, State: state}
		}
	}
	owners := map[string]string{}
	labels := map[string]string{}
	for _, item := range s.rt.ExtensionSnapshot().Plugins {
		for localName := range item.MCPServers {
			name := runtime.PluginMCPServerName(item.ID, localName)
			owners[name], labels[name] = item.ID, localName
			if _, exists := statuses[name]; !exists {
				statuses[name] = mcp.ServerStatus{Name: name, State: mcp.MCPServerStateDisabled}
			}
		}
	}
	result := MCPListResult{Servers: mcpStatusList(statuses)}
	for index := range result.Servers {
		server := &result.Servers[index]
		server.PluginID, server.DisplayName = owners[server.Name], labels[server.Name]
		definition, exists := definitions[server.Name]
		server.Editable = exists && server.PluginID == "" && (definition.Command != "" || definition.URL != "")
		if managerErr == nil {
			if cfg, ok := mgr.Config(server.Name); ok && cfg.OAuth != nil {
				server.OAuthRedirectURI = cfg.OAuth.RedirectURI
			}
		}
	}
	if !s.rt.IsCurrentPluginGeneration(generation) || (managerErr == nil && !current()) {
		return MCPListResult{}, mcpGenerationChanged()
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
	definition, exists := definitions[params.Name]
	if !exists || runtime.IsPluginMCPServerName(params.Name) || (definition.Command == "" && definition.URL == "") {
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
	for name, definition := range params.Servers {
		if name == "" || strings.TrimSpace(name) != name || runtime.IsPluginMCPServerName(name) {
			return s.writeResponse(req.ID, nil, errors.New("MCP names must be nonempty, trimmed, and outside the plugin. namespace"))
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
