package skills

import (
	"bufio"
	"fmt"
	"io/fs"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"gopkg.in/yaml.v3"
)

// Skill represents a discovered skill definition. Wuu accepts a portable
// frontmatter shape so imported skills can be used without rewriting metadata.
type Skill struct {
	Name         string // canonical name without leading slash, e.g. "commit"
	Description  string // one-line description from frontmatter; empty descriptions stay hidden from model catalogs
	WhenToUse    string // detailed usage scenarios for the model
	Content      string // full markdown body after frontmatter (no variable substitution applied)
	Source       string // "project" or "user"
	Path         string // filesystem path to the SKILL.md file
	Dir          string // directory containing the skill (parent of SKILL.md, or file's parent for flat)
	ArgumentHint string // gray help text shown after skill name in /<name> ...
	// Workflow metadata. These are optional; older skills remain valid.
	TriggerCondition      string
	RequiredContext       []string
	Examples              []string
	VerificationChecklist []string
	ProgressiveDisclosure string

	// Portable skill metadata fields.
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
	// Hooks declares lifecycle hooks this skill registers when loaded.
	// Parsed from YAML frontmatter. Keys are event names, values are
	// lists of hook configs.
	Hooks map[string][]SkillHookConfig `json:"-"`

	local *localFile // mutable file identity; absent for pinned packages and bundled skills
}

// SkillHookConfig is a hook entry declared in skill frontmatter.
type SkillHookConfig struct {
	Matcher string `json:"matcher,omitempty"`
	Type    string `json:"type,omitempty"`
	Command string `json:"command,omitempty"`
	Prompt  string `json:"prompt,omitempty"`
	Model   string `json:"model,omitempty"`
	Timeout int    `json:"timeout,omitempty"`
}

type SourceDir struct {
	Path      string
	Source    string
	Recursive bool
}

// Discover scans the given directories for skills and returns a deduplicated
// list. Project skills override user skills with the same name.
//
// Each directory is scanned for two formats:
//  1. Directory format: <dir>/<skill-name>/SKILL.md (preferred, portable)
//  2. Flat file format: <dir>/<skill-name>.md (legacy, simpler)
func Discover(projectDir, userDir string) []Skill {
	return DiscoverDirs([]string{projectDir}, []string{userDir})
}

// DiscoverDirs scans skill directories from lowest to highest precedence:
// user dirs in order, then project dirs in order. Later dirs override earlier
// dirs with the same skill name.
func DiscoverDirs(projectDirs, userDirs []string) []Skill {
	return DiscoverSourceDirs(sourceDirs(projectDirs, "project"), sourceDirs(userDirs, "user"))
}

