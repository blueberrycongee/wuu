package tools

import (
	"strings"

	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/skills"
)

// FilterSkillsForSurface returns only skills whose declared tool needs
// are compatible with the active compiled model surface. Without this
// guard, load_skill can reintroduce terminal or edit-tool instructions
// that the profile compiler intentionally removed from the visible tool
// list.
func FilterSkillsForSurface(all []skills.Skill, surface capability.Surface) []skills.Skill {
	if surface.ProfileName == "" {
		out := make([]skills.Skill, len(all))
		copy(out, all)
		return out
	}
	out := make([]skills.Skill, 0, len(all))
	for _, skill := range all {
		if skillAllowedBySurface(skill, surface) {
			out = append(out, skill)
		}
	}
	return out
}

func skillAllowedBySurface(skill skills.Skill, surface capability.Surface) bool {
	if surface.ProfileName == "" {
		return true
	}
	if !skillSurfaceHasCapability(surface, capability.CapabilityCommandBash) && skillMentionsTerminalOnlyPath(skill) {
		return false
	}
	if len(skill.AllowedTools) == 0 {
		return true
	}
	for _, raw := range skill.AllowedTools {
		name := strings.TrimSpace(raw)
		if name == "" {
			continue
		}
		if !surfaceAllowsSkillTool(surface, name) {
			return false
		}
	}
	return true
}

func surfaceAllowsSkillTool(surface capability.Surface, name string) bool {
	name = strings.TrimSpace(name)
	if name == "" {
		return true
	}
	if _, ok := surface.Tools[name]; ok {
		return true
	}
	if _, ok := surface.DeferredTools[name]; ok {
		return true
	}
	if _, ok := surface.NestedTools[name]; ok {
		return true
	}
	if _, ok := surface.ContributedTools[name]; ok {
		return true
	}
	switch name {
	case "bash":
		return skillSurfaceHasCapability(surface, capability.CapabilityCommandBash)
	case "process":
		return skillSurfaceHasCapability(surface, capability.CapabilityCommandBackground)
	case "git":
		return false
	default:
		return false
	}
}

// Profile capability flags describe the underlying model, not session tool
// restrictions. Skill aliases require a concrete callable tool on this surface.
func skillSurfaceHasCapability(surface capability.Surface, capabilityName capability.Capability) bool {
	for _, names := range []map[string]capability.Capability{surface.Tools, surface.NestedTools} {
		for _, available := range names {
			if available == capabilityName {
				return true
			}
		}
	}
	return false
}

func skillMentionsTerminalOnlyPath(skill skills.Skill) bool {
	return mentionsTerminalOnlyPath(
		skill.Name,
		skill.Description,
		skill.WhenToUse,
		skill.TriggerCondition,
		strings.Join(skill.RequiredContext, " "),
		strings.Join(skill.VerificationChecklist, " "),
		skill.ProgressiveDisclosure,
		skill.Content,
		skill.Path,
		skill.Dir,
		skill.Source,
	)
}
