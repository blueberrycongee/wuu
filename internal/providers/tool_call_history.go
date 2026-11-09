package providers

import (
	"encoding/json"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/toolerrors"
)

// ValidateAssistantToolCalls rejects malformed provider tool-call metadata
// before it reaches the shared history machinery. Function-call arguments stay
// as provider-emitted strings here; individual tools parse them and return
// model-visible errors when the JSON is invalid.
func ValidateAssistantToolCalls(calls []ToolCall) error {
	seen := make(map[string]struct{}, len(calls))
	for i, call := range calls {
		if call.ID == "" {
			return fmt.Errorf("tool_call %d: missing id", i)
		}
		if _, ok := seen[call.ID]; ok {
			return fmt.Errorf("tool_call %d: duplicate id %q", i, call.ID)
		}
		seen[call.ID] = struct{}{}
	}
	return nil
}

// RepairToolCallHistory ensures every assistant message that contains
// tool_calls is followed immediately by matching tool results,
// repairing interleaved history when needed and removing orphan tool
// outputs that lack a corresponding tool_call.
func RepairToolCallHistory(msgs []ChatMessage) []ChatMessage {
	if len(msgs) == 0 {
		return nil
	}

	usedToolResults := make([]bool, len(msgs))

	// Rebuild the history with tool results attached directly to
	// their declaring assistant. Any interleaved non-tool messages are
	// kept, but they naturally fall after the assistant's tool block.
	out := make([]ChatMessage, 0, len(msgs))
	for i, msg := range msgs {
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
			if tm, ok := findToolResultForCall(msgs, usedToolResults, i, tc.ID); ok {
				if tm.ToolResultKind == "" {
					tm.ToolResultKind = tc.Kind
				}
				ordered = append(ordered, tm)
			} else {
				content := `{"error":"aborted"}`
				if err := validateToolCallArguments(tc); err != nil {
					content = invalidToolArgumentsContent(err)
				}
				ordered = append(ordered, ChatMessage{
					Role:           "tool",
					Name:           tc.Name,
					ToolCallID:     tc.ID,
					ToolResultKind: tc.Kind,
					Content:        content,
				})
			}
		}

		out = append(out, ordered...)
	}

	return out
}

func findToolResultForCall(msgs []ChatMessage, used []bool, assistantIndex int, callID string) (ChatMessage, bool) {
	for i := assistantIndex + 1; i < len(msgs); i++ {
		msg := msgs[i]
		if msg.Role == "assistant" {
			break
		}
		if msg.Role == "tool" && !used[i] && msg.ToolCallID == callID {
			used[i] = true
			return msg, true
		}
	}
	for i := 0; i < assistantIndex; i++ {
		msg := msgs[i]
		if msg.Role == "tool" && !used[i] && msg.ToolCallID == callID {
			used[i] = true
			return msg, true
		}
	}
	return ChatMessage{}, false
}

func validateToolCallArguments(call ToolCall) error {
	raw := strings.TrimSpace(call.Arguments)
	if raw == "" {
		return nil
	}
	if !json.Valid([]byte(raw)) {
		return fmt.Errorf("invalid arguments JSON for %s", call.Name)
	}
	return nil
}

func invalidToolArgumentsContent(err error) string {
	payload, marshalErr := json.Marshal(map[string]any{
		"error":      "invalid tool arguments: " + err.Error(),
		"error_kind": toolerrors.InvalidArguments,
		"ok":         false,
	})
	if marshalErr != nil {
		return `{"error":"invalid tool arguments","error_kind":"invalid_tool_arguments","ok":false}`
	}
	return string(payload)
}

func isInvalidToolArgumentsResult(content string) bool {
	var payload struct {
		OK        *bool  `json:"ok"`
		Error     string `json:"error"`
		ErrorKind string `json:"error_kind"`
	}
	if err := json.Unmarshal([]byte(content), &payload); err != nil {
		return false
	}
	if payload.OK == nil || *payload.OK || strings.TrimSpace(payload.Error) == "" {
		return false
	}
	if payload.ErrorKind != "" {
		return payload.ErrorKind == toolerrors.InvalidArguments
	}
	// Older persisted tool errors did not carry error_kind. A paired,
	// explicitly failed result is sufficient to prove that malformed arguments
	// were rejected; its human-readable wording is not part of the protocol.
	return true
}

func hasInvalidToolArgumentsResultInCurrentBlock(msgs []ChatMessage, assistantIndex int, callID string) bool {
	for i := assistantIndex + 1; i < len(msgs); i++ {
		msg := msgs[i]
		if msg.Role != "tool" {
			break
		}
		if msg.ToolCallID == callID && isInvalidToolArgumentsResult(msg.Content) {
			return true
		}
	}
	return false
}

// RepairAndValidateToolCallHistory repairs any sequence issues that can be
// safely synthesized client-side, then rejects histories that still
// violate tool ordering invariants.
func RepairAndValidateToolCallHistory(msgs []ChatMessage) ([]ChatMessage, error) {
	repaired := RepairToolCallHistory(msgs)
	if err := ValidateToolCallHistory(repaired); err != nil {
		return nil, fmt.Errorf("invalid message sequence after tool-call history repair: %w", err)
	}
	return repaired, nil
}

// ValidateToolCallHistory returns a non-nil error when the message slice
// violates tool-call history invariants that are cheap to check client-side.
// It is intended for diagnostics and tests, not as a gate (RepairToolCallHistory
// should be used to repair sequences instead).
func ValidateToolCallHistory(msgs []ChatMessage) error {
	for i, msg := range msgs {
		switch msg.Role {
		case "system":
			if i > 0 && msgs[i-1].Role != "system" {
				return fmt.Errorf("message %d: system message must precede all non-system messages", i)
			}
		case "assistant":
			if err := ValidateAssistantToolCalls(msg.ToolCalls); err != nil {
				return fmt.Errorf("message %d: invalid assistant tool_calls: %w", i, err)
			}
			for _, tc := range msg.ToolCalls {
				if err := validateToolCallArguments(tc); err != nil {
					if !hasInvalidToolArgumentsResultInCurrentBlock(msgs, i, tc.ID) {
						return fmt.Errorf("message %d: invalid assistant tool_calls: tool_call %q: %w", i, tc.ID, err)
					}
				}
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
