package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"io/fs"
	"path/filepath"
	"strings"
	"unicode"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type slashCommandTemplate struct {
	Name        string
	Aliases     []string
	Execution   slashCommandExecution
	Prompt      string
	PromptNoArg string
}

type slashCommandExecution string

const slashCommandExecutionInlinePrompt slashCommandExecution = "inline_prompt"

var lightweightSlashCommandTemplates = []slashCommandTemplate{
	{
		Name:        "review",
		Aliases:     []string{"audit"},
		Execution:   slashCommandExecutionInlinePrompt,
		Prompt:      "Review the current code changes (staged, unstaged, and untracked files) with this focus:\n\n{{args}}\n\nProvide prioritized, actionable findings.",
		PromptNoArg: "Review the current code changes (staged, unstaged, and untracked files) and provide prioritized findings.",
	},
	{
		Name:        "debug",
		Aliases:     []string{"investigate", "diagnose"},
		Execution:   slashCommandExecutionInlinePrompt,
		Prompt:      "Investigate this problem before making changes:\n\n{{args}}\n\nFind the root cause first. Use the repository and runtime evidence as needed, then propose or implement the smallest complete fix.",
		PromptNoArg: "Investigate the current problem before making changes. Find the root cause first. Use the repository and runtime evidence as needed, then propose or implement the smallest complete fix.",
	},
	{
		Name:        "fix",
		Aliases:     []string{"repair"},
		Execution:   slashCommandExecutionInlinePrompt,
		Prompt:      "Fix this issue:\n\n{{args}}\n\nInspect the relevant code first, keep the change scoped, and verify the behavior.",
		PromptNoArg: "Fix the current issue. Inspect the relevant code first, keep the change scoped, and verify the behavior.",
	},
	{
		Name:        "test",
		Aliases:     []string{"tests"},
		Execution:   slashCommandExecutionInlinePrompt,
		Prompt:      "Add or update tests for this behavior:\n\n{{args}}\n\nFocus on real behavior coverage, then run the relevant verification.",
		PromptNoArg: "Add or update tests for the current behavior. Focus on real behavior coverage, then run the relevant verification.",
	},
	{
		Name:        "explain",
		Aliases:     []string{"why"},
		Execution:   slashCommandExecutionInlinePrompt,
		Prompt:      "Explain this clearly:\n\n{{args}}\n\nUse the relevant code, errors, or runtime evidence instead of guessing.",
		PromptNoArg: "Explain the current code or behavior clearly. Use the relevant code, errors, or runtime evidence instead of guessing.",
	},
	{
		Name:        "commit",
		Aliases:     []string{"save"},
		Execution:   slashCommandExecutionInlinePrompt,
		Prompt:      "Prepare an atomic repository commit for the current changes.\n\nUser notes:\n{{args}}\n\nReview the diff, run relevant checks when practical, and create a clear English commit message using the capabilities exposed by the active model surface. If the active surface cannot create commits, explain that limitation and report the prepared message.",
		PromptNoArg: "Prepare an atomic repository commit for the current changes. Review the diff, run relevant checks when practical, and create a clear English commit message using the capabilities exposed by the active model surface. If the active surface cannot create commits, explain that limitation and report the prepared message.",
	},
	{
		Name:        "pr",
		Aliases:     []string{"pull-request", "pullrequest"},
		Execution:   slashCommandExecutionInlinePrompt,
		Prompt:      "Prepare a pull request for the current branch.\n\nUser notes:\n{{args}}\n\nReview the diff, summarize the user-facing changes and verification, and create the PR when the branch is ready.",
		PromptNoArg: "Prepare a pull request for the current branch. Review the diff, summarize the user-facing changes and verification, and create the PR when the branch is ready.",
	},
}

// manualCompactSlashCommandName is the slash command that forces a
// compact-only control turn (see isManualCompactPrompt).
const manualCompactSlashCommandName = "compact"

// isManualCompactPrompt reports whether the raw composer prompt is the
// /compact slash command (or one of its aliases). The turn start/queue paths
// use it to route old raw slash input into the compact control operation.
func isManualCompactPrompt(prompt string) bool {
	display := strings.TrimSpace(prompt)
	if !strings.HasPrefix(display, "/") || strings.HasPrefix(display, "//") {
		return false
	}
	command, _, ok := splitSlashCommand(display)
	if !ok {
		return false
	}
	switch strings.ToLower(strings.TrimSpace(command)) {
	case manualCompactSlashCommandName, "compress":
		return true
	default:
		return false
	}
}

func renderLightweightSlashCommandPrompt(prompt string) (string, string, bool) {
	display := strings.TrimSpace(prompt)
	if !strings.HasPrefix(display, "/") || strings.HasPrefix(display, "//") {
		return prompt, "", false
	}
	command, args, ok := splitSlashCommand(display)
	if !ok {
		return prompt, "", false
	}
	template, ok := findLightweightSlashCommandTemplate(command)
	if !ok {
		return prompt, "", false
	}
	if template.Execution != slashCommandExecutionInlinePrompt {
		return prompt, "", false
	}
	rendered := renderSlashTemplate(template, args)
	if strings.TrimSpace(rendered) == "" {
		return prompt, "", false
	}
	return rendered, display, true
}

