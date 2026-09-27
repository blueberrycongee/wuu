package providers

import (
	"fmt"
	"hash/fnv"
	"strings"
)

// PrepareMessagesForModelRequest applies shared tool-call history repair plus
// request-only model/provider compatibility transforms. It deliberately does
// not mutate stored history; clients call this just before lowering to wire
// format.
func PrepareMessagesForModelRequest(model string, msgs []ChatMessage) ([]ChatMessage, error) {
	return PrepareMessagesForProviderRequest("", model, msgs)
}

// PrepareMessagesForProviderRequest also enforces provider provenance for
// provider-native state. Provider adapters should prefer this entry point
// whenever ChatRequest.Provider is available.
func PrepareMessagesForProviderRequest(provider, model string, msgs []ChatMessage) ([]ChatMessage, error) {
	return PrepareMessagesForProviderRequestWithPolicy(provider, model, msgs, MediaInputPolicy{})
}

// PrepareMessagesForProviderRequestWithPolicy is PrepareMessagesForProviderRequest
// plus media admission: media kinds the policy marks unsupported are stripped
// and replaced by a short marker after rich tool results become observations,
// before model compatibility transforms and final validation.
// Auto and supported kinds pass through unchanged.
func PrepareMessagesForProviderRequestWithPolicy(provider, model string, msgs []ChatMessage, policy MediaInputPolicy) ([]ChatMessage, error) {
	msgs = ResolveProviderHistory(msgs, provider, "")
	repaired, err := RepairAndValidateToolCallHistory(msgs)
	if err != nil {
		return nil, err
	}
	projected := ApplyToolResultProjections(repaired)
	if err := ValidateRequiredMedia(projected, policy); err != nil {
		return nil, err
	}
	// Rich history reads create observation media too. Apply the same policy
	// after projection so switching to a text-only model cannot reintroduce it.
	projected = ProjectMediaForPolicy(projected, policy)
	compatible := ApplyProviderModelMessageCompatibility(provider, model, projected)
	if err := ValidateToolCallHistory(compatible); err != nil {
		return nil, fmt.Errorf("invalid message sequence after model compatibility: %w", err)
	}
	return compatible, nil
}

// ResolveProviderHistory expands the portable fallback behind opaque native
// checkpoints that are incompatible with the active provider state scope.
// Empty scope means the caller can only enforce provider provenance.
func ResolveProviderHistory(msgs []ChatMessage, provider, scope string) []ChatMessage {
	provider = strings.TrimSpace(provider)
	scope = strings.TrimSpace(scope)
	out := make([]ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		resolved := false
		for _, item := range msg.ProviderItems {
			providerMismatch := item.Provider != "" && strings.TrimSpace(item.Provider) != provider
			scopeMismatch := scope != "" && item.Scope != "" && strings.TrimSpace(item.Scope) != scope
			if (providerMismatch || scopeMismatch) && len(item.Fallback) > 0 {
				out = append(out, CloneChatMessages(item.Fallback)...)
				resolved = true
				break
			}
		}
		if !resolved {
			out = append(out, CloneChatMessage(msg))
		}
	}
	return out
}

// ApplyModelMessageCompatibility mirrors the request-only message transforms
// in OpenCode's ProviderTransform.message that are relevant to wuu's Go
// clients.
func ApplyModelMessageCompatibility(model string, msgs []ChatMessage) []ChatMessage {
	return ApplyProviderModelMessageCompatibility("", model, msgs)
}

// ApplyProviderModelMessageCompatibility removes request-only native state
// produced by a different configured provider or model while retaining the
// visible conversation.
func ApplyProviderModelMessageCompatibility(provider, model string, msgs []ChatMessage) []ChatMessage {
	if len(msgs) == 0 {
		return nil
	}
	out := cloneMessagesForRequest(msgs)
	out = sanitizeMessageText(out)
	out = dropForeignProviderModelState(provider, model, out)

	lower := strings.ToLower(strings.TrimSpace(model))
	switch {
	case isMistralModel(lower):
		out = rewriteToolCallIDs(out, scrubMistralToolCallID)
		out = insertMistralToolUserSeparator(out)
	case isClaudeModel(lower):
		out = rewriteToolCallIDs(out, scrubClaudeToolCallID)
	}
	return out
}

func dropForeignProviderModelState(provider, model string, msgs []ChatMessage) []ChatMessage {
	currentProvider := strings.TrimSpace(provider)
	currentModel := strings.TrimSpace(model)
	for i := range msgs {
		foreignMessage := providerStateOriginMismatch(
			currentProvider,
			currentModel,
			msgs[i].ProviderItemProvider,
			msgs[i].ProviderItemModel,
		)
		if foreignMessage {
			msgs[i].Content = downgradeReasoningToAssistantText(msgs[i])
			msgs[i].ProviderItemID = ""
			msgs[i].ReasoningContent = ""
			msgs[i].ReasoningBlocks = nil
			msgs[i].DiscoveredTools = nil
		}
		for j := range msgs[i].ToolCalls {
			call := &msgs[i].ToolCalls[j]
			if foreignMessage || providerStateOriginMismatch(currentProvider, currentModel, call.ProviderItemProvider, call.ProviderItemModel) {
				call.ProviderItemID = ""
			}
		}
	}
	return msgs
}

