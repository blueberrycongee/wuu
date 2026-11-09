package skills

import (
	"embed"
	"io/fs"
	"path"
	"sort"
	"strings"
)

// The whole bundled tree is embedded (not just SKILL.md) so a skill's
// supporting files — references/, scripts/, assets/ — travel in the binary and
// can be materialized to disk at runtime. See materialize.go.
//
//go:embed bundled
var bundledFS embed.FS

// BundledSkills returns skills compiled into the binary. These are
// parsed at call time from the embedded filesystem. Discovered skills
// with the same name take precedence (project customization wins).
func BundledSkills() []Skill {
	var out []Skill
	_ = fs.WalkDir(bundledFS, "bundled", func(filePath string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		base := path.Base(filePath)
		// Only SKILL.md entrypoints are skills; sibling references/*.md and
		// scripts are supporting files, not separate skills.
		if !strings.EqualFold(base, "SKILL.md") {
			return nil
		}
		data, err := bundledFS.ReadFile(filePath)
		if err != nil {
			return nil
		}
		dir := path.Dir(filePath)
		s := parseSkillContent(string(data), base, "bundled", dir)
		if s.Name == "" {
			return nil
		}
		if strings.EqualFold(base, "SKILL.md") {
			s.Name = canonicalName(firstNonEmpty(s.Name, path.Base(dir)))
		}
		s.Path = filePath
		s.Dir = dir
		out = append(out, s)
		return nil
	})
	sort.Slice(out, func(i, j int) bool { return out[i].Name < out[j].Name })
	return out
}

func firstNonEmpty(values ...string) string {
	for _, value := range values {
		value = strings.TrimSpace(value)
		if value != "" {
			return value
		}
	}
	return ""
}

// MergeWithBundled merges discovered skills with the binary-embedded bundled
// skills. The bundled tree is materialized to disk under cacheRoot (see
// MaterializeBundled) so each skill's supporting files (references/, scripts/,
// assets/) live on a real filesystem path the model's Read/Bash tools can
// reach — the same contract as disk-based skills. Discovered skills override
// bundled skills with the same name. cacheRoot may be empty, in which case
// bundled skills fall back to embed-only parsing with a virtual directory.
func MergeWithBundled(discovered []Skill, cacheRoot string) []Skill {
	bundled := materializedOrEmbeddedBundled(cacheRoot)
	if len(bundled) == 0 {
		return discovered
	}

	// Index discovered names for dedup.
	seen := make(map[string]bool, len(discovered))
	for _, s := range discovered {
		seen[s.Name] = true
	}

	// Append bundled skills not overridden by discovered ones.
	merged := make([]Skill, len(discovered), len(discovered)+len(bundled))
	copy(merged, discovered)
	for _, s := range bundled {
		if !seen[s.Name] {
			merged = append(merged, s)
		}
	}
	return merged
}

// materializedOrEmbeddedBundled returns bundled skills backed by real on-disk
// paths when materialization to cacheRoot succeeds, otherwise the embed-only
// parse (which has an unresolvable virtual directory). The disk-backed form is
// required for any skill that ships supporting files.
func materializedOrEmbeddedBundled(cacheRoot string) []Skill {
	if root, err := MaterializeBundled(cacheRoot); err == nil && root != "" {
		if scanned := scanDir(root, "bundled"); len(scanned) > 0 {
			return scanned
		}
	}
	return BundledSkills()
}
