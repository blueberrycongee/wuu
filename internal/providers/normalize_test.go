package providers

import (
	"testing"
)

func TestNormalizeMessages_empty(t *testing.T) {
	got := NormalizeMessages(nil)
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
	got = NormalizeMessages([]ChatMessage{})
	if got != nil {
		t.Fatalf("expected nil, got %v", got)
	}
}

func TestNormalizeMessages_noToolCalls(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
	}
	got := NormalizeMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d", len(got))
	}
}

func TestNormalizeMessages_orphanToolRemoved(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
		{Role: "tool", ToolCallID: "call_orphan", Content: "orphan"},
	}
	got := NormalizeMessages(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(got), roles(got))
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("expected tool call_1, got %s", got[2].ToolCallID)
	}
}

func TestNormalizeMessages_missingOutputInserted(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file"},
			{ID: "call_2", Name: "grep"},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}
	got := NormalizeMessages(msgs)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(got), roles(got))
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("expected call_1 at index 2, got %s", got[2].ToolCallID)
	}
	if got[3].Role != "tool" || got[3].ToolCallID != "call_2" {
		t.Fatalf("expected synthetic tool call_2 at index 3, got %+v", got[3])
	}
	if got[3].Content != `{"error":"aborted"}` {
		t.Fatalf("expected aborted content, got %q", got[3].Content)
	}
}

func TestNormalizeMessages_multipleAssistants(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_a", Name: "a"}}},
		{Role: "tool", ToolCallID: "call_a", Content: "a_ok"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_b", Name: "b"}}},
		// missing tool for call_b
	}
	got := NormalizeMessages(msgs)
	if len(got) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(got), roles(got))
	}
	if got[4].Role != "tool" || got[4].ToolCallID != "call_b" {
		t.Fatalf("expected synthetic tool call_b at index 4, got %+v", got[4])
	}
}

func TestNormalizeMessages_allOutputsPresent(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "a"},
			{ID: "call_2", Name: "b"},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "a"},
		{Role: "tool", ToolCallID: "call_2", Content: "b"},
	}
	got := NormalizeMessages(msgs)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d", len(got))
	}
}

func TestNormalizeMessages_noDuplicateSynthetic(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "a"}}},
		// missing tool
		{Role: "assistant", Content: "done"},
	}
	got := NormalizeMessages(msgs)
	// First assistant gets synthetic; second assistant has no calls.
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(got), roles(got))
	}
	count := 0
	for _, m := range got {
		if m.Role == "tool" && m.ToolCallID == "call_1" {
			count++
		}
	}
	if count != 1 {
		t.Fatalf("expected exactly 1 synthetic tool for call_1, got %d", count)
	}
}

func TestValidateMessageSequence_ok(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "a"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}
	if err := ValidateMessageSequence(msgs); err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestValidateMessageSequence_orphanTool(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "tool", ToolCallID: "call_orphan", Content: "x"},
	}
	if err := ValidateMessageSequence(msgs); err == nil {
		t.Fatalf("expected error for orphan tool")
	}
}

func TestValidateMessageSequence_toolAfterUser(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "a"}}},
		{Role: "user", Content: "mid"},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}
	if err := ValidateMessageSequence(msgs); err == nil {
		t.Fatalf("expected error for tool after user")
	}
}

func roles(msgs []ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}