// DiscoverSourceDirs is DiscoverDirs with per-directory source labels.
func DiscoverSourceDirs(projectDirs, userDirs []SourceDir) []Skill {
	userSkills := scanSkillSourceDirs(userDirs)
	projectSkills := scanSkillSourceDirs(projectDirs)

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

func sourceDirs(paths []string, source string) []SourceDir {
	out := make([]SourceDir, 0, len(paths))
	for _, path := range paths {
		out = append(out, SourceDir{Path: path, Source: source})
	}
	return out
}

func scanSkillSourceDirs(dirs []SourceDir) []Skill {
	var out []Skill
	for _, dir := range dirs {
		source := strings.TrimSpace(dir.Source)
		if source == "" {
			source = "unknown"
		}
		scanned := scanDir(dir.Path, source)
		if dir.Recursive {
			scanned = scanRecursiveDir(dir.Path, source)
		}
		for _, skill := range scanned {
			out = append(out, skill)
		}
	}
	return out
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
			// Directory format: <dir>/<skill-name>/SKILL.md. Claude Code names a
			// skill by its folder; the frontmatter name is advisory. Drop only
			// when the folder name can't form a portable skill/command name — a
			// name mismatch no longer silently discards the skill.
			skillFile := findSkillMD(path)
			if skillFile == "" {
				continue
			}
			name := canonicalName(entry.Name())
			if !isPortableSkillName(name) {
				continue
			}
			skill, parseErr := parseSkillFile(skillFile, source)
			if parseErr != nil {
				continue
			}
			skill.Name = name
			skill.Dir = path
			if skill.local != nil {
				skill.local.directory = true
			}
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

func scanRecursiveDir(root, source string) []Skill {
	if strings.TrimSpace(root) == "" {
		return nil
	}
	info, err := os.Stat(root)
	if err != nil || !info.IsDir() {
		return nil
	}

	var skills []Skill
	_ = filepath.WalkDir(root, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() || !strings.EqualFold(d.Name(), "SKILL.md") {
			return nil
		}
		dir := filepath.Dir(path)
		name := canonicalName(filepath.Base(dir))
		if !isPortableSkillName(name) {
			return nil
		}
		skill, parseErr := parseSkillFile(path, source)
		if parseErr != nil {
			return nil
		}
		skill.Name = name
		skill.Dir = dir
		if skill.local != nil {
			skill.local.directory = true
		}
		skills = append(skills, skill)
		return nil
	})
	sort.Slice(skills, func(i, j int) bool { return skills[i].Path < skills[j].Path })
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

// isPortableSkillName mirrors opencode's documented skill name format:
// lowercase alphanumeric words separated by single hyphens.
func isPortableSkillName(name string) bool {
	if len(name) == 0 || len(name) > 64 {
		return false
	}
	prevHyphen := false
	for i, r := range name {
		switch {
		case r >= 'a' && r <= 'z':
			prevHyphen = false
		case r >= '0' && r <= '9':
			prevHyphen = false
		case r == '-':
			if i == 0 || prevHyphen {
				return false
			}
			prevHyphen = true
		default:
			return false
		}
	}
	return !prevHyphen
}

func parseSkillFile(path, source string) (Skill, error) {
	data, err := os.ReadFile(path)
	if err != nil {
		return Skill{}, err
	}
	skill, err := parseSkill(string(data), source)
	if err != nil {
		return Skill{}, err
	}
	skill.Path = path
	if source == "project" || source == "user" {
		resolved, err := filepath.EvalSymlinks(path)
		if err != nil {
			return Skill{}, err
		}
		skill.local = &localFile{resolved: resolved}
	}
	if skill.Name == "" {
		base := filepath.Base(path)
		if strings.EqualFold(base, "SKILL.md") {
			skill.Name = filepath.Base(filepath.Dir(path))
		} else {
			skill.Name = strings.TrimSuffix(base, filepath.Ext(base))
		}
	}
	return skill, nil
}

// splitFrontmatter separates a SKILL.md into its raw YAML frontmatter and
// markdown body. It errors only when the leading "---" fence is missing;
// whether the frontmatter is valid YAML is the caller's concern.
func splitFrontmatter(content string) (front, body string, err error) {
	scanner := bufio.NewScanner(strings.NewReader(content))
	scanner.Buffer(make([]byte, 4096), 1024*1024)

	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return "", "", fmt.Errorf("no frontmatter")
	}

	var frontB strings.Builder
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		frontB.WriteString(line)
		frontB.WriteString("\n")
	}

	var bodyB strings.Builder
	for scanner.Scan() {
		if bodyB.Len() > 0 {
			bodyB.WriteString("\n")
		}
		bodyB.WriteString(scanner.Text())
	}
	return frontB.String(), bodyB.String(), nil
}

// parseSkill splits a SKILL.md into YAML frontmatter and markdown body. The
// frontmatter is parsed with a real YAML parser, so block lists
// (allowed-tools:\n  - bash) work identically to inline lists ([bash]) — the
// portable shape imported skills use. Frontmatter that fails to parse is
// tolerated (fields stay empty) rather than rejected, matching the permissive
// behavior of the ecosystems those skills come from.
func parseSkill(content, source string) (Skill, error) {
	front, body, err := splitFrontmatter(content)
	if err != nil {
		return Skill{}, err
	}

	skill := Skill{Source: source, UserInvocable: true}
	if raw := strings.TrimSpace(front); raw != "" {
		var fm map[string]any
		if err := yaml.Unmarshal([]byte(front), &fm); err == nil {
			assignFrontmatter(&skill, fm)
		}
	}
	skill.Content = body

	return skill, nil
}

