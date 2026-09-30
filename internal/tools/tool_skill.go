package tools

import (
	"context"
	"errors"
	"fmt"
	"html"
	"io/fs"
	"net/url"
	"os"
	"path/filepath"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
)

type LoadSkillTool struct{ env *Env }

func NewLoadSkillTool(env *Env) *LoadSkillTool { return &LoadSkillTool{env: env} }

func (t *LoadSkillTool) Name() string            { return "load_skill" }
func (t *LoadSkillTool) IsReadOnly() bool        { return true }
func (t *LoadSkillTool) IsConcurrencySafe() bool { return true }

func (t *LoadSkillTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "load_skill",
		Description: strings.Join([]string{
			"Load full instructions for a listed skill when the task matches it.",
			"Available skill names and summaries are in the system prompt; output returns a <skill_content name=\"...\"> block.",
		}, "\n"),
		InputSchema: map[string]any{
			"type": "object",
			"properties": map[string]any{
				"name": map[string]any{
					"type":        "string",
					"description": "Skill name (e.g. \"commit\" or \"review-pr\"). Leading slash is optional.",
				},
				"arguments": map[string]any{
					"type":        "string",
					"description": "Optional arguments string substituted into ${ARGUMENTS} placeholders in the skill body.",
				},
			},
			"required": []string{"name"},
		},
	}
}

func (t *LoadSkillTool) Execute(ctx context.Context, argsJSON string) (string, error) {
	var args struct {
		Name      string `json:"name"`
		Arguments string `json:"arguments"`
	}
	if err := decodeArgs(argsJSON, &args); err != nil {
		return "", err
	}
	if strings.TrimSpace(args.Name) == "" {
		return "", errors.New("load_skill requires name")
	}
	skill, ok := t.env.FindSkill(args.Name)
	if !ok {
		return "", fmt.Errorf("skill %q not found. available: %s", args.Name, strings.Join(t.env.SkillNames(), ", "))
	}

	body := t.env.ProcessSkillBody(ctx, skill, args.Arguments)
	output := SkillContentBlock(skill, body)

	result := map[string]any{
		"action": "load_skill",
		"title":  fmt.Sprintf("Loaded skill: %s", skill.Name),
		"output": output,
		"metadata": map[string]any{
			"name":   skill.Name,
			"dir":    skill.Dir,
			"source": skill.Source,
		},
	}
	return mustJSON(result)
}

// SkillContentBlock formats loaded instructions and their relative resource base.
func SkillContentBlock(skill skills.Skill, body string) string {
	dir := strings.TrimSpace(skill.Dir)
	files := sampleSkillFiles(dir, 10)
	return strings.Join([]string{
		fmt.Sprintf(`<skill_content name="%s">`, html.EscapeString(skill.Name)),
		fmt.Sprintf("# Skill: %s", skill.Name),
		"",
		strings.TrimSpace(body),
		"",
		"Base directory for this skill: " + skillBaseReference(dir),
		"Relative paths in this skill (e.g., scripts/, reference/) are relative to this base directory.",
		"Note: file list is sampled.",
		"",
		"<skill_files>",
		files,
		"</skill_files>",
		"</skill_content>",
	}, "\n")
}

func skillBaseReference(dir string) string {
	dir = strings.TrimSpace(dir)
	if dir == "" {
		return "unknown"
	}
	if abs, err := filepath.Abs(dir); err == nil {
		if info, statErr := os.Stat(abs); statErr == nil && info.IsDir() {
			return (&url.URL{Scheme: "file", Path: filepath.ToSlash(abs)}).String()
		}
	}
	return dir
}

func sampleSkillFiles(dir string, limit int) string {
	dir = strings.TrimSpace(dir)
	if dir == "" || limit <= 0 {
		return ""
	}
	info, err := os.Stat(dir)
	if err != nil || !info.IsDir() {
		return ""
	}
	var files []string
	_ = filepath.WalkDir(dir, func(path string, d fs.DirEntry, err error) error {
		if err != nil || d.IsDir() {
			return nil
		}
		if strings.EqualFold(d.Name(), "SKILL.md") {
			return nil
		}
		files = append(files, path)
		return nil
	})
	sort.Strings(files)
	if len(files) > limit {
		files = files[:limit]
	}
	for i, file := range files {
		files[i] = fmt.Sprintf("<file>%s</file>", html.EscapeString(file))
	}
	return strings.Join(files, "\n")
}
