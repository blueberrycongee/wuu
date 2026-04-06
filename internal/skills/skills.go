package skills

import (
	"bufio"
	"fmt"
	"os"
	"path/filepath"
	"sort"
	"strings"
)

// Skill represents a discovered skill definition.
type Skill struct {
	Name        string // e.g. "/commit"
	Description string
	Content     string // full markdown body after frontmatter
	Source      string // "project" or "user"
	Path        string // filesystem path
}

// Discover scans directories for skill files and returns deduplicated list.
// User-level skills override project-level skills with the same name.
func Discover(projectDir, userDir string) []Skill {
	projectSkills := scanDir(projectDir, "project")
	userSkills := scanDir(userDir, "user")

	// User overrides project
	byName := make(map[string]Skill, len(projectSkills)+len(userSkills))
	for _, s := range projectSkills {
		byName[s.Name] = s
	}
	for _, s := range userSkills {
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

func scanDir(dir, source string) []Skill {
	if dir == "" {
		return nil
	}
	var skills []Skill
	filepath.Walk(dir, func(path string, info os.FileInfo, err error) error {
		if err != nil || info.IsDir() {
			return nil
		}
		if !strings.HasSuffix(strings.ToLower(info.Name()), ".md") {
			return nil
		}
		skill, parseErr := parseSkillFile(path, source)
		if parseErr != nil {
			return nil // skip unparseable files
		}
		skills = append(skills, skill)
		return nil
	})
	return skills
}

func parseSkillFile(path, source string) (Skill, error) {
	f, err := os.Open(path)
	if err != nil {
		return Skill{}, err
	}
	defer f.Close()

	scanner := bufio.NewScanner(f)

	// Check for frontmatter start
	if !scanner.Scan() || strings.TrimSpace(scanner.Text()) != "---" {
		return Skill{}, fmt.Errorf("no frontmatter")
	}

	// Parse frontmatter
	var name, description string
	for scanner.Scan() {
		line := scanner.Text()
		if strings.TrimSpace(line) == "---" {
			break
		}
		if strings.HasPrefix(line, "name:") {
			name = strings.TrimSpace(strings.TrimPrefix(line, "name:"))
		}
		if strings.HasPrefix(line, "description:") {
			description = strings.TrimSpace(strings.TrimPrefix(line, "description:"))
		}
	}

	if name == "" {
		// Use filename as name
		base := filepath.Base(path)
		name = "/" + strings.TrimSuffix(base, filepath.Ext(base))
	}

	// Read body
	var body strings.Builder
	for scanner.Scan() {
		if body.Len() > 0 {
			body.WriteString("\n")
		}
		body.WriteString(scanner.Text())
	}

	return Skill{
		Name:        name,
		Description: description,
		Content:     body.String(),
		Source:      source,
		Path:        path,
	}, nil
}
