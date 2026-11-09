package runtime

import (
	"context"
	"path/filepath"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
)

// Each toolkit gets its own local discovery cache. The captured package skills
// belong to its pinned generation; refreshing local files never rereads packages.
func (s *Session) bindLocalSkillRefresh(kit *tools.Toolkit, root string, generation *PluginGeneration) {
	if kit == nil {
		return
	}
	if s.UserBinding().IsUser() {
		cfg := s.skillsConfig
		if generation != nil {
			cfg = generation.settings.Skills
		}
		kit.SetSkillRefresh(func(ctx context.Context, _ bool) ([]skills.Skill, error) {
			if !cfg.IsEnabled() {
				return nil, ctx.Err()
			}
			var dirs []skills.SourceDir
			if s.UserSpaceID == "local" && s.ProductDomain == "personal" && s.UserHostIsolationMode != UserHostIsolationVerifiedHost {
				dirs = userSkillDirs(s.HomeDir, s.WuuHome)
			}
			dirs = append(dirs, skills.SourceDir{Path: filepath.Join(s.RootDir, ".agents", "skills"), Source: "user"})
			items, _, err := skills.DiscoverLocal(ctx, dirs, nil)
			if err != nil {
				return nil, err
			}
			byName := make(map[string]skills.Skill, len(items))
			for _, item := range items {
				byName[item.Name] = item
			}
			items = items[:0]
			for _, item := range byName {
				items = append(items, item)
			}
			sort.Slice(items, func(i, j int) bool { return items[i].Name < items[j].Name })
			return items, nil
		})
		return
	}
	plugins, cfg := s.pluginSkills, s.skillsConfig
	if generation != nil {
		plugins, cfg = generation.pluginSkills, generation.settings.Skills
	}
	// The initial catalog for the session root was discovered using RootDir.
	// Preserve that spelling when a toolkit resolves an equivalent symlink, so
	// refreshing does not report a location-only change in the first step.
	if sameRuntimeRoot(root, s.RootDir) {
		root = s.RootDir
	}
	kit.SetSkillRefresh(localSkillLoader(root, s.HomeDir, s.WuuHome, plugins, cfg))
}

func localSkillLoader(root, homeDir, wuuHome string, plugins pluginSkillSnapshot, cfg config.SkillsConfig) func(context.Context, bool) ([]skills.Skill, error) {
	if !cfg.IsEnabled() {
		return func(ctx context.Context, _ bool) ([]skills.Skill, error) { return nil, ctx.Err() }
	}
	dirs := append(userSkillDirs(homeDir, wuuHome), skillProjectDirs(root)...)
	commands := append(commandUserDirs(wuuHome), commandProjectDirs(root)...)
	bundled := skills.MergeWithBundled(nil, wuuHome)
	gate := make(chan struct{}, 1)
	var cached []skills.Skill
	var readAt time.Time
	return func(ctx context.Context, force bool) ([]skills.Skill, error) {
		if err := ctx.Err(); err != nil {
			return nil, err
		}
		select {
		case gate <- struct{}{}:
		case <-ctx.Done():
			return nil, ctx.Err()
		}
		defer func() { <-gate }()
		if !force && !readAt.IsZero() && time.Since(readAt) < time.Second {
			return cached, nil
		}
		local, templates, err := skills.DiscoverLocal(ctx, dirs, commands)
		if err != nil {
			return nil, err
		}
		byName := make(map[string]skills.Skill)
		for _, layer := range [][]skills.Skill{plugins.user, localSkillsBySource(local, "user"), plugins.project, localSkillsBySource(local, "project")} {
			for _, item := range layer {
				byName[item.Name] = item
			}
		}
		discovered := make([]skills.Skill, 0, len(byName))
		for _, item := range byName {
			discovered = append(discovered, item)
		}
		sort.Slice(discovered, func(i, j int) bool { return discovered[i].Name < discovered[j].Name })
		// Later command roots win before native skills and bundled fallback merge.
		byName = make(map[string]skills.Skill)
		for _, item := range templates {
			byName[item.Name] = item
		}
		templates = nil
		for _, item := range byName {
			templates = append(templates, item)
		}
		sort.Slice(templates, func(i, j int) bool { return templates[i].Name < templates[j].Name })
		cached = skills.MergeCommands(skills.MergeCommands(discovered, templates), bundled)
		readAt = time.Now()
		return cached, nil
	}
}

func userSkillDirs(homeDir, wuuHome string) []skills.SourceDir {
	var userDirs []skills.SourceDir
	if home := skillUserHome(homeDir); home != "" {
		for _, path := range []string{".codex/skills", ".claude/skills", ".agents/skills", ".config/opencode/skills"} {
			userDirs = append(userDirs, skills.SourceDir{Path: filepath.Join(home, filepath.FromSlash(path)), Source: "user"})
		}
	}
	if strings.TrimSpace(wuuHome) != "" {
		userDirs = append(userDirs, skills.SourceDir{Path: filepath.Join(wuuHome, "skills"), Source: "user"})
	}
	return userDirs
}

func localSkillsBySource(items []skills.Skill, source string) []skills.Skill {
	var result []skills.Skill
	for _, item := range items {
		if item.Source == source {
			result = append(result, item)
		}
	}
	return result
}