func splitSlashCommand(value string) (string, string, bool) {
	body := strings.TrimPrefix(value, "/")
	if body == "" {
		return "", "", false
	}
	if first, _ := utf8.DecodeRuneInString(body); unicode.IsSpace(first) {
		return "", "", false
	}
	nameEnd := len(body)
	for index, r := range body {
		if unicode.IsSpace(r) {
			nameEnd = index
			break
		}
	}
	name := strings.ToLower(strings.TrimSpace(body[:nameEnd]))
	if name == "" {
		return "", "", false
	}
	args := ""
	if nameEnd < len(body) {
		args = strings.TrimSpace(body[nameEnd:])
	}
	return name, args, true
}

func findLightweightSlashCommandTemplate(name string) (slashCommandTemplate, bool) {
	for _, template := range lightweightSlashCommandTemplates {
		if template.Name == name {
			return template, true
		}
		for _, alias := range template.Aliases {
			if alias == name {
				return template, true
			}
		}
	}
	return slashCommandTemplate{}, false
}

func renderSlashTemplate(template slashCommandTemplate, args string) string {
	args = strings.TrimSpace(args)
	if args == "" && strings.TrimSpace(template.PromptNoArg) != "" {
		return strings.TrimSpace(template.PromptNoArg)
	}
	return strings.TrimSpace(strings.ReplaceAll(template.Prompt, "{{args}}", args))
}

// Explicit skill drafts carry a catalog identity, never a path to read directly.
// Resolving at submission freezes the instructions for queued turns and retries.
func (s *Server) renderExplicitSkillPrompt(threadID, prompt string) (string, bool, error) {
	command, value, ok := splitSlashCommand(strings.TrimSpace(prompt))
	if !strings.HasPrefix(strings.TrimSpace(prompt), "/") || !ok || command != "skill" {
		return prompt, false, nil
	}
	var identity struct {
		Name    string                `json:"name"`
		Source  string                `json:"source"`
		Path    *string               `json:"path"`
		Project *SkillProjectIdentity `json:"project"`
	}
	decoder := json.NewDecoder(strings.NewReader(value))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&identity); err != nil {
		return "", true, fmt.Errorf("invalid skill selection: %w", err)
	}
	arguments := strings.TrimSpace(value[decoder.InputOffset():])
	if identity.Project != nil && (identity.Path != nil || identity.Source != "project" ||
		identity.Project.Root != sessionWorkspacePath(s.rt.RootDir) || !validProjectSkillPath(identity.Project.Path)) {
		return "", true, fmt.Errorf("invalid project skill selection")
	}
	catalog, root, err := s.skillCatalog(threadID)
	if err != nil {
		return "", true, err
	}
	for _, skill := range catalog {
		if skill.Name != identity.Name || skill.Source != identity.Source {
			continue
		}
		if identity.Project != nil {
			project := projectSkillIdentity(skill, root, s.rt.RootDir)
			if project == nil || *project != *identity.Project {
				continue
			}
		} else {
			selectedPath := ""
			if identity.Path != nil {
				selectedPath = *identity.Path
			}
			if skill.Path != selectedPath {
				continue
			}
		}
		if !skill.UserInvocable {
			return "", true, fmt.Errorf("skill %q cannot be invoked by the user", identity.Name)
		}
		env := tools.Env{SessionID: threadID}
		body := env.ProcessSkillBody(context.Background(), skill, arguments)
		content := tools.SkillContentBlock(skill, body)
		if arguments != "" {
			content += "\n\n" + arguments
		}
		return content, true, nil
	}
	return "", true, fmt.Errorf("selected skill %q is no longer available; select it again", identity.Name)
}

func validProjectSkillPath(value string) bool {
	return value != "." && fs.ValidPath(value) && !strings.Contains(value, `\`) && filepath.IsLocal(filepath.FromSlash(value))
}

// Project identities bind a workspace while selecting its destination-checkout version.
// Only discovered files owned by the skill discovery boundary receive this form.
func projectSkillIdentity(skill skills.Skill, checkoutRoot, workspaceRoot string) *SkillProjectIdentity {
	if skill.Source != "project" || skill.Path == "" {
		return nil
	}
	checkout := skillDiscoveryRoot(checkoutRoot)
	if checkout == "" {
		return nil
	}
	instruction, err := filepath.EvalSymlinks(skill.Path)
	if err != nil {
		return nil
	}
	relative, err := filepath.Rel(checkout, instruction)
	if err != nil {
		return nil
	}
	relative = filepath.ToSlash(relative)
	if !validProjectSkillPath(relative) {
		return nil
	}
	return &SkillProjectIdentity{Root: sessionWorkspacePath(workspaceRoot), Path: relative}
}

func skillDiscoveryRoot(root string) string {
	if root == "" {
		return ""
	}
	canonical, err := filepath.EvalSymlinks(root)
	if err != nil {
		return ""
	}
	if boundary := runtime.SkillProjectRoot(canonical); boundary != "" {
		return boundary
	}
	return canonical
}
