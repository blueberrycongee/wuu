package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// References preserve user intent, not grants. Resolve names and accounts from
// host-owned catalogs; never accept a client-supplied tool name as authority.
func (s *Server) resolvePluginReferences(ctx context.Context, parts []providers.MessageContentPart, host *pluginhost.Host, kit *tools.Toolkit) (string, error) {
	var references []providers.PluginReference
	seen := make(map[providers.PluginReference]bool)
	for _, part := range parts {
		if part.Type == "plugin_reference" && part.Plugin != nil && !seen[*part.Plugin] {
			references = append(references, *part.Plugin)
			seen[*part.Plugin] = true
		}
	}
	if len(references) == 0 {
		return "", nil
	}
	if len(references) > 32 {
		return "", fmt.Errorf("select at most 32 plugins per message")
	}
	var servers map[string]MCPServerStatus
	for _, ref := range references {
		if ref.MCPServer != "" {
			list, err := s.mcpServerList(ctx)
			if err != nil {
				return "", err
			}
			servers = make(map[string]MCPServerStatus, len(list.Servers))
			for _, server := range list.Servers {
				servers[server.Name] = server
			}
			break
		}
	}
	type selectedPlugin struct {
		providers.PluginReference
		Tools []string `json:"tools"`
	}
	selected := make([]selectedPlugin, 0, len(references))
	for _, ref := range references {
		var names []string
		if ref.MCPServer != "" {
			server, found := servers[ref.MCPServer]
			if !found || server.PluginID != ref.PluginID || server.ResourceURL != ref.ResourceURL {
				return "", fmt.Errorf("plugin connection %q changed; remove its reference and select it again", ref.MCPServer)
			}
			if !server.Connected || server.ConfigurationEnabled != nil && !*server.ConfigurationEnabled || server.PluginActive != nil && !*server.PluginActive {
				return "", fmt.Errorf("plugin connection %q is unavailable; connect or enable it before sending", ref.MCPServer)
			}
			accountID, workspaceID := "", ""
			if identity := server.Identity; identity != nil && identity.State == "verified" {
				if identity.Account != nil {
					accountID = identity.Account.ID
				}
				if identity.Workspace != nil {
					workspaceID = identity.Workspace.ID
				}
			}
			if ref.AccountID != accountID || ref.WorkspaceID != workspaceID || ref.AuthorizationID != server.AuthorizationID {
				return "", fmt.Errorf("account for %q changed; select the connection again to confirm the current account", ref.MCPServer)
			}
			if server.AuthStatus == "oauth" && server.AuthorizationID == "" {
				return "", fmt.Errorf("reconnect %q to bind this reference to its authorization", ref.MCPServer)
			}
			names = server.ToolNames
		} else {
			for _, definition := range host.ToolDefinitions() {
				if tool, ok := host.Tool(definition.Name); ok && tool.PluginID == ref.PluginID {
					names = append(names, definition.Name)
				}
			}
		}
		available := make([]string, 0, len(names))
		for _, name := range names {
			if kit == nil || !kit.IsToolDisabled(name) {
				available = append(available, name)
			}
		}
		if len(available) == 0 {
			return "", fmt.Errorf("selected plugin %q has no available conversation tools; enable it or remove its reference", firstNonEmpty(ref.PluginID, ref.MCPServer))
		}
		selected = append(selected, selectedPlugin{PluginReference: ref, Tools: available})
	}
	data, err := json.Marshal(selected)
	if err != nil {
		return "", err
	}
	return "\n\nSelected plugins (user references; existing tool permissions still apply). Use these exact tools for the requested task; do not substitute another account or claim access when a tool fails:\n" + string(data), nil
}

func latestPluginReferenceParts(history []providers.ChatMessage) []providers.MessageContentPart {
	for index := len(history) - 1; index >= 0; index-- {
		if history[index].Role == "user" {
			return history[index].ContentParts
		}
	}
	return nil
}

func hasPluginReferences(parts []providers.MessageContentPart) bool {
	for _, part := range parts {
		if part.Type == "plugin_reference" {
			return true
		}
	}
	return false
}

func pluginReferenceServers(parts []providers.MessageContentPart) []string {
	var names []string
	for _, part := range parts {
		if part.Type == "plugin_reference" && part.Plugin != nil && strings.TrimSpace(part.Plugin.MCPServer) != "" {
			names = append(names, part.Plugin.MCPServer)
		}
	}
	return names
}
