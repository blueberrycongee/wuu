package runtime

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/extensions"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/skills"
)

// ExtensionSnapshot is a coherent read of installed and currently available
// extension surfaces. Revocation filters availability without rewriting the
// immutable generation snapshots retained by running conversations.
type ExtensionSnapshot struct {
	Plugins           []pluginpkg.Plugin
	ActivePlugins     []pluginpkg.Plugin
	Skills            []skills.Skill
	ExtensionSettings *extensions.Settings
	PluginHost        *pluginhost.Host
}

func (s *Session) ExtensionSnapshot() ExtensionSnapshot {
	if s == nil {
		return ExtensionSnapshot{}
	}
	s.pluginGenerationMu.Lock()
	defer s.pluginGenerationMu.Unlock()
	snapshot := ExtensionSnapshot{
		Plugins:           append([]pluginpkg.Plugin(nil), s.Plugins...),
		ExtensionSettings: s.ExtensionSettings,
		PluginHost:        s.PluginHost,
	}
	generation := s.pluginGeneration
	if generation != nil {
		generation.revokedMu.Lock()
		defer generation.revokedMu.Unlock()
	}
	for _, item := range s.ActivePlugins {
		if generation == nil || !generation.revokedPlugins[item.ID] {
			snapshot.ActivePlugins = append(snapshot.ActivePlugins, item)
		}
	}
	for _, skill := range s.Skills {
		if generation == nil || !strings.HasPrefix(skill.Source, "plugin:") || !generation.revokedPlugins[strings.TrimPrefix(skill.Source, "plugin:")] {
			snapshot.Skills = append(snapshot.Skills, skill)
		}
	}
	return snapshot
}

func (s *Session) AvailableSkills() []skills.Skill { return s.ExtensionSnapshot().Skills }

// skillAvailable is installed once with a toolkit's immutable skill catalog.
// Clones keep their owning generation's gate across later session publications.
func (g *PluginGeneration) skillAvailable(skill skills.Skill) bool {
	if g == nil || !strings.HasPrefix(skill.Source, "plugin:") {
		return true
	}
	g.revokedMu.Lock()
	defer g.revokedMu.Unlock()
	return !g.revokedPlugins[strings.TrimPrefix(skill.Source, "plugin:")]
}

func (g *PluginGeneration) filterSkills(items []skills.Skill) []skills.Skill {
	if g == nil {
		return append([]skills.Skill(nil), items...)
	}
	g.revokedMu.Lock()
	defer g.revokedMu.Unlock()
	out := make([]skills.Skill, 0, len(items))
	for _, skill := range items {
		if !strings.HasPrefix(skill.Source, "plugin:") || !g.revokedPlugins[strings.TrimPrefix(skill.Source, "plugin:")] {
			out = append(out, skill)
		}
	}
	return out
}
