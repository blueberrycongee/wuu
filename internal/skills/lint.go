package skills

import (
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"gopkg.in/yaml.v3"
)

// Lint severity levels. An error means discovery drops the skill or loses its
// metadata; a warning means the skill loads but not the way the author likely
// intended.
const (
	LintError   = "error"
	LintWarning = "warning"
)

// LintIssue is a single finding from linting a skill file.
type LintIssue struct {
	Path     string
	Severity string
	Message  string
}

// notExecutedIssues reports frontmatter fields the parser accepts but the
// current runtime does not act on, so a skill does not silently behave
// differently from what its frontmatter promises. Declarations that match the
// actual behavior anyway (context: inline, model: inherit) are not reported.
func notExecutedIssues(path string, skill Skill, fm map[string]any) []LintIssue {
	var out []LintIssue
	warn := func(field, reason string) {
		out = append(out, LintIssue{
			Path:     path,
			Severity: LintWarning,
			Message:  fmt.Sprintf("frontmatter field %q is accepted but not executed by the current runtime: %s", field, reason),
		})
	}
	if v := strings.TrimSpace(skill.Model); v != "" && !strings.EqualFold(v, "inherit") {
		warn("model", "the skill body always runs on the session model")
	}
	if v := strings.TrimSpace(skill.Context); v != "" && !strings.EqualFold(v, "inline") {
		warn("context", "the skill body is always loaded inline into the current context")
	}
	if strings.TrimSpace(skill.Agent) != "" {
		warn("agent", "no sub-agent is spawned for skill bodies")
	}
	if strings.TrimSpace(skill.Effort) != "" {
		warn("effort", "the session reasoning effort is not changed")
	}
	if len(skill.Paths) > 0 {
		warn("paths", "skills are not conditionally activated by path")
	}
	for key := range fm {
		if strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "_", "-")) == "hooks" {
			warn("hooks", "skill-declared hooks are not registered")
			break
		}
	}
	return out
}

// LintPath lints skills at path with the same parsing rules discovery uses, so
// a clean lint means discovery loads the skill exactly as written. path may be
// a flat skill markdown file, a single skill directory containing SKILL.md, or
// a root directory holding several skills.
func LintPath(path string) ([]LintIssue, error) {
	info, err := os.Stat(path)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() {
		return lintFlatFile(path), nil
	}
	if file := findSkillMD(path); file != "" {
		return lintSkillDir(path, file), nil
	}

	entries, err := os.ReadDir(path)
	if err != nil {
		return nil, err
	}
	var out []LintIssue
	found := false
	for _, entry := range entries {
		child := filepath.Join(path, entry.Name())
		if entry.IsDir() {
			if file := findSkillMD(child); file != "" {
				found = true
				out = append(out, lintSkillDir(child, file)...)
			}
			continue
		}
		if strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			found = true
			out = append(out, lintFlatFile(child)...)
		}
	}
	if !found {
		out = append(out, LintIssue{
			Path:     path,
			Severity: LintWarning,
			Message:  "no skills found (expected <name>/SKILL.md directories or flat .md files)",
		})
	}
	return out, nil
}

// lintSkillDir lints a directory-format skill. The folder name is the
// canonical skill name; discovery drops the skill when it is not portable.
func lintSkillDir(dir, skillFile string) []LintIssue {
	var out []LintIssue
	folderName := canonicalName(filepath.Base(dir))
	if !isPortableSkillName(folderName) {
		out = append(out, LintIssue{
			Path:     skillFile,
			Severity: LintError,
			Message:  fmt.Sprintf("folder name %q is not a portable skill name (lowercase letters, digits, single hyphens, max 64 chars); discovery drops this skill", folderName),
		})
	}
	issues, skill, ok := lintFile(skillFile)
	out = append(out, issues...)
	if ok && skill.Name != "" && skill.Name != folderName {
		out = append(out, LintIssue{
			Path:     skillFile,
			Severity: LintWarning,
			Message:  fmt.Sprintf("frontmatter name %q is ignored; the folder name %q is canonical", skill.Name, folderName),
		})
	}
	return out
}

// lintFlatFile lints a flat-format skill (<dir>/<skill-name>.md).
func lintFlatFile(path string) []LintIssue {
	issues, skill, ok := lintFile(path)
	if !ok {
		return issues
	}
	name := skill.Name
	if name == "" {
		base := filepath.Base(path)
		name = strings.TrimSuffix(base, filepath.Ext(base))
	}
	if !isPortableSkillName(canonicalName(name)) {
		issues = append(issues, LintIssue{
			Path:     path,
			Severity: LintWarning,
			Message:  fmt.Sprintf("skill name %q is not portable (lowercase letters, digits, single hyphens, max 64 chars); other tools may reject it", name),
		})
	}
	return issues
}

// lintFile checks one skill markdown file and returns the parsed skill when
// the file is loadable, so callers can run name checks against it.
func lintFile(path string) ([]LintIssue, Skill, bool) {
	data, err := os.ReadFile(path)
	if err != nil {
		return []LintIssue{{Path: path, Severity: LintError, Message: fmt.Sprintf("cannot read file: %v", err)}}, Skill{}, false
	}

	front, body, err := splitFrontmatter(string(data))
	if err != nil {
		return []LintIssue{{
			Path:     path,
			Severity: LintError,
			Message:  "missing YAML frontmatter (leading --- fence); discovery skips this file",
		}}, Skill{}, false
	}

	var out []LintIssue
	skill := Skill{UserInvocable: true}
	var unknown []string
	if strings.TrimSpace(front) != "" {
		var fm map[string]any
		if yamlErr := yaml.Unmarshal([]byte(front), &fm); yamlErr != nil {
			out = append(out, LintIssue{
				Path:     path,
				Severity: LintError,
				Message:  fmt.Sprintf("frontmatter is not valid YAML (discovery keeps the skill but every metadata field stays empty): %v", yamlErr),
			})
		} else {
			unknown = assignFrontmatter(&skill, fm)
			out = append(out, notExecutedIssues(path, skill, fm)...)
		}
	}
	for _, key := range unknown {
		out = append(out, LintIssue{
			Path:     path,
			Severity: LintWarning,
			Message:  fmt.Sprintf("unknown frontmatter key %q is ignored", key),
		})
	}

	if strings.TrimSpace(skill.Description) == "" {
		out = append(out, LintIssue{
			Path:     path,
			Severity: LintWarning,
			Message:  "description is empty; the skill is hidden from the model catalog (users can still invoke it by name)",
		})
	}
	if strings.TrimSpace(body) == "" {
		out = append(out, LintIssue{
			Path:     path,
			Severity: LintWarning,
			Message:  "skill body is empty; loading this skill delivers no instructions",
		})
	}
	return out, skill, true
}
