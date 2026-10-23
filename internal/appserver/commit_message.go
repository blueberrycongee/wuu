package appserver

import (
	"context"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/provideroptions"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	gitCommitMessageTimeout = 45 * time.Second
	// gitCommitMessageMaxDiffLength caps the staged diff the desktop sends.
	// The desktop truncates before sending; this is the server-side guard.
	gitCommitMessageMaxDiffLength = 200_000
)

// gitCommitMessageSystemPrompt is adapted from
// thirdparty/multica/server/internal/agenttmpl/templates/commit-message.json,
// narrowed to one-shot generation from a staged diff: the model must return
// exactly one Conventional Commits message and nothing else.
const gitCommitMessageSystemPrompt = `You write git commit messages in the Conventional Commits format.

Format:
  <type>(<scope>): <subject under 72 chars, imperative, lowercase>
  <blank line>
  <body — optional, wrap at 72 chars, explains why not what>

Type vocabulary (use exactly these):
- feat — new user-facing capability
- fix — bug fix the user would notice
- refactor — code change with no behaviour change
- perf — performance improvement
- docs — documentation only
- test — adding/updating tests only
- chore — tooling, deps, build config
- revert — reverts a previous commit

Rules:
- Scope is the area touched (one word); omit it if the change is truly global.
- Imperative mood, lowercase subject: "add login retry", not "Added" or "Adding".
- Pick a specific verb ("rename", "inline", "extract", "guard against"); never vague ones ("update", "fix stuff", "improve things").
- The body explains WHY, not WHAT; omit it when the subject says enough.
- Output only the commit message. No explanations, no quotes, no code fences.`

func (s *Server) handleGitCommitMessage(req Request) error {
	var params GitCommitMessageParams
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	diff := strings.TrimSpace(params.Diff)
	if diff == "" {
		return s.writeResponse(req.ID, nil, errors.New("diff is required"))
	}
	if len([]rune(diff)) > gitCommitMessageMaxDiffLength {
		return s.writeResponse(req.ID, nil, fmt.Errorf("diff exceeds %d characters", gitCommitMessageMaxDiffLength))
	}
	if s.rt == nil {
		return s.writeResponse(req.ID, nil, errors.New("BYOK model runtime is not available"))
	}
	binding, err := s.rt.RetainState()
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	models, err := binding.Models()
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("BYOK model runtime is not available: %w", err))
	}
	if models.Main == nil {
		return s.writeResponse(req.ID, nil, errors.New("BYOK model runtime is not available"))
	}
	selection := models.Roles.Main
	model := strings.TrimSpace(selection.APIModel)
	if model == "" {
		model = strings.TrimSpace(selection.Model)
	}
	if model == "" {
		return s.writeResponse(req.ID, nil, errors.New("no BYOK model is configured"))
	}

	var user strings.Builder
	if files := cleanCommitMessageFileList(params.Files); len(files) > 0 {
		user.WriteString("Staged files:\n")
		for _, file := range files {
			user.WriteString("- ")
			user.WriteString(file)
			user.WriteString("\n")
		}
		user.WriteString("\n")
	}
	user.WriteString("Staged diff:\n")
	user.WriteString(diff)

	ctx, cancel := context.WithTimeout(context.Background(), gitCommitMessageTimeout)
	defer cancel()
	ctx = providers.WithInferenceJournal(ctx, binding.InferenceJournalRuntime.ForOwner("git-commit-message"))
	response, err := providers.ExecuteChat(
		ctx,
		models.Main,
		providers.ChatRequest{
			Provider: selection.Provider,
			Model:    model,
			Messages: []providers.ChatMessage{
				{Role: "system", Content: gitCommitMessageSystemPrompt},
				{Role: "user", Content: user.String()},
			},
			Temperature:     models.Temperature,
			Effort:          selection.LegacyEffort,
			ProviderOptions: provideroptions.Clone(selection.ProviderOptions),
		},
		providers.InferenceOperationAuxiliary,
		providers.InferenceProfileInteractive,
	)
	if err != nil {
		return s.writeResponse(req.ID, nil, fmt.Errorf("BYOK commit message generation failed: %w", err))
	}
	message := cleanGeneratedCommitMessage(response.Content)
	if message == "" {
		return s.writeResponse(req.ID, nil, errors.New("BYOK model returned an empty commit message"))
	}
	return s.writeResponse(req.ID, GitCommitMessageResult{Message: message}, nil)
}

// cleanGeneratedCommitMessage strips reasoning fences, code fences, and
// surrounding quotes, then keeps the message as returned (subject plus
// optional body). Returns "" when nothing usable remains.
func cleanGeneratedCommitMessage(text string) string {
	text = stripThinkBlocks(text)
	lines := strings.Split(text, "\n")
	kept := make([]string, 0, len(lines))
	for _, line := range lines {
		trimmed := strings.TrimSpace(line)
		if strings.HasPrefix(trimmed, "```") {
			continue
		}
		kept = append(kept, line)
	}
	message := strings.TrimSpace(strings.Join(kept, "\n"))
	message = strings.Trim(message, "\"'`“”‘’")
	return strings.TrimSpace(message)
}

func cleanCommitMessageFileList(files []string) []string {
	const maxFiles = 200
	result := make([]string, 0, len(files))
	seen := make(map[string]struct{}, len(files))
	for _, file := range files {
		file = strings.TrimSpace(file)
		if file == "" {
			continue
		}
		if _, ok := seen[file]; ok {
			continue
		}
		seen[file] = struct{}{}
		result = append(result, file)
		if len(result) >= maxFiles {
			break
		}
	}
	return result
}
