package providers

import (
	"encoding/json"
	"strings"
)

// LoadableToolsFromToolSearchResult extracts deferred tool schemas from the
// shared tool_search result payload.
func LoadableToolsFromToolSearchResult(content string) []LoadableToolDefinition {
	var parsed struct {
		LoadableTools []LoadableToolDefinition `json:"loadable_tools"`
		Tools         []LoadableToolDefinition `json:"tools"`
	}
	if err := json.Unmarshal([]byte(strings.TrimSpace(content)), &parsed); err != nil {
		return nil
	}
	if len(parsed.LoadableTools) == 0 {
		return CloneLoadableToolDefinitions(parsed.Tools)
	}
	return CloneLoadableToolDefinitions(parsed.LoadableTools)
}

// AttachedDiscoveredToolsFromMessages returns tools stored as message metadata.
func AttachedDiscoveredToolsFromMessages(messages []ChatMessage) []LoadableToolDefinition {
	var out []LoadableToolDefinition
	for _, msg := range messages {
		out = MergeLoadableToolDefinitions(out, msg.DiscoveredTools)
	}
	return out
}

// CompactedDiscoveredToolsFromMessages returns discovered tools carried by
// summary/system messages after the original tool result position is gone.
func CompactedDiscoveredToolsFromMessages(messages []ChatMessage) []LoadableToolDefinition {
	var out []LoadableToolDefinition
	for _, msg := range messages {
		if !strings.EqualFold(msg.Role, "system") {
			continue
		}
		out = MergeLoadableToolDefinitions(out, msg.DiscoveredTools)
	}
	return out
}

// ToolSearchResultToolsFromMessages returns tools still represented by raw
// tool_search result messages in provider history.
func ToolSearchResultToolsFromMessages(messages []ChatMessage) []LoadableToolDefinition {
	var out []LoadableToolDefinition
	for _, msg := range messages {
		if !IsToolSearchResultMessage(msg) {
			continue
		}
		out = MergeLoadableToolDefinitions(out, LoadableToolsFromToolSearchResult(msg.Content))
	}
	return out
}

// DiscoveredToolsFromMessages returns every known deferred tool, combining
// explicit message metadata with raw tool_search results still in history.
func DiscoveredToolsFromMessages(messages []ChatMessage) []LoadableToolDefinition {
	out := AttachedDiscoveredToolsFromMessages(messages)
	return MergeLoadableToolDefinitions(out, ToolSearchResultToolsFromMessages(messages))
}

// DiscoveredToolNamesFromMessages returns a lookup set for tools that should be
// considered already loaded by provider-native tool discovery.
func DiscoveredToolNamesFromMessages(messages []ChatMessage) map[string]struct{} {
	tools := DiscoveredToolsFromMessages(messages)
	return LoadableToolNames(tools)
}

func CompactedDiscoveredToolNamesFromMessages(messages []ChatMessage) map[string]struct{} {
	return LoadableToolNames(CompactedDiscoveredToolsFromMessages(messages))
}

func ToolSearchResultToolNamesFromMessages(messages []ChatMessage) map[string]struct{} {
	return LoadableToolNames(ToolSearchResultToolsFromMessages(messages))
}

func LoadableToolNames(tools []LoadableToolDefinition) map[string]struct{} {
	if len(tools) == 0 {
		return map[string]struct{}{}
	}
	names := make(map[string]struct{}, len(tools))
	for _, tool := range tools {
		if name := strings.TrimSpace(tool.Name); name != "" {
			names[name] = struct{}{}
		}
	}
	return names
}

func IsToolSearchResultMessage(msg ChatMessage) bool {
	return msg.ToolResultKind == ToolCallKindToolSearch || strings.EqualFold(msg.Name, "tool_search")
}

func CloneLoadableToolDefinitions(tools []LoadableToolDefinition) []LoadableToolDefinition {
	if len(tools) == 0 {
		return nil
	}
	out := make([]LoadableToolDefinition, len(tools))
	for i, tool := range tools {
		out[i] = CloneLoadableToolDefinition(tool)
	}
	return out
}

func CloneLoadableToolDefinition(tool LoadableToolDefinition) LoadableToolDefinition {
	tool.InputSchema = cloneLoadableToolSchema(tool.InputSchema)
	return tool
}

func CloneChatMessages(messages []ChatMessage) []ChatMessage {
	if len(messages) == 0 {
		return nil
	}
	out := make([]ChatMessage, len(messages))
	for i, msg := range messages {
		out[i] = CloneChatMessage(msg)
	}
	return out
}

func CloneChatMessage(msg ChatMessage) ChatMessage {
	msg.Images = append([]InputImage(nil), msg.Images...)
	msg.Files = append([]InputFile(nil), msg.Files...)
	msg.ReasoningBlocks = append([]ReasoningBlock(nil), msg.ReasoningBlocks...)
	msg.ProviderItems = cloneProviderItems(msg.ProviderItems)
	msg.ToolCalls = CloneToolCalls(msg.ToolCalls)
	msg.DiscoveredTools = CloneLoadableToolDefinitions(msg.DiscoveredTools)
	if msg.ToolResult != nil {
		result := msg.ToolResult.Clone()
		msg.ToolResult = &result
	}
	return msg
}

func cloneProviderItems(items []ProviderItem) []ProviderItem {
	if len(items) == 0 {
		return nil
	}
	out := append([]ProviderItem(nil), items...)
	for i := range out {
		out[i].Fallback = CloneChatMessages(items[i].Fallback)
	}
	return out
}

func CloneToolCalls(calls []ToolCall) []ToolCall {
	if len(calls) == 0 {
		return nil
	}
	out := make([]ToolCall, len(calls))
	for i, call := range calls {
		out[i] = CloneToolCall(call)
	}
	return out
}

func CloneToolCall(call ToolCall) ToolCall {
	call.Display = call.Display.Clone()
	return call
}

func cloneLoadableToolSchema(schema map[string]any) map[string]any {
	if schema == nil {
		return nil
	}
	if cloned, ok := cloneSchemaValue(schema).(map[string]any); ok {
		return cloned
	}
	return nil
}

// MergeLoadableToolDefinitions deduplicates by name while preserving first-seen
// order. Later definitions replace earlier ones so refreshed schemas win.
func MergeLoadableToolDefinitions(sets ...[]LoadableToolDefinition) []LoadableToolDefinition {
	indexes := map[string]int{}
	var out []LoadableToolDefinition
	for _, set := range sets {
		for _, tool := range set {
			name := strings.TrimSpace(tool.Name)
			if name == "" {
				continue
			}
			clone := CloneLoadableToolDefinition(tool)
			clone.Name = name
			if i, ok := indexes[name]; ok {
				out[i] = clone
				continue
			}
			indexes[name] = len(out)
			out = append(out, clone)
		}
	}
	return out
}
