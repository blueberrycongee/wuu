package compactionservice

import (
	"encoding/json"
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/contextbudget"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/workingnotes"
	"strings"
)

const FreshContextTargetTokens = 50_000
const freshContextTransformReserveTokens = 4_000
const newContextToolName = "new_context"

var ErrFreshContextTooLarge = errors.New("fresh context cannot fit the target budget")
var ErrFreshContextNotSmaller = errors.New("fresh context would not shrink active history")

// Temporary extraction: remove the agent copy after selected window consumers migrate.
func buildFreshContext(messages []providers.ChatMessage, historyHeadSeq, fixedTokens, targetTokens int, policies ...providers.MediaInputPolicy) ([]providers.ChatMessage, error) {
	if targetTokens <= 0 {
		targetTokens = FreshContextTargetTokens
	}
	fixedTokens = max(0, fixedTokens)
	var task *providers.ChatMessage
	for index := len(messages) - 1; index >= 0; index-- {
		if strings.EqualFold(messages[index].Role, "user") && !messages[index].Hidden {
			task = &messages[index]
			break
		}
	}
	recovery := fmt.Sprintf("[Context window]\nThe previous active transcript is archived through History Seq %d. Files, processes and other environment state are unchanged. Read your persistent working notes with notes. Recover missing decisions, progress and tool outcomes with history_search and history_read before repeating actions. Start at Seq %d if no useful note exists.", historyHeadSeq, max(1, historyHeadSeq-24))
	if task != nil && task.Seq > 0 {
		recovery += fmt.Sprintf(" The latest user instruction is at Seq %d.", task.Seq)
	}
	request := acceptedNewContextRequest(messages)
	if request != nil && request.Checkpoint != nil {
		recovery += " A saved checkpoint read is supplied in wuu_context_checkpoint. Call notes with those arguments directly (through tools in run_code when PTC is enabled); discovery is unnecessary. If the collection revision changed, reread the same path without the stale revision and reconcile the current note before continuing."
	}
	replacement := append(freshContextSystemPrefix(messages), providers.ChatMessage{
		Role: "system", Content: recovery, Hidden: true, Origin: "internal", Cause: "fresh_context",
	})
	if request != nil && request.Checkpoint != nil {
		// Keep model-authored note addresses as data, not system instructions.
		read, _ := json.Marshal(map[string]any{
			"tool": "notes",
			"arguments": map[string]any{
				"action": "read", "path": request.Checkpoint.Path, "revision": request.Checkpoint.Revision,
			},
		})
		replacement = append(replacement, providers.ChatMessage{
			Role: "user", Name: "wuu_context_checkpoint", Content: string(read),
			Hidden: true, Origin: "internal", Cause: "fresh_context",
		})
	}
	budget := targetTokens - fixedTokens - min(freshContextTransformReserveTokens, targetTokens/10)
	if estimateFreshContextMessages(replacement, policies...) > budget {
		return nil, ErrFreshContextTooLarge
	}
	if task != nil {
		candidate := append(providers.CloneChatMessages(replacement), providers.CloneChatMessage(*task))
		if estimateFreshContextMessages(candidate, policies...) <= budget && estimateFreshContextMessages(candidate, policies...) < estimateFreshContextMessages(messages, policies...) {
			replacement = candidate
		} else {
			// A recovery address cannot substitute for required native evidence.
			for _, image := range task.Images {
				if image.Required {
					return nil, ErrFreshContextTooLarge
				}
			}
			for _, file := range task.Files {
				if file.Required {
					return nil, ErrFreshContextTooLarge
				}
			}
		}
	}
	if estimateFreshContextMessages(replacement, policies...) >= estimateFreshContextMessages(messages, policies...) {
		return nil, ErrFreshContextNotSmaller
	}
	if err := providers.ValidateToolCallHistory(replacement); err != nil {
		return nil, fmt.Errorf("fresh context has invalid tool-call history: %w", err)
	}
	return replacement, nil
}

func freshContextSystemPrefix(messages []providers.ChatMessage) []providers.ChatMessage {
	end := 0
	for end < len(messages) && strings.EqualFold(strings.TrimSpace(messages[end].Role), "system") {
		if compact.IsConversationSummaryContent(messages[end].Content) || messages[end].Cause == "fresh_context" {
			break
		}
		end++
	}
	return providers.CloneChatMessages(messages[:end])
}

func estimateFreshContextMessages(messages []providers.ChatMessage, policies ...providers.MediaInputPolicy) int {
	var policy providers.MediaInputPolicy
	if len(policies) > 0 {
		policy = policies[0]
	}
	tokens := compact.EstimateMessagesTokens(providers.ProjectMediaForPolicy(providers.ApplyToolResultProjections(messages), policy))
	for _, message := range messages {
		if len(message.DiscoveredTools) != 0 {
			if encoded, err := json.Marshal(message.DiscoveredTools); err == nil {
				tokens += contextbudget.EstimateJSONTokens(string(encoded))
			}
		}
	}
	return tokens
}

type newContextRequest struct {
	Requested  bool                     `json:"requested"`
	Checkpoint *workingnotes.Checkpoint `json:"checkpoint,omitempty"`
}

func acceptedNewContextRequest(results []providers.ChatMessage) *newContextRequest {
	// Only the latest tool batch can request a transition, even if steering
	// arrived after it. Do not reuse a reference from an earlier assistant step.
	// Failed or truncated calls have no control effect.
	for index := len(results) - 1; index >= 0 && results[index].Role != "assistant"; index-- {
		message := results[index]
		if message.Role != "tool" || message.Name != newContextToolName || message.ToolResult == nil || message.ToolResult.IsError {
			continue
		}
		var signal newContextRequest
		if json.Unmarshal([]byte(message.ToolResult.TextProjection()), &signal) == nil && signal.Requested {
			return &signal
		}
	}
	return nil
}

// BuildWindow is the native policy used by the selected service and explicit
// local embeddings. Production consumers use Client.Window instead.
func BuildWindow(in WindowRequest) (Replacement, error) {
	messages, err := buildFreshContext(in.Messages, in.HistoryHead, in.FixedTokens, in.TargetTokens, in.MediaInput)
	return Replacement{Messages: messages}, err
}
