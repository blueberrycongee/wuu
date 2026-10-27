package session

import (
	"sort"
	"strings"
)

// PluginGenerationBinding is one plugin's contribution identity inside a
// session generation snapshot.
type PluginGenerationBinding struct {
	ID          string `json:"id"`
	Fingerprint string `json:"fingerprint"`
}

// PluginGenerationSnapshot is the durable, session-scoped set of plugin
// contributions the session was created against. Old sessions keep their old
// snapshot while new sessions adopt the current generation.
type PluginGenerationSnapshot struct {
	Plugins []PluginGenerationBinding `json:"plugins"`
}

// Normalize trims, dedupes, and sorts bindings by id so equivalent snapshots
// compare and persist identically.
func (s PluginGenerationSnapshot) Normalize() PluginGenerationSnapshot {
	seen := make(map[string]struct{}, len(s.Plugins))
	out := make([]PluginGenerationBinding, 0, len(s.Plugins))
	for _, binding := range s.Plugins {
		id := strings.TrimSpace(binding.ID)
		if id == "" {
			continue
		}
		if _, ok := seen[id]; ok {
			continue
		}
		seen[id] = struct{}{}
		out = append(out, PluginGenerationBinding{
			ID:          id,
			Fingerprint: strings.TrimSpace(binding.Fingerprint),
		})
	}
	sort.Slice(out, func(i, j int) bool { return out[i].ID < out[j].ID })
	return PluginGenerationSnapshot{Plugins: out}
}
