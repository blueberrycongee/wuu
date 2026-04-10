package skills

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill represents a discovered skill definition. Fields mirror Claude Code's
// frontmatter schema for cross-compatibility — wuu reads them so a CC-style
// SKILL.md can be dropped in unchanged.
type Skill struct {
	Name        string // canonical name without leading slash, e.g. "commit"
	Description string // one-line description, auto-derived from first markdown paragraph if absent
	WhenToUse   string // detailed usage scenarios for the model
	Content     string // full markdown body after frontmatter (no variable substitution applied)
	Source      string // "project" or "user"
	Path        string // filesystem path to the SKILL.md file
	Dir         string // directory containing the skill (parent of SKILL.md, or file's parent for flat)
	ArgumentHint string // gray help text shown after skill name in /<name> ...

	// CC-compatible fields parsed but not yet acted on (kept for forward compat).
	Model              string   // "sonnet", "haiku", "opus", "inherit"
	Context            string   // "inline" (default) or "fork"
	Agent              string   // sub-agent type when Context=fork
	AllowedTools       []string // tools the skill is allowed to call
	UserInvocable      bool     // can the user type /<name> to invoke
	DisableModelInvoke bool     // hide from model auto-invocation
	Paths              []string // glob patterns for conditional activation
	Effort             string   // thinking effort hint
	Version            string   // skill version string
	Shell              string   // "bash" or "powershell"
}

// Discover scans the given directories for skills and returns a deduplicated
// list. Project skills override user skills with the same name.
//
// Each directory is scanned for two formats:
//  1. Directory format: <dir>/<skill-name>/SKILL.md (preferred, CC-compatible)
//  2. Flat file format: <dir>/<skill-name>.md (legacy, simpler)
func Discover(projectDir, userDir string) []Skill {
	userSkills := scanDir(userDir, "user")
	projectSkills := scanDir(projectDir, "project")

	// Project overrides user (project is more specific).
	byName := make(map[string]Skill, len(projectSkills)+len(userSkills))
	for _, s := range userSkills {
		byName[s.Name] = s
	}
	for _, s := range projectSkills {
		byName[s.Name] = s
	}

	result := make([]Skill, 0, len(byName))
	for _, s := range byName {
		result = append(result, s)
	}
	sort.Slice(result, func(i, j int) bool {
		return result[i].Name < result[j].Name
	})
	return result
}

// Find returns the skill with the given name (slash-prefix tolerated), or
// false if not found.
func Find(skills []Skill, name string) (Skill, bool) {
	name = canonicalName(name)
	for _, s := range skills {
		if s.Name == name {
			return s, true
		}
	}
	return Skill{}, false
}

func scanDir(dir, source string) []Skill {
	if dir == "" {
		return nil
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return nil
	}

	var skills []Skill
	entries, err := os.ReadDir(dir)
	if err != nil {
		return nil
	}

	for _, entry := range entries {
		path := filepath.Join(dir, entry.Name())

		if entry.IsDir() {
			// Directory format: <dir>/<skill-name>/SKILL.md
			skillFile := findSkillMD(path)
			if skillFile == "" {
				continue
			}
			skill, parseErr := parseSkillFile(skillFile, source)
			if parseErr != nil {
				continue
			}
			// Use directory name as canonical name if frontmatter didn't provide one.
			if skill.Name == "" || skill.Name == strings.TrimSuffix(filepath.Base(skillFile), filepath.Ext(skillFile)) {
				skill.Name = entry.Name()
			}
			skill.Name = canonicalName(skill.Name)
			skill.Dir = path
			skills = append(skills, skill)
			continue
		}

		// Flat file format: <dir>/<skill-name>.md
		if !strings.HasSuffix(strings.ToLower(entry.Name()), ".md") {
			continue
		}
		skill, parseErr := parseSkillFile(path, source)
		if parseErr != nil {
			continue
		}
		skill.Name = canonicalName(skill.Name)
		skill.Dir = filepath.Dir(path)
		skills = append(skills, skill)
	}
	return skills
}

// findSkillMD returns the path to SKILL.md (case-insensitive) inside dir,
// or empty string if not found.
func findSkillMD(dir string) string {
	entries, err := os.ReadDir(dir)
	if err != nil {
		return ""
	}
	for _, e := range entries {
		if e.IsDir() {
			continue
		}
		if strings.EqualFold(e.Name(), "SKILL.md") {
			return filepath.Join(dir, e.Name())
		}
	}
	return ""
}