// assignFrontmatter maps parsed YAML keys onto the Skill. Keys are normalized so
// hyphen and underscore spellings (when-to-use / when_to_use) are equivalent.
// The returned slice lists normalized keys that matched no known field; the
// engine ignores them silently and lint reports them.
func assignFrontmatter(skill *Skill, fm map[string]any) []string {
	var unknown []string
	for key, val := range fm {
		normalized := strings.ToLower(strings.ReplaceAll(strings.TrimSpace(key), "_", "-"))
		switch normalized {
		case "name":
			skill.Name = scalarString(val)
		case "description":
			skill.Description = scalarString(val)
		case "when-to-use":
			skill.WhenToUse = scalarString(val)
		case "trigger", "trigger-condition":
			skill.TriggerCondition = scalarString(val)
		case "model":
			skill.Model = scalarString(val)
		case "context":
			skill.Context = scalarString(val)
		case "agent":
			skill.Agent = scalarString(val)
		case "allowed-tools":
			skill.AllowedTools = stringList(val)
		case "required-context":
			skill.RequiredContext = stringList(val)
		case "examples":
			skill.Examples = stringList(val)
		case "verification-checklist":
			skill.VerificationChecklist = stringList(val)
		case "progressive-disclosure":
			skill.ProgressiveDisclosure = scalarString(val)
		case "user-invocable":
			skill.UserInvocable = boolVal(val, true)
		case "disable-model-invocation":
			skill.DisableModelInvoke = boolVal(val, false)
		case "argument-hint":
			skill.ArgumentHint = scalarString(val)
		case "paths":
			skill.Paths = stringList(val)
		case "effort":
			skill.Effort = scalarString(val)
		case "version":
			skill.Version = scalarString(val)
		case "shell":
			skill.Shell = scalarString(val)
		case "hooks":
			// Declared in the Skill struct but not yet wired to the hook
			// registry; recognized here so lint reports it as a not-executed
			// field rather than an unknown key.
		case "license", "metadata":
			// Inert informational fields common in imported skills;
			// recognized so lint does not flag them as unknown.
		default:
			unknown = append(unknown, normalized)
		}
	}
	sort.Strings(unknown)
	return unknown
}

// scalarString renders a YAML scalar (string, number, bool) as a trimmed string.
func scalarString(v any) string {
	switch t := v.(type) {
	case nil:
		return ""
	case string:
		return strings.TrimSpace(t)
	default:
		return strings.TrimSpace(fmt.Sprintf("%v", t))
	}
}

// stringList accepts a YAML sequence, a single scalar, or a bare comma-separated
// string and returns a trimmed, non-empty string slice.
func stringList(v any) []string {
	switch t := v.(type) {
	case nil:
		return nil
	case []any:
		out := make([]string, 0, len(t))
		for _, item := range t {
			if s := scalarString(item); s != "" {
				out = append(out, s)
			}
		}
		return out
	case string:
		parts := strings.Split(t, ",")
		out := make([]string, 0, len(parts))
		for _, p := range parts {
			if s := strings.Trim(strings.TrimSpace(p), `"'`); s != "" {
				out = append(out, s)
			}
		}
		return out
	default:
		if s := scalarString(v); s != "" {
			return []string{s}
		}
		return nil
	}
}

// boolVal coerces a YAML bool or truthy/falsey string with a default fallback.
func boolVal(v any, def bool) bool {
	switch t := v.(type) {
	case bool:
		return t
	case string:
		switch strings.ToLower(strings.TrimSpace(t)) {
		case "true", "yes", "1", "on":
			return true
		case "false", "no", "0", "off":
			return false
		}
	}
	return def
}

// parseSkillContent parses a skill from raw markdown content (with frontmatter).
// Used by BundledSkills to parse embedded files. The filename is used as a
// fallback name, source identifies the origin ("bundled", "mcp", etc.).
func parseSkillContent(content, filename, source, dir string) Skill {
	s, err := parseSkill(content, source)
	if err != nil {
		return Skill{}
	}
	if s.Name == "" {
		s.Name = strings.TrimSuffix(filename, filepath.Ext(filename))
	}
	s.Source = source
	s.Dir = dir
	if s.Description == "" {
		s.Description = firstMarkdownLine(s.Content)
	}
	return s
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
