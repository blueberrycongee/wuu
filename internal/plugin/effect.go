package plugin

import (
	"path"
	"strings"
)

// ReloadEffect classifies how a development change to a plugin-owned surface
// becomes visible to the host.
type ReloadEffect string

const (
	// ReloadEffectCapability surfaces render or serve data for the UI and never
	// enter the model context; they hot reload without a new session.
	ReloadEffectCapability ReloadEffect = "capability"
	// ReloadEffectMind surfaces enter the model context (tools, skills, hooks,
	// MCP schemas, prompts); conversations adopt them at the next idle turn boundary.
	ReloadEffectMind ReloadEffect = "mind"
	// ReloadEffectTrust is the collection switch (manifest/install/enable/disable
	// semantics); it publishes a generation with independent runtime and UI adoption.
	ReloadEffectTrust ReloadEffect = "trust"
)

// ReloadHint is the minimal visible effect contract: which effect a developer
// should expect, the paths that drove the classification, and a one-line
// hint suitable for a dev tool.
type ReloadHint struct {
	Effect  ReloadEffect `json:"effect"`
	Paths   []string     `json:"paths,omitempty"`
	Message string       `json:"message"`
}

// ClassifyReload maps changed package-relative paths to the effect the
// developer should expect. It is deliberately conservative: paths that cannot
// be attributed to a single surface fall back to the plugin's declared
// surface set, and unknown changes in a mixed plugin are reported honestly
// rather than guessed.
func ClassifyReload(manifest Manifest, changedPaths []string) ReloadHint {
	changed := normalizeRelPaths(changedPaths)
	if len(changed) == 0 {
		return ReloadHint{Effect: "", Message: "no changed source files detected"}
	}

	if containsManifestPath(changed) {
		return ReloadHint{
			Effect:  ReloadEffectTrust,
			Paths:   changed,
			Message: "manifest changed: generation published; desktop reloads independently and conversations adopt agent surfaces at the next idle turn boundary",
		}
	}

	mind := manifest.HasAgentSurfaces()
	capability := manifest.HasCapabilitySurfaces()

	mindMatched := false
	capabilityMatched := false
	for _, rel := range changed {
		if manifest.agentPathMatches(rel) {
			mindMatched = true
		}
		if manifest.capabilityPathMatches(rel) {
			capabilityMatched = true
		}
	}

	switch {
	case mindMatched && capabilityMatched:
		return ReloadHint{
			Effect:  ReloadEffectMind,
			Paths:   changed,
			Message: "agent-link and frontend files changed: running turns keep their snapshot; agent surfaces apply at the next idle turn boundary and desktop reloads independently",
		}
	case mindMatched:
		return ReloadHint{
			Effect:  ReloadEffectMind,
			Paths:   changed,
			Message: "agent-link change: applies at the next idle turn boundary; running turns keep their snapshot",
		}
	case capabilityMatched:
		return ReloadHint{
			Effect:  ReloadEffectCapability,
			Paths:   changed,
			Message: "frontend change: hot reload applies now; no app restart or new session needed",
		}
	case mind && !capability:
		return ReloadHint{
			Effect:  ReloadEffectMind,
			Paths:   changed,
			Message: "agent plugin change: applies at the next idle turn boundary; running turns keep their snapshot",
		}
	case capability && !mind:
		return ReloadHint{
			Effect:  ReloadEffectCapability,
			Paths:   changed,
			Message: "frontend plugin change: hot reload applies now; no app restart or new session needed",
		}
	default:
		return ReloadHint{
			Effect:  ReloadEffectMind,
			Paths:   changed,
			Message: "could not attribute the change to a single surface: package refreshed; frontend-only edits hot reload, agent-link edits apply at the next idle turn boundary",
		}
	}
}

// HasAgentSurfaces reports whether the manifest declares any surface that
// enters the model context.
func (m Manifest) HasAgentSurfaces() bool {
	if m.Runtime != nil {
		return true
	}
	if len(m.Hooks) > 0 || len(m.MCPServers) > 0 {
		return true
	}
	return len(m.Skills) > 0 || len(m.Commands) > 0
}

// HasCapabilitySurfaces reports whether the manifest declares any UI-only
// surface that is rendered or served to the desktop without entering the
// model context.
func (m Manifest) HasCapabilitySurfaces() bool {
	if m.Desktop != nil || len(m.Themes) > 0 || len(m.Settings) > 0 {
		return true
	}
	if len(m.Slots) > 0 || len(m.Surfaces) > 0 || len(m.Presenters) > 0 {
		return true
	}
	return len(m.Navigation) > 0 || len(m.WorkspaceTools) > 0 || len(m.SettingsPages) > 0
}

// agentPathMatches reports whether rel is owned by a declared agent-link
// surface. Declared paths are package-relative and may be files or
// directories, so prefix matching is used.
func (m Manifest) agentPathMatches(rel string) bool {
	if m.RuntimePath != "" && relPathUnder(m.RuntimePath, rel) {
		return true
	}
	for _, dir := range m.skillDirs() {
		if relPathUnder(dir, rel) {
			return true
		}
	}
	for _, declared := range append(append(append([]string{}, m.HookPaths...), m.MCPPaths...), m.CommandPaths...) {
		if relPathUnder(declared, rel) {
			return true
		}
	}
	return false
}

// capabilityPathMatches reports whether rel belongs to a UI-only surface. The
// desktop entry and its directory are treated as frontend-owned; icon assets
// are visual contributions and follow the same hot-reload semantics.
func (m Manifest) capabilityPathMatches(rel string) bool {
	if m.Desktop != nil && strings.TrimSpace(m.Desktop.Entry) != "" {
		entry := normalizeRelPath(m.Desktop.Entry)
		if rel == entry {
			return true
		}
		if dir := path.Dir(entry); dir != "." && relPathUnder(dir, rel) {
			return true
		}
	}
	if m.Icon != nil {
		for _, asset := range m.Icon.AssetPaths() {
			if relPathUnder(asset, rel) {
				return true
			}
		}
	}
	return false
}

// skillDirs returns the declared skill directories, or the conventional
// skills directory when the manifest does not declare any.
func (m Manifest) skillDirs() []string {
	if len(m.Skills) == 0 {
		return []string{"skills"}
	}
	return m.Skills
}

func normalizeRelPaths(values []string) []string {
	seen := make(map[string]struct{}, len(values))
	out := make([]string, 0, len(values))
	for _, value := range values {
		rel := normalizeRelPath(value)
		if rel == "" || rel == "." {
			continue
		}
		if _, ok := seen[rel]; ok {
			continue
		}
		seen[rel] = struct{}{}
		out = append(out, rel)
	}
	return out
}

func normalizeRelPath(value string) string {
	value = strings.TrimSpace(value)
	if value == "" {
		return ""
	}
	return strings.TrimPrefix(path.Clean(strings.ReplaceAll(value, "\\", "/")), "./")
}

func isManifestPath(rel string) bool {
	switch normalizeRelPath(rel) {
	case ManifestFilename, CodexManifestFilename, ClaudeManifestFilename:
		return true
	default:
		return false
	}
}

func containsManifestPath(changed []string) bool {
	for _, rel := range changed {
		if isManifestPath(rel) {
			return true
		}
	}
	return false
}

// relPathUnder reports whether rel equals dir or lives beneath it, both in
// slash-normalized package-relative form.
func relPathUnder(dir, rel string) bool {
	dir = strings.TrimSuffix(normalizeRelPath(dir), "/")
	if dir == "" || dir == "." {
		return false
	}
	return rel == dir || strings.HasPrefix(rel, dir+"/")
}
