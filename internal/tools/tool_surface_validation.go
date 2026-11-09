package tools

import "github.com/blueberrycongee/wuu/internal/providers"

func (t *Toolkit) ValidateActiveToolSurfaceForProvider(target providers.ToolSurfaceValidationTarget) error {
	if t == nil {
		return nil
	}
	return providers.ValidateToolDefinitionsForProvider(target, t.providerSurfaceDefinitionsForValidation())
}

func (t *Toolkit) providerSurfaceDefinitionsForValidation() []providers.ToolDefinition {
	if t.CodeModeOnly() {
		return t.Definitions()
	}
	surface := t.activeCompiledSurface()
	defs := make([]providers.ToolDefinition, 0)
	seen := map[string]struct{}{}
	for _, tool := range t.allKnownTools() {
		if !activeSurfaceAllowsKnownTool(surface, tool) {
			continue
		}
		name := tool.Name()
		if _, ok := seen[name]; ok {
			continue
		}
		exposure := t.toolExposure(name)
		if exposure != ToolExposureDirect && exposure != ToolExposureDeferred {
			continue
		}
		seen[name] = struct{}{}
		defs = append(defs, tool.Definition())
	}
	return defs
}
