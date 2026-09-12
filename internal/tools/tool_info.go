package tools

import (
	"encoding/json"
	"strings"
	"unicode/utf8"
)

const (
	directMCPToolLimit           = 4
	directMCPDescriptionMaxRunes = 600
	directMCPInputSchemaMaxBytes = 8 * 1024
)

// ToolKind groups tools by their primary product behavior. It is intentionally
// coarse so future telemetry can compare broad tool classes without depending
// on provider-specific names.
type ToolKind string

const (
	ToolKindFile      ToolKind = "file"
	ToolKindSearch    ToolKind = "search"
	ToolKindDiscovery ToolKind = "discovery"
	ToolKindShell     ToolKind = "shell"
	ToolKindTest      ToolKind = "test"
	ToolKindGit       ToolKind = "git"
	ToolKindWeb       ToolKind = "web"
	ToolKindSession   ToolKind = "session"
	ToolKindSkill     ToolKind = "skill"
	ToolKindAgent     ToolKind = "agent"
	ToolKindProcess   ToolKind = "process"
	ToolKindSchedule  ToolKind = "schedule"
	ToolKindMCP       ToolKind = "mcp"
	ToolKindBrowser   ToolKind = "browser"
	ToolKindChat      ToolKind = "chat"
	ToolKindPlugin    ToolKind = "plugin"
	ToolKindUnknown   ToolKind = "unknown"
)

// ToolExposure describes whether a known tool is currently visible to the
// model. Hidden tools remain known to the runtime, but are not exposed in
// Definitions() and cannot be executed through this toolkit.
type ToolExposure string

const (
	ToolExposureDirect   ToolExposure = "direct"
	ToolExposureHidden   ToolExposure = "hidden"
	ToolExposureDeferred ToolExposure = "deferred"
)

// ToolInfo is lightweight metadata for inspection, policy, and telemetry.
type ToolInfo struct {
	Name            string       `json:"name"`
	Kind            ToolKind     `json:"kind"`
	Exposure        ToolExposure `json:"exposure"`
	Risk            ToolRisk     `json:"risk"`
	ReadOnly        bool         `json:"read_only"`
	ConcurrencySafe bool         `json:"concurrency_safe"`
	Destructive     bool         `json:"destructive"`
	Reason          string       `json:"reason,omitempty"`
}

// ToolInfo returns metadata for a known tool. Disabled tools still return
// metadata, but with hidden exposure. This is the default classification for
// the tool without call arguments; use ToolMetadata for call-specific behavior.
func (t *Toolkit) ToolInfo(name string) (ToolInfo, bool) {
	tool := t.LookupTool(name)
	if tool == nil {
		return ToolInfo{}, false
	}
	return buildToolInfo(tool, t.toolExposure(name)), true
}

// ToolInfos returns metadata for every known tool, including hidden tools.
func (t *Toolkit) ToolInfos() []ToolInfo {
	all := t.allKnownTools()
	out := make([]ToolInfo, 0, len(all))
	for _, tool := range all {
		out = append(out, buildToolInfo(tool, t.toolExposure(tool.Name())))
	}
	return out
}

func buildToolInfo(tool Tool, exposure ToolExposure) ToolInfo {
	return buildToolInfoForArgs(tool, exposure, "")
}

func buildToolInfoForArgs(tool Tool, exposure ToolExposure, argsJSON string) ToolInfo {
	kind := classifyToolKind(tool.Name())
	classification := classifyToolCall(tool, kind, argsJSON)
	return ToolInfo{
		Name:            tool.Name(),
		Kind:            kind,
		Exposure:        exposure,
		Risk:            classification.Risk,
		ReadOnly:        classification.ReadOnly,
		ConcurrencySafe: classification.ConcurrencySafe,
		Destructive:     classification.Destructive,
		Reason:          classification.Reason,
	}
}

