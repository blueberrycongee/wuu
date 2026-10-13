package runtime

import (
	"sort"

	"github.com/blueberrycongee/wuu/internal/activity"
)

// bindActivityOwnership keeps local control tied to the exact provider
// generation, including declarative MCP plugins without a runtime process.
func (g *PluginGeneration) bindActivityOwnership(registry *activity.Registry) {
	g.activityRegistry = registry
	if registry == nil || g.mcp == nil || len(g.mcpBinding) == 0 {
		return
	}
	bindings := g.mcpBinding
	generationID := g.id
	g.mcp.SetConnectionRetiredCallback(func(serverName, connectionID string) {
		if binding, ok := bindings[serverName]; ok {
			registry.RevokeConnection(binding.PluginID, generationID, connectionID)
		}
	})
}

// revokeActivities fences the provider before process and service teardown.
// An empty plugin ID retires every local-control owner in this generation.
func (g *PluginGeneration) revokeActivities(pluginID string) []string {
	if g == nil || g.activityRegistry == nil {
		return nil
	}
	owners := make(map[string]bool)
	for _, binding := range g.mcpBinding {
		if pluginID == "" || binding.PluginID == pluginID {
			owners[binding.PluginID] = true
		}
	}
	ids := make([]string, 0, len(owners))
	for owner := range owners {
		ids = append(ids, owner)
	}
	sort.Strings(ids)
	for _, owner := range ids {
		g.activityRegistry.RevokeOwner(owner, g.id)
	}
	return ids
}
