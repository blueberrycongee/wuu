package openai

import (
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// SupportsDynamicToolLoading identifies the Chat Completions model with a
// documented message-level tools protocol. Older Kimi models reject it.
// https://platform.kimi.ai/docs/guide/use-dynamic-tool-loading
func SupportsDynamicToolLoading(model string) bool {
	return strings.EqualFold(strings.TrimSpace(model), "kimi-k3")
}

// applyDynamicToolLoading keeps global tools fixed and replays declarations at
// their discovery positions. Kimi rejects messages containing both tools and
// content, so declarations must be separate system messages.
func applyDynamicToolLoading(payload *chatCompletionsRequest, req providers.ChatRequest) {
	if !providers.NativeToolDiscoveryEnabled(req.NativeDeferredToolDiscovery, req.Tools) || !SupportsDynamicToolLoading(req.Model) {
		return
	}
	deferred := providers.DiscoveredToolNamesFromMessages(req.Messages)
	for _, tool := range req.Tools {
		if tool.DeferLoading {
			deferred[tool.Name] = struct{}{}
		}
	}
	visible := payload.Tools[:0]
	for _, tool := range payload.Tools {
		if _, found := deferred[tool.Function.Name]; !found {
			visible = append(visible, tool)
		}
	}
	payload.Tools = visible

	messages := make([]chatMessage, 0, len(req.Messages))
	var pending []providers.LoadableToolDefinition
	for i, msg := range req.Messages {
		mapped := mapMessage(req.Model, msg)
		loadable := msg.DiscoveredTools
		if msg.Role == "tool" && providers.IsToolSearchResultMessage(msg) {
			loadable = providers.MergeLoadableToolDefinitions(loadable, providers.LoadableToolsFromToolSearchResult(msg.Content))
			if len(loadable) > 0 {
				names := make([]string, 0, len(loadable))
				for _, tool := range loadable {
					names = append(names, tool.Name)
				}
				result, _ := json.Marshal(map[string]any{"loaded_tools": names})
				mapped.Content = string(result)
			}
		}
		messages = append(messages, mapped)
		pending = providers.MergeLoadableToolDefinitions(pending, loadable)
		// Keep parallel tool results adjacent before exposing the next step's tools.
		if msg.Role == "tool" && i+1 < len(req.Messages) && req.Messages[i+1].Role == "tool" {
			continue
		}
		if len(pending) == 0 {
			continue
		}
		declaration := chatMessage{Role: "system", Tools: make([]toolDefinition, 0, len(pending))}
		strict := false
		for _, tool := range pending {
			declaration.Tools = append(declaration.Tools, toolDefinition{Type: "function", Function: toolFunctionDefinition{
				Name: tool.Name, Description: tool.Description,
				Parameters: providers.ToolInputSchemaForModel(req.Model, tool.InputSchema), Strict: &strict,
			}})
		}
		messages = append(messages, declaration)
		pending = nil
	}
	payload.Messages = messages
}