func (t *Toolkit) toolExposure(name string) ToolExposure {
	if t.isToolDisabled(name) {
		return ToolExposureHidden
	}
	// The bash-first surface collapses every command entry point into a
	// single "bash" tool. The model never has to choose between bash and
	// the structured git tool: git stays registered so LookupTool still
	// finds it, but toolExposure returns Hidden for every surface so it
	// never appears in Definitions.
	if isAdvancedCommandToolHidden(name) {
		return ToolExposureHidden
	}
	surface := t.activeCompiledSurface()
	if surface.ProfileName != "" {
		if !activeSurfaceAllowsKnownTool(surface, t.LookupTool(name)) {
			return ToolExposureHidden
		}
		return activeSurfaceToolExposure(surface, name)
	}
	if classifyToolKind(name) == ToolKindMCP {
		if t.shouldExposeMCPDirectly(name) {
			return ToolExposureDirect
		}
		return ToolExposureDeferred
	}
	if t.shouldDeferByDefault(name) {
		return ToolExposureDeferred
	}
	return ToolExposureDirect
}

// isAdvancedCommandToolHidden reports whether the given tool name
// belongs to the set of advanced command tools that the bash-first
// surface demotes to internal. The model-facing command surface is
// "bash" only; the structured git tool stays registered so internal
// callers (tool_search, replay) can still reach it, but it is never
// exposed in Definitions.
func isAdvancedCommandToolHidden(name string) bool {
	switch name {
	case "git":
		return true
	}
	return false
}

func (t *Toolkit) shouldExposeMCPDirectly(name string) bool {
	tools := t.mcpToolsForExposure()
	if len(tools) == 0 || len(tools) > directMCPToolLimit {
		return false
	}
	for _, tool := range tools {
		if tool.Name() != name {
			continue
		}
		return isDirectMCPToolCandidate(tool)
	}
	return false
}

func (t *Toolkit) mcpToolsForExposure() []Tool {
	all := t.allKnownTools()
	out := make([]Tool, 0, len(all))
	for _, tool := range all {
		if classifyToolKind(tool.Name()) == ToolKindMCP {
			out = append(out, tool)
		}
	}
	return out
}

func isDirectMCPToolCandidate(tool Tool) bool {
	def := tool.Definition()
	if strings.TrimSpace(def.Name) == "" {
		return false
	}
	if utf8.RuneCountInString(def.Description) > directMCPDescriptionMaxRunes {
		return false
	}
	if len(def.InputSchema) > 0 {
		raw, err := json.Marshal(def.InputSchema)
		if err != nil || len(raw) > directMCPInputSchemaMaxBytes {
			return false
		}
	}
	return true
}

func classifyToolKind(name string) ToolKind {
	switch name {
	case "read_file", "write_file", "list_files", "edit_file", "apply_patch":
		return ToolKindFile
	case "grep", "glob":
		return ToolKindSearch
	case "tool_search":
		return ToolKindDiscovery
	case "bash":
		return ToolKindShell
	case "git":
		return ToolKindGit
	case "web_search", "web_fetch":
		return ToolKindWeb
	case "thread_get", "yield_turn":
		return ToolKindSession
	case "load_skill":
		return ToolKindSkill
	case "list_agent_profiles", "create_agent_profile":
		return ToolKindAgent
	case "chat_check", "chat_read", "chat_session", "chat_roster", "chat_send", "collaboration_send", "chat_draft", "chat_task", "chat_work", "chat_verify", "chat_remind":
		return ToolKindChat
	case "browser", browserToolName:
		return ToolKindBrowser
	default:
		if strings.HasPrefix(name, "mcp_") {
			return ToolKindMCP
		}
		return ToolKindUnknown
	}
}

func isDeferredByDefault(name string) bool {
	if strings.HasPrefix(name, "mcp_") {
		return true
	}
	switch name {
	case "thread_get":
		return true
	default:
		return false
	}
}

func (t *Toolkit) shouldDeferByDefault(name string) bool {
	if isDeferredByDefault(name) {
		return true
	}
	return false
}
