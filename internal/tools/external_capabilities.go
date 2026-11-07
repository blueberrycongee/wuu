package tools

import (
	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// ConfigureExternalCapabilities limits a private toolkit to workspace discovery,
// the embedded browser, CUA and explicitly referenced MCP servers. It preserves permission and tool
// policy gates while removing native-worker and provider presentation state.
// Call before publishing the toolkit to an external engine.
func (t *Toolkit) ConfigureExternalCapabilities() {
	if t == nil || t.env == nil {
		return
	}
	t.env.externalCapabilitiesOnly = true
	t.env.AgentControl = nil
	t.env.ProcessMgr = nil
	t.env.ExecutionEnvironment = nil
	t.env.PluginManager = nil
	t.ConfigurePTC(nil, config.PTCConfig{})
	t.SetToolSearchEnabled(false)
	t.SetNativeDeferredToolDiscovery(false)
	t.SetActiveProfile(modelprofile.Profile{}, true)
}

// SetConversationTools admits host-resolved user references to this thread's
// external-engine bridge. The caller validates their identity before execution.
func (t *Toolkit) SetConversationTools(names, pluginTools []string) {
	if t == nil || t.env == nil {
		return
	}
	t.env.conversationMCPServers = make(map[string]bool, len(names))
	for _, name := range names {
		t.env.conversationMCPServers[name] = true
	}
	t.env.conversationPluginTools = make(map[string]bool, len(pluginTools))
	for _, name := range pluginTools {
		t.env.conversationPluginTools[name] = true
	}
}

// ExternalCapabilityDefinitions returns the directly callable workspace discovery,
// browser and CUA catalog. Admission uses trusted local activity bindings, never MCP
// descriptions, name prefixes, or server-declared capabilities.
func (t *Toolkit) ExternalCapabilityDefinitions() []providers.ToolDefinition {
	if t == nil || t.registry == nil {
		return nil
	}
	t.refreshMCPToolSnapshot(false)
	definitions := make([]providers.ToolDefinition, 0)
	if browser := t.registry.Lookup(browserToolName); browser != nil && !t.IsToolDisabled(browserToolName) {
		definitions = append(definitions, browser.Definition())
	}
	if catalog := t.registry.Lookup("list_workspaces"); catalog != nil && !t.IsToolDisabled(catalog.Name()) {
		definitions = append(definitions, catalog.Definition())
	}
	if present := t.registry.Lookup("present_artifact"); present != nil && !t.IsToolDisabled(present.Name()) {
		definitions = append(definitions, present.Definition())
	}
	for _, tool := range t.mcpToolsSnapshot() {
		if t.externalCapabilityAllowed(tool.Name()) && !t.IsToolDisabled(tool.Name()) {
			definitions = append(definitions, tool.Definition())
		}
	}
	return definitions
}

func (t *Toolkit) externalCapabilityAllowed(name string) bool {
	if name == browserToolName || name == "list_workspaces" || name == "present_artifact" || t.env.conversationPluginTools[name] {
		return true
	}
	for _, tool := range t.mcpToolsSnapshot() {
		if tool.Name() == name {
			binding, bound := t.mcpActivityBindings[tool.ServerName()]
			return bound && binding.Kind == activity.KindCUA || t.env.conversationMCPServers[tool.ServerName()]
		}
	}
	return false
}
