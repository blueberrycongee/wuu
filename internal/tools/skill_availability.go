package tools

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"strings"
)

// SetSkillToolProvider binds the enclosing executor's live, scoped plugin
// tools. Unlike a frozen model schema list, it must omit revoked tools.
// Toolkit clones require their own enclosing executor to bind this provider.
func (t *Toolkit) SetSkillToolProvider(provider func() []providers.ToolDefinition) {
	t.codeModeMu.Lock()
	t.skillToolProvider = provider
	t.codeModeMu.Unlock()
}

// A catalog is immutable after publication. Its gate belongs to the generation
// that supplied the skills, so retained clones observe revocation without
// adopting a replacement generation's policy or mutating prompt snapshots.
type skillCatalog struct {
	skills    []skills.Skill
	available func(skills.Skill) bool
	refresh   func(context.Context, bool) ([]skills.Skill, error)
	baseline  string
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

// SetSkillRefresh installs a root- and generation-specific local discovery path.
// Binding happens before execution; clones retain the old immutable catalog.
func (t *Toolkit) SetSkillRefresh(refresh func(context.Context, bool) ([]skills.Skill, error)) {
	previous := t.env.skillCatalog.Load()
	if previous == nil {
		return
	}
	next := *previous
	next.refresh = refresh
	next.baseline = ""
	t.env.skillCatalog.Store(&next)
}

// CurrentSkills refreshes the browsing snapshot without changing a running
// model's admitted catalog. Ordinary listing coalesces within the loader cache.
func (t *Toolkit) CurrentSkills(ctx context.Context, force bool) ([]skills.Skill, error) {
	catalog := t.env.skillCatalog.Load()
	if catalog == nil || catalog.refresh == nil {
		return t.Skills(), nil
	}
	items, err := catalog.refresh(ctx, force)
	if err != nil {
		return nil, err
	}
	return catalog.filter(items), nil
}

// PrepareModelStep publishes local changes only between model/tool rounds. The
// latest catalog notice is compared with history so retries and unchanged rounds
// do not inject duplicates, and existing prompt/history bytes stay untouched.
func (t *Toolkit) PrepareModelStep(ctx context.Context, history []providers.ChatMessage) ([]providers.ChatMessage, error) {
	catalog := t.env.skillCatalog.Load()
	if catalog == nil || catalog.refresh == nil {
		return nil, nil
	}
	baseline := catalog.baseline
	if baseline == "" {
		baseline = skills.FormatAvailable(t.env.VisibleSkills(), true)
	}
	items, err := catalog.refresh(ctx, true)
	if err != nil {
		if ctxErr := ctx.Err(); ctxErr != nil {
			return nil, ctxErr
		}
		// A failed refresh must not stop the turn. Keep the admitted catalog;
		// a later round boundary retries discovery.
		providers.DebugLogf("refresh local skill catalog: %v", err)
		return nil, nil
	}
	next := *catalog
	next.skills = append([]skills.Skill(nil), items...)
	next.baseline = baseline
	if !t.env.skillCatalog.CompareAndSwap(catalog, &next) {
		return nil, nil
	}
	current := skills.FormatAvailable(t.env.VisibleSkills(), true)
	previous := baseline
	const prefix = "The available skill catalog has changed. Use this catalog for subsequent skill selection; previously loaded instructions remain historical context.\n\n"
	for i := len(history) - 1; i >= 0; i-- {
		if history[i].Origin == "runtime" && history[i].Cause == "skill_catalog" {
			previous = strings.TrimPrefix(history[i].Content, prefix)
			break
		}
	}
	if current == previous {
		return nil, nil
	}
	return []providers.ChatMessage{{Role: "user", Content: prefix + current, Hidden: true, ReadOnly: true, Origin: "runtime", Cause: "skill_catalog"}}, nil
}
