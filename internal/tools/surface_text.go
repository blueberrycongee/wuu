package tools

import (
	"encoding/json"
	"regexp"
	"strings"

	"github.com/blueberrycongee/wuu/internal/capability"
)

var terminalPathMarkerRE = regexp.MustCompile(`(?i)(^|[^a-z0-9])(?:bash|git|command\.bash|terminal|shell)([^a-z0-9]|$)`)
var packageCommandMarkerRE = regexp.MustCompile(`(?i)(^|[^a-z0-9])(?:npm|pnpm|yarn|bun|docker|make)[[:space:]_-]+(?:run|test|install|ci|add|exec|dlx|build|start|dev|serve|preview|compose|runner|command|script|shell)([^a-z0-9]|$)?`)
var npxCommandMarkerRE = regexp.MustCompile(`(?i)(^|[^a-z0-9])npx[[:space:]_-]+[a-z0-9]`)

func mentionsTerminalOnlyPath(parts ...string) bool {
	text := strings.Join(parts, "\n")
	return terminalPathMarkerRE.MatchString(text) ||
		packageCommandMarkerRE.MatchString(text) ||
		npxCommandMarkerRE.MatchString(text)
}

func surfaceLacksTerminalExecution(surface capability.Surface) bool {
	return surface.ProfileName != "" &&
		!surfaceHasVisibleCapability(surface, capability.CapabilityCommandBash)
}

func mcpToolMentionsTerminalOnlyPath(tool Tool) bool {
	if tool == nil || classifyToolKind(tool.Name()) != ToolKindMCP {
		return false
	}
	def := tool.Definition()
	schema, _ := json.Marshal(def.InputSchema)
	return mentionsTerminalOnlyPath(def.Name, def.Description, string(schema))
}

func mcpToolCapability(tool Tool) (capability.Capability, bool) {
	declaring, ok := tool.(CapabilityDeclaringTool)
	if !ok {
		return "", false
	}
	capName, ok := declaring.DeclaredCapability()
	if !ok || strings.TrimSpace(string(capName)) == "" {
		return "", false
	}
	return capName, true
}
