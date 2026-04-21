package providers

import (
	"fmt"
)

// NormalizeMessages ensures every assistant message that contains
// tool_calls is followed by a matching tool result, and removes any
// orphan tool results that lack a corresponding tool_call.
//
// This is aligned with Codex's ensure_call_outputs_present +
// remove_orphan_outputs in codex-rs/core/src/context_manager/normalize.rs.
// Missing outputs are synthesised with {"error":"aborted"} so the
// conversation stays valid for chat-completions APIs.
func NormalizeMessages(msgs []ChatMessage) []ChatMessage {
	if len(msgs) == 0 {
		return nil
	}

	// 1. Collect every tool-call ID declared by assistant messages.
	callIDs := make(map[string]struct{}, 8)
	for _, msg := range msgs {
		if msg.Role != "assistant" {
			continue
		}
		for _, tc := range msg.ToolCalls {
			if tc.ID != "" {
				callIDs[tc.ID] = struct{}{}
			}
		}
	}

	// 2. First pass — strip orphan tool results (no matching call).
	filtered := make([]ChatMessage, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role == "tool" && msg.ToolCallID != "" {
			if _, ok := callIDs[msg.ToolCallID]; !ok {
				continue // orphan
			}
		}
		filtered = append(filtered, msg)
	}

	// 3. Second pass — group each assistant with its contiguous tool
	//    results, reorder to match assistant.ToolCalls order, and pad
	//    missing outputs.
	out := make([]ChatMessage, 0, len(filtered))
	for i := 0; i < len(filtered); {
		msg := filtered[i]
		out = append(out, msg)

		if msg.Role != "assistant" || len(msg.ToolCalls) == 0 {
			i++
			continue
		}

		// Collect the contiguous tool block that follows this assistant.
		i++ // advance past assistant
		toolBlock := make([]ChatMessage, 0, len(msg.ToolCalls))
		for i < len(filtered) && filtered[i].Role == "tool" {
			toolBlock = append(toolBlock, filtered[i])
			i++
		}

		// Reorder / pad to match the assistant's tool_calls declaration.
		ordered := make([]ChatMessage, 0, len(msg.ToolCalls))
		for _, tc := range msg.ToolCalls {
			if tc.ID == "" {
				continue
			}
			found := -1
			for j, tm := range toolBlock {
				if tm.ToolCallID == tc.ID {
					found = j
					break
				}
			}
			if found >= 0 {
				ordered = append(ordered, toolBlock[found])
				// Remove consumed entry so it can't be reused.
				toolBlock = append(toolBlock[:found], toolBlock[found+1:]...)
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
		// Any remaining tool messages that didn't match this assistant's
		// calls are kept in-place. In a well-formed history this never
		// happens; dropping them silently would hide bugs.
		out = append(out, toolBlock...)
	}

	return out
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
