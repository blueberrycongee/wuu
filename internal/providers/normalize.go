package providers

import (
	"fmt"
)

// NormalizeMessages ensures every assistant message that contains
// tool_calls is followed immediately by matching tool results,
// repairing interleaved history when needed and removing orphan tool
// outputs that lack a corresponding tool_call.
//
// This is aligned with Codex's ensure_call_outputs_present +
// remove_orphan_outputs, with one extra repair: if non-tool messages
// were mistakenly inserted between an assistant tool_call and its
// tool results, the tool results are pulled back up so the provider
// still receives a valid sequence.
func NormalizeMessages(msgs []ChatMessage) []ChatMessage {
	if len(msgs) == 0 {
		return nil
	}

	// 1. Collect every declared tool-call ID and the first matching
	// tool result we saw for it. Duplicate outputs are dropped so one
	// bad write cannot keep the history invalid forever.
	callIDs := make(map[string]struct{}, 8)
	toolResults := make(map[string]ChatMessage, 8)
	for _, msg := range msgs {
		switch msg.Role {
		case "assistant":
			for _, tc := range msg.ToolCalls {
				if tc.ID != "" {
					callIDs[tc.ID] = struct{}{}
				}
			}
		case "tool":
			if msg.ToolCallID == "" {
				continue
			}
			if _, ok := toolResults[msg.ToolCallID]; ok {
				continue
			}
			toolResults[msg.ToolCallID] = msg
		}
	}

	// 2. Rebuild the history with tool results attached directly to
	// their declaring assistant. Any interleaved non-tool messages are
	// kept, but they naturally fall after the assistant's tool block.
	out := make([]ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role == "tool" {
			continue
		}
		out = append(out, msg)

		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			continue
		}

		ordered := make([]ChatMessage, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" {
				continue
			}
			if _, ok := callIDs[tc.ID]; !ok {
				continue
			}
			if tm, ok := toolResults[tc.ID]; ok {
				ordered = append(ordered, tm)
				delete(toolResults, tc.ID)
			} else {
				ordered = append(ordered, ChatMessage{
					Role:       "tool",
					Name:       tc.Name,
					ToolCallID: tc.ID,
					Content:    `{"error":"aborted"}`,
				})
			}
		}

		out = append(out, ordered...)
	}

	return out
}

// NormalizeAndValidateMessages repairs any sequence issues that can be
// safely synthesized client-side, then rejects histories that still
// violate tool ordering invariants.
func NormalizeAndValidateMessages(msgs []ChatMessage) ([]ChatMessage, error) {
	normalized := NormalizeMessages(msgs)
	if err := ValidateMessageSequence(normalized); err != nil {
		return nil, fmt.Errorf("invalid message sequence after normalization: %w", err)
	}
	return normalized, nil
}

// ValidateMessageSequence returns a non-nil error when the message slice
// violates provider ordering rules that are cheap to check client-side.
// It is intended for diagnostics and tests, not as a gate (NormalizeMessages
// should be used to repair sequences instead).
func ValidateMessageSequence(msgs []ChatMessage) error {
	for i, msg := range msgs {
		switch msg.Role {
		case "system":
			if i > 0 && msgs[i-1].Role != "system" {
				return fmt.Errorf("message %d: system message must precede all non-system messages", i)
			}
		case "tool":
			if i == 0 {
				return fmt.Errorf("message %d: tool message cannot be first", i)
			}
			if msgs[i-1].Role != "assistant" && msgs[i-1].Role != "tool" {
				return fmt.Errorf("message %d: tool message must follow an assistant or tool message", i)
			}
			if msg.ToolCallID == "" {
				return fmt.Errorf("message %d: tool message missing tool_call_id", i)
			}
			// Ensure the tool_call_id matches a call in the most recent
			// assistant message that declared tool_calls.
			found := false
			for j := i - 1; j >= 0; j-- {
				if msgs[j].Role != "assistant" && msgs[j].Role != "tool" {
					break
				}
				if msgs[j].Role == "assistant" {
					for _, tc := range msgs[j].ToolCalls {
						if tc.ID == msg.ToolCallID {
							found = true
							break
						}
					}
					break
				}
			}
			if !found {
				return fmt.Errorf("message %d: tool message with call_id %q has no matching assistant tool_call", i, msg.ToolCallID)
			}
		}
	}
	return nil
}
