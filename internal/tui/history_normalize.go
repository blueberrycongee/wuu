package tui

import "github.com/blueberrycongee/wuu/internal/providers"

// normalizeChatHistory defensively repairs tool_use/tool_result ordering
// in chat history before it is sent to the API. It:
//   1. Removes orphan tool_results that have no matching tool_use
//   2. Moves any interleaved messages out from between a tool_use and its
//      tool_result (e.g. worker results that landed in the wrong spot)
//   3. Inserts synthetic error placeholders for tool_calls missing results
//
// This is the defense-in-depth layer that protects against corrupted
// history (from the pre-fix interleaving bug) and any future ordering bugs.
func normalizeChatHistory(msgs []providers.ChatMessage) []providers.ChatMessage {
	if len(msgs) == 0 {
		return nil
	}

	allToolUseIDs := make(map[string]struct{})
	for _, msg := range msgs {
		for _, tc := range msg.ToolCalls {
			allToolUseIDs[tc.ID] = struct{}{}
		}
	}

	toolResults := make(map[string]providers.ChatMessage)
	for _, msg := range msgs {
		if msg.Role != "tool" {
			continue
		}
		if _, ok := allToolUseIDs[msg.ToolCallID]; !ok {
			continue
		}
		toolResults[msg.ToolCallID] = msg
	}

	var result []providers.ChatMessage
	for _, msg := range msgs {
		if msg.Role == "tool" {
			continue
		}

		result = append(result, msg)

		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}

		for _, tc := range msg.ToolCalls {
			if tr, ok := toolResults[tc.ID]; ok {
				result = append(result, tr)
				delete(toolResults, tc.ID)
			} else {
				result = append(result, providers.ChatMessage{
					Role:       "tool",
					ToolCallID: tc.ID,
					Content:    "[Error: tool result is missing — the stream was interrupted or the result was lost]",
				})
			}
		}
	}

	return result
}
