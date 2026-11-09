package runtime

import (
	"sort"

	"github.com/blueberrycongee/wuu/internal/session"
)

// PluginGenerationSnapshot returns the session-scoped plugin generation this
// runtime is bound to. It is derived from the active plugins, so a session
// created before a plugin change keeps the old identity while a new session
// adopts the new one.
func (s *Session) PluginGenerationSnapshot() session.PluginGenerationSnapshot {
	if s == nil {
		return session.PluginGenerationSnapshot{}
	}
	active := s.ExtensionSnapshot().ActivePlugins
	bindings := make([]session.PluginGenerationBinding, 0, len(active))
	for _, plugin := range active {
		bindings = append(bindings, session.PluginGenerationBinding{
			ID:          plugin.ID,
			Fingerprint: plugin.Fingerprint,
		})
	}
	sort.Slice(bindings, func(i, j int) bool { return bindings[i].ID < bindings[j].ID })
	return session.PluginGenerationSnapshot{Plugins: bindings}.Normalize()
}