// canonicalName strips leading slash and trims whitespace.
func canonicalName(name string) string {
	name = strings.TrimSpace(name)
	name = strings.TrimPrefix(name, "/")
	return name
}

func parseSkillFile(path, source string) (Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)
	scanner.Buffer(make([]byte, 4096), 1024*1024)

	// Check for frontmatter start.
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Skill{}, fmt.Errorf("no frontmatter")
	}

	skill := Skill{
		Source:        source,
		Path:          path,
		UserInvocable: true, // default true for CC compatibility
	}

	// Parse frontmatter as flat key:value pairs. Multi-line YAML lists are
	// supported as comma-separated strings or bracketed inline lists.
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		k, v, ok := splitYAMLLine(line)
		if !ok {
			continue
		}
		switch k {
		case "name":
			skill.Name = v
		case "description":
			skill.Description = v
		case "when-to-use", "when_to_use":
			skill.WhenToUse = v
		case "model":
			skill.Model = v
		case "context":
			skill.Context = v
		case "agent":
			skill.Agent = v
		case "allowed-tools", "allowed_tools":
			skill.AllowedTools = parseList(v)
		case "user-invocable", "user_invocable":
			skill.UserInvocable = parseBool(v, true)
		case "disable-model-invocation", "disable_model_invocation":
			skill.DisableModelInvoke = parseBool(v, false)
		case "argument-hint", "argument_hint":
			skill.ArgumentHint = v
		case "paths":
			skill.Paths = parseList(v)
		case "effort":
			skill.Effort = v
		case "version":
			skill.Version = v
		case "shell":
			skill.Shell = v
		}
	}

	if skill.Name == "" {
		base := filepath.Base(path)
		if strings.EqualFold(base, "SKILL.md") {
			skill.Name = filepath.Base(filepath.Dir(path))
		} else {
			skill.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}

	// Read body.
	var body strings.Builder
	for scanner.Scan() {
		if body.Len() > 0 {
			body.WriteString("\n")
		}
		body.WriteString(scanner.Text())
	}
	skill.Content = body.String()

	// Auto-derive description from first non-empty markdown line if missing.
	if skill.Description == "" {
		skill.Description = firstMarkdownLine(skill.Content)
	}

	return skill, nil
}

// splitYAMLLine parses a "key: value" YAML line, stripping quotes.
func splitYAMLLine(line string) (key, value string, ok bool) {
	// Skip indented lines (list items belonging to a previous key).
	if strings.HasPrefix(line, " ") || strings.HasPrefix(line, "\t") {
		return "", "", false
	}
	idx := strings.Index(line, ":")
	if idx < 0 {
		return "", "", false
	}
	key = strings.TrimSpace(line[:idx])
	value = strings.TrimSpace(line[idx+1:])
	value = strings.Trim(value, `"'`)
	return key, value, key != ""
}

// parseList accepts either "[a, b, c]" or "a, b, c" and returns a slice.
func parseList(v string) []string {
	v = strings.TrimSpace(v)
	v = strings.TrimPrefix(v, "[")
	v = strings.TrimSuffix(v, "]")
	if v == "" {
		return nil
	}
	parts := strings.Split(v, ",")
	out := make([]string, 0, len(parts))
	for _, p := range parts {
		p = strings.Trim(strings.TrimSpace(p), `"'`)
		if p != "" {
			out = append(out, p)
		}
	}
	return out
}

// parseBool accepts "true"/"false"/"yes"/"no" with a default fallback.
func parseBool(v string, def bool) bool {
	switch strings.ToLower(strings.TrimSpace(v)) {
	case "true", "yes", "1", "on":
		return true
	case "false", "no", "0", "off":
		return false
	}
	return def
}

// firstMarkdownLine returns the first non-empty content line from markdown,
// stripping markdown syntax characters.
func firstMarkdownLine(content string) string {
	scanner := bufio.NewScanner(strings.NewReader(content))
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		// Strip leading markdown header markers.
		line = strings.TrimLeft(line, "#")
		line = strings.TrimSpace(line)
		if line == "" {
			continue
		}
		// Cap length so it stays one-line in displays.
		if len(line) > 200 {
			line = line[:200] + "..."
		}
		return line
	}
	return ""
}
