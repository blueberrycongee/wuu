package tools

import "github.com/blueberrycongee/wuu/internal/skills"

// A catalog is immutable after publication. Its gate belongs to the generation
// that supplied the skills, so retained clones observe revocation without
// adopting a replacement generation's policy or mutating prompt snapshots.
type skillCatalog struct {
	skills    []skills.Skill
	available func(skills.Skill) bool
}

func (catalog *skillCatalog) filter(items []skills.Skill) []skills.Skill {
	var result []skills.Skill
	for _, skill := range items {
		if catalog == nil || catalog.available == nil || catalog.available(skill) {
			result = append(result, skill)
		}
	}
	return result
}

// AvailableSkills returns a fresh catalog slice filtered by live generation
// availability. Skill values remain immutable; model surface filtering is a
// separate step performed by VisibleSkills.
func (e *Env) AvailableSkills() []skills.Skill {
	if e == nil {
		return nil
	}
	catalog := e.skillCatalog.Load()
	if catalog == nil {
		return catalog.filter(e.Skills)
	}
	return catalog.filter(catalog.skills)
}