// downgradeReasoningToAssistantText follows the cross-model behavior used by
// other BYOK clients: readable reasoning stays in the conversation as plain
// assistant text, while the caller removes signatures and opaque payloads.
func downgradeReasoningToAssistantText(msg ChatMessage) string {
	readable := make([]string, 0, len(msg.ReasoningBlocks))
	for _, block := range msg.ReasoningBlocks {
		if text := strings.TrimSpace(block.Thinking); text != "" {
			readable = append(readable, text)
		}
	}
	reasoning := strings.Join(readable, "\n")
	if reasoning == "" {
		reasoning = strings.TrimSpace(msg.ReasoningContent)
	}
	if reasoning == "" {
		return msg.Content
	}
	if strings.TrimSpace(msg.Content) == "" {
		return reasoning
	}
	return reasoning + "\n\n" + msg.Content
}

func providerStateOriginMismatch(currentProvider, currentModel, originProvider, originModel string) bool {
	originProvider = strings.TrimSpace(originProvider)
	originModel = strings.TrimSpace(originModel)
	if currentProvider != "" && originProvider != "" && !strings.EqualFold(originProvider, currentProvider) {
		return true
	}
	return currentModel != "" && originModel != "" && !strings.EqualFold(originModel, currentModel)
}

func cloneMessagesForRequest(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, len(msgs))
	for i, msg := range msgs {
		out[i] = msg
		if len(msg.Images) > 0 {
			out[i].Images = append([]InputImage(nil), msg.Images...)
		}
		if len(msg.Files) > 0 {
			out[i].Files = append([]InputFile(nil), msg.Files...)
		}
		if len(msg.ToolCalls) > 0 {
			out[i].ToolCalls = append([]ToolCall(nil), msg.ToolCalls...)
		}
		if msg.ToolResult != nil {
			result := msg.ToolResult.Clone()
			out[i].ToolResult = &result
		}
		if len(msg.ReasoningBlocks) > 0 {
			out[i].ReasoningBlocks = append([]ReasoningBlock(nil), msg.ReasoningBlocks...)
		}
	}
	return out
}

func sanitizeMessageText(msgs []ChatMessage) []ChatMessage {
	for i := range msgs {
		msgs[i].Content = SanitizeSurrogates(msgs[i].Content)
		msgs[i].ReasoningContent = SanitizeSurrogates(msgs[i].ReasoningContent)
		for j := range msgs[i].ReasoningBlocks {
			msgs[i].ReasoningBlocks[j].Thinking = SanitizeSurrogates(msgs[i].ReasoningBlocks[j].Thinking)
		}
		for j := range msgs[i].Files {
			msgs[i].Files[j].Filename = SanitizeSurrogates(msgs[i].Files[j].Filename)
		}
	}
	return msgs
}

// SanitizeSurrogates replaces UTF-16 surrogate code points with U+FFFD.
// Several provider frontends reject lone surrogates even when Go can encode
// them into JSON strings.
func SanitizeSurrogates(content string) string {
	return strings.Map(func(r rune) rune {
		if r >= 0xD800 && r <= 0xDFFF {
			return '\uFFFD'
		}
		return r
	}, content)
}

func rewriteToolCallIDs(msgs []ChatMessage, scrub func(string) string) []ChatMessage {
	for i := range msgs {
		if msgs[i].ToolCallID != "" {
			msgs[i].ToolCallID = scrub(msgs[i].ToolCallID)
		}
		for j := range msgs[i].ToolCalls {
			msgs[i].ToolCalls[j].ID = scrub(msgs[i].ToolCalls[j].ID)
		}
	}
	return msgs
}

func scrubClaudeToolCallID(id string) string {
	return strings.Map(func(r rune) rune {
		if (r >= 'a' && r <= 'z') || (r >= 'A' && r <= 'Z') || (r >= '0' && r <= '9') || r == '_' || r == '-' {
			return r
		}
		return '_'
	}, id)
}

// scrubMistralToolCallID maps an arbitrary tool-call ID onto Mistral's
// required 9-alphanumeric form. An ID that is already valid passes through;
// everything else is hashed rather than prefix-truncated, because prefix
// truncation collapses IDs sharing their first nine alphanumerics — e.g.
// Moonshot-style "functions.name:0" and "functions.name:1" both truncated to
// "functions" — into duplicates that fail history validation on every retry,
// permanently fail-closing the conversation on Mistral models.
func scrubMistralToolCallID(id string) string {
	if len(id) == 9 && isMistralToolCallID(id) {
		return id
	}
	h := fnv.New64a()
	_, _ = h.Write([]byte(id))
	sum := h.Sum64()
	const digits = "0123456789abcdefghijklmnopqrstuvwxyz"
	var b [9]byte
	for i := len(b) - 1; i >= 0; i-- {
		b[i] = digits[sum%36]
		sum /= 36
	}
	return string(b[:])
}

func isMistralToolCallID(id string) bool {
	for _, r := range id {
		if (r < 'a' || r > 'z') && (r < 'A' || r > 'Z') && (r < '0' || r > '9') {
			return false
		}
	}
	return true
}

func insertMistralToolUserSeparator(msgs []ChatMessage) []ChatMessage {
	out := make([]ChatMessage, 0, len(msgs)+1)
	for i, msg := range msgs {
		out = append(out, msg)
		if msg.Role == "tool" && i+1 < len(msgs) && msgs[i+1].Role == "user" {
			out = append(out, ChatMessage{Role: "assistant", Content: "Done."})
		}
	}
	return out
}

func isClaudeModel(model string) bool {
	return strings.Contains(model, "claude") || strings.Contains(model, "anthropic")
}

func isMistralModel(model string) bool {
	return strings.Contains(model, "mistral") || strings.Contains(model, "devstral")
}

func IsDeepSeekModel(model string) bool {
	model = strings.ToLower(strings.TrimSpace(model))
	return strings.Contains(model, "deepseek")
}
