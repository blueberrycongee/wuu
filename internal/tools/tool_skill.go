package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type LoadSkillTool struct{ env *Env }

func NewLoadSkillTool(env *Env) *LoadSkillTool { return &LoadSkillTool{env: env} }

func (t *LoadSkillTool) Name() string            { return "load_skill" }
func (t *LoadSkillTool) IsReadOnly() bool         { return true }
func (t *LoadSkillTool) IsConcurrencySafe() bool  { return true }

func (t *LoadSkillTool) Definition() providers.ToolDefinition {
	return providers.ToolDefinition{
		Name: "load_skill",
		Description: "Load the full body of a named skill from the project's .claude/skills/ or " +
			"the user's ~/.claude/skills/ directory. Skills are reusable instructions that you " +
			"can invoke when their description matches the user's request. The returned body " +
			"may contain ${ARGUMENTS} (replaced by the arguments parameter), ${CLAUDE_SKILL_DIR} " +
			"(skill's directory path), and ${CLAUDE_SESSION_ID} (current session). Use the " +
			"/skills command to see what's available.",
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

	result := map[string]any{
		"name":        skill.Name,
		"description": skill.Description,
		"source":      skill.Source,
		"content":     body,
	}
	if skill.WhenToUse != "" {
		result["when_to_use"] = skill.WhenToUse
	}
	if len(skill.AllowedTools) > 0 {
		result["allowed_tools"] = skill.AllowedTools
	}
	out, err := json.Marshal(result)
	if err != nil {
		return "", err
	}
	return string(out), nil
}
