package providers

import (
	"strings"
	"testing"
)

func TestValidateAssistantToolCalls_rejectsMissingID(t *testing.T) {
	if err := ValidateAssistantToolCalls([]ToolCall{{ID: "", Name: "a"}}); err == nil {
		t.Fatal("expected missing id error")
	}
}

func TestValidateAssistantToolCalls_rejectsDuplicateID(t *testing.T) {
	if err := ValidateAssistantToolCalls([]ToolCall{
		{ID: "call_1", Name: "a"},
		{ID: "call_1", Name: "b"},
	}); err == nil {
		t.Fatal("expected duplicate id error")
	}
}

func TestValidateAssistantToolCalls_allowsRawFunctionArguments(t *testing.T) {
	err := ValidateAssistantToolCalls([]ToolCall{{ID: "call_1", Name: "update_todo", Arguments: `{"todos": `}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepairToolCallHistory_orphanToolRemoved(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
		{Role: "tool", ToolCallID: "call_orphan", Content: "orphan"},
	}
	got := RepairToolCallHistory(msgs)
	if len(got) != 3 {
		t.Fatalf("expected 3 messages, got %d: %+v", len(got), roles(got))
	}
	if got[2].ToolCallID != "call_1" {
		t.Fatalf("expected tool call_1, got %s", got[2].ToolCallID)
	}
}

func TestRepairToolCallHistory_missingOutputInserted(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{
			{ID: "call_1", Name: "read_file"},
			{ID: "call_2", Name: "tool_search", Kind: ToolCallKindToolSearch},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}
	got := RepairToolCallHistory(msgs)
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
	if got[3].ToolResultKind != ToolCallKindToolSearch {
		t.Fatalf("expected synthetic output to keep tool_search kind, got %+v", got[3])
	}
}

func TestRepairToolCallHistory_keepsRecoverableInvalidToolArguments(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "continue"},
		{
			Role:    "assistant",
			Content: "I will update the TODO list.",
			ToolCalls: []ToolCall{{
				ID:        "call_todo",
				Name:      "update_todo",
				Arguments: `{"todos": `,
			}},
		},
		{
			Role:       "tool",
			Name:       "update_todo",
			ToolCallID: "call_todo",
			Content:    `{"error":"request body ended before the object closed","error_kind":"invalid_tool_arguments","ok":false}`,
		},
		{Role: "user", Content: "continue again"},
	}
	got, err := RepairAndValidateToolCallHistory(msgs)
	if err != nil {
		t.Fatalf("RepairAndValidateToolCallHistory: %v", err)
	}
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(got), roles(got))
	}
	if len(got[1].ToolCalls) != 1 {
		t.Fatalf("expected invalid tool call to be retained, got %+v", got[1].ToolCalls)
	}
	if got[2].ToolCallID != "call_todo" || got[3].Content != "continue again" {
		t.Fatalf("unexpected repaired messages: %+v", got)
	}
}

func TestRepairToolCallHistory_keepsLegacyFailedResultForInvalidToolArguments(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "continue"},
		{Role: "assistant", ToolCalls: []ToolCall{{
			ID: "call_write", Name: "write_file", Arguments: `{"path":"page.html","content":"truncated`,
		}}},
		{
			Role:       "tool",
			Name:       "write_file",
			ToolCallID: "call_write",
			Content:    `{"error":"tool \"write_file\" arguments are invalid JSON","ok":false}`,
		},
	}

	got, err := RepairAndValidateToolCallHistory(msgs)
	if err != nil {
		t.Fatalf("RepairAndValidateToolCallHistory: %v", err)
	}
	if len(got) != 3 || got[2].ToolCallID != "call_write" {
		t.Fatalf("expected paired legacy failure to remain recoverable, got %+v", got)
	}
}

func TestRepairToolCallHistory_synthesizesInvalidToolArgumentsOutput(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "continue"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:        "call_todo",
				Name:      "update_todo",
				Arguments: `{"todos": `,
			}},
		},
	}
	got, err := RepairAndValidateToolCallHistory(msgs)
	if err != nil {
		t.Fatalf("RepairAndValidateToolCallHistory: %v", err)
	}
	if len(got) != 3 {
		t.Fatalf("expected synthetic result, got %d messages: %+v", len(got), roles(got))
	}
	if got[2].Role != "tool" || got[2].ToolCallID != "call_todo" {
		t.Fatalf("expected synthetic tool result, got %+v", got[2])
	}
	if !strings.Contains(got[2].Content, `"error_kind":"invalid_tool_arguments"`) {
		t.Fatalf("expected invalid arguments content, got %q", got[2].Content)
	}
}

func TestRepairToolCallHistory_keepsInvalidToolArgumentsWithoutMatchingToolError(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "continue"},
		{
			Role: "assistant",
			ToolCalls: []ToolCall{{
				ID:        "call_todo",
				Name:      "update_todo",
				Arguments: `{"todos": `,
			}},
		},
		{Role: "tool", Name: "update_todo", ToolCallID: "call_todo", Content: `{"error":"different failure","error_kind":"tool","ok":false}`},
	}
	_, err := RepairAndValidateToolCallHistory(msgs)
	if err == nil {
		t.Fatal("expected invalid tool call to remain invalid")
	}
	if !strings.Contains(err.Error(), "invalid arguments JSON") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestRepairToolCallHistory_multipleAssistants(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_a", Name: "a"}}},
		{Role: "tool", ToolCallID: "call_a", Content: "a_ok"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_b", Name: "b"}}},
		// missing tool for call_b
	}
	got := RepairToolCallHistory(msgs)
	if len(got) != 5 {
		t.Fatalf("expected 5 messages, got %d: %+v", len(got), roles(got))
	}
	if got[4].Role != "tool" || got[4].ToolCallID != "call_b" {
		t.Fatalf("expected synthetic tool call_b at index 4, got %+v", got[4])
	}
}

func TestRepairToolCallHistory_interleavedUserMovedAfterToolResults(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "a"}}},
		{Role: "user", Content: `{"type":"agent_result","agent_id":"worker-1","result":"done"}`},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}
	got := RepairToolCallHistory(msgs)
	if len(got) != 4 {
		t.Fatalf("expected 4 messages, got %d: %+v", len(got), roles(got))
	}
	if got[1].Role != "assistant" || got[2].Role != "tool" || got[3].Role != "user" {
		t.Fatalf("expected assistant/tool/user ordering after repair, got %+v", got)
	}
	if got[2].ToolCallID != "call_1" || got[3].Content != `{"type":"agent_result","agent_id":"worker-1","result":"done"}` {
		t.Fatalf("unexpected repaired messages: %+v", got)
	}
}

func TestRepairToolCallHistory_noDuplicateSynthetic(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "a"}}},
		// missing tool
		{Role: "assistant", Content: "done"},
	}
	got := RepairToolCallHistory(msgs)
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

func TestRepairAndValidateToolCallHistory_allowsDuplicateToolCallIDsAcrossTurns(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "a"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "first"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "b"}}},
		{Role: "tool", ToolCallID: "call_1", Content: "second"},
	}
	got, err := RepairAndValidateToolCallHistory(msgs)
	if err != nil {
		t.Fatalf("RepairAndValidateToolCallHistory: %v", err)
	}
	if err := ValidateToolCallHistory(got); err != nil {
		t.Fatalf("ValidateToolCallHistory: %v: %+v", err, got)
	}
	if gotOrder := roleToolOrder(got); gotOrder != "user,assistant,tool:call_1,assistant,tool:call_1" {
		t.Fatalf("unexpected order: got %s: %+v", gotOrder, got)
	}
	contents := toolContents(got, "call_1")
	if len(contents) != 2 || contents[0] != "first" || contents[1] != "second" {
		t.Fatalf("unexpected duplicate call contents: %+v", contents)
	}
}

func TestRepairAndValidateToolCallHistory_usesLocalInvalidArgumentResultForDuplicateID(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":"a"}`}}},
		{Role: "tool", ToolCallID: "call_1", Content: "first"},
		{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "read", Arguments: `{"path":`}}},
		{
			Role:       "tool",
			ToolCallID: "call_1",
			Content:    `{"error":"invalid tool arguments: invalid arguments JSON for read","ok":false}`,
		},
	}
	got, err := RepairAndValidateToolCallHistory(msgs)
	if err != nil {
		t.Fatalf("RepairAndValidateToolCallHistory: %v", err)
	}
	contents := toolContents(got, "call_1")
	if len(contents) != 2 || contents[0] != "first" || !strings.Contains(contents[1], "invalid tool arguments") {
		t.Fatalf("unexpected duplicate call contents: %+v", contents)
	}
}

func TestRepairAndValidateToolCallHistory_asyncTimingScenarios(t *testing.T) {
	tests := []struct {
		name          string
		msgs          []ChatMessage
		want          string
		wantContentBy map[string]string
	}{
		{
			name: "parallel tool outputs finish out of order",
			msgs: []ChatMessage{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []ToolCall{
					{ID: "call_1", Name: "read_file"},
					{ID: "call_2", Name: "grep"},
				}},
				{Role: "tool", ToolCallID: "call_2", Content: "grep ok"},
				{Role: "tool", ToolCallID: "call_1", Content: "read ok"},
			},
			want: "user,assistant,tool:call_1,tool:call_2",
		},
		{
			name: "worker message arrives between parallel tool outputs",
			msgs: []ChatMessage{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []ToolCall{
					{ID: "call_1", Name: "read_file"},
					{ID: "call_2", Name: "grep"},
				}},
				{Role: "tool", ToolCallID: "call_1", Content: "read ok"},
				{Role: "user", Content: `{"type":"agent_result","agent_id":"worker-1","result":"done"}`},
				{Role: "tool", ToolCallID: "call_2", Content: "grep ok"},
			},
			want: "user,assistant,tool:call_1,tool:call_2,user",
		},
		{
			name: "worker message arrives before first tool output",
			msgs: []ChatMessage{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "spawn_agent"}}},
				{Role: "user", Content: `{"type":"agent_result","agent_id":"worker-1","result":"done"}`},
				{Role: "tool", ToolCallID: "call_1", Content: "worker ok"},
			},
			want: "user,assistant,tool:call_1,user",
		},
		{
			name: "orphan and retry outputs are dropped",
			msgs: []ChatMessage{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file"}}},
				{Role: "tool", ToolCallID: "call_orphan", Content: "orphan"},
				{Role: "tool", ToolCallID: "call_1", Content: "first result"},
				{Role: "tool", ToolCallID: "call_1", Content: "retry result"},
				{Role: "user", Content: "after"},
			},
			want: "user,assistant,tool:call_1,user",
			wantContentBy: map[string]string{
				"call_1": "first result",
			},
		},
		{
			name: "missing earlier output is synthesized before later output",
			msgs: []ChatMessage{
				{Role: "user", Content: "inspect"},
				{Role: "assistant", ToolCalls: []ToolCall{
					{ID: "call_1", Name: "read_file"},
					{ID: "call_2", Name: "grep"},
				}},
				{Role: "tool", ToolCallID: "call_2", Content: "grep ok"},
				{Role: "user", Content: `{"type":"agent_result","agent_id":"worker-1","result":"done"}`},
			},
			want: "user,assistant,tool:call_1,tool:call_2,user",
			wantContentBy: map[string]string{
				"call_1": `{"error":"aborted"}`,
				"call_2": "grep ok",
			},
		},
		{
			name: "tool output is committed before assistant tool call",
			msgs: []ChatMessage{
				{Role: "user", Content: "inspect"},
				{Role: "tool", ToolCallID: "call_1", Content: "early output"},
				{Role: "assistant", ToolCalls: []ToolCall{{ID: "call_1", Name: "read_file"}}},
			},
			want: "user,assistant,tool:call_1",
			wantContentBy: map[string]string{
				"call_1": "early output",
			},
		},
	}

	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			got, err := RepairAndValidateToolCallHistory(tt.msgs)
			if err != nil {
				t.Fatalf("RepairAndValidateToolCallHistory: %v", err)
			}
			if err := ValidateToolCallHistory(got); err != nil {
				t.Fatalf("ValidateToolCallHistory: %v: %+v", err, got)
			}
			if gotOrder := roleToolOrder(got); gotOrder != tt.want {
				t.Fatalf("unexpected order: got %s want %s: %+v", gotOrder, tt.want, got)
			}
			for callID, want := range tt.wantContentBy {
				if got := toolContent(got, callID); got != want {
					t.Fatalf("tool %s content: got %q want %q", callID, got, want)
				}
			}
		})
	}
}

func TestValidateToolCallHistory_orphanTool(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "hi"},
		{Role: "tool", ToolCallID: "call_orphan", Content: "x"},
	}
	if err := ValidateToolCallHistory(msgs); err == nil {
		t.Fatalf("expected error for orphan tool")
	}
}

func TestValidateToolCallHistory_toolAfterUser(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "call_1", Name: "a"}}},
		{Role: "user", Content: "mid"},
		{Role: "tool", ToolCallID: "call_1", Content: "ok"},
	}
	if err := ValidateToolCallHistory(msgs); err == nil {
		t.Fatalf("expected error for tool after user")
	}
}

func TestValidateToolCallHistory_rejectsAssistantToolCallMissingID(t *testing.T) {
	msgs := []ChatMessage{
		{Role: "user", Content: "hello"},
		{Role: "assistant", Content: "", ToolCalls: []ToolCall{{ID: "", Name: "a"}}},
	}
	if err := ValidateToolCallHistory(msgs); err == nil {
		t.Fatal("expected error for assistant tool_call without id")
	}
}

func roles(msgs []ChatMessage) []string {
	out := make([]string, len(msgs))
	for i, m := range msgs {
		out[i] = m.Role
	}
	return out
}

func roleToolOrder(msgs []ChatMessage) string {
	out := make([]string, 0, len(msgs))
	for _, msg := range msgs {
		if msg.Role == "tool" {
			out = append(out, "tool:"+msg.ToolCallID)
			continue
		}
		out = append(out, msg.Role)
	}
	return strings.Join(out, ",")
}

func toolContent(msgs []ChatMessage, callID string) string {
	for _, msg := range msgs {
		if msg.Role == "tool" && msg.ToolCallID == callID {
			return msg.Content
		}
	}
	return ""
}

func toolContents(msgs []ChatMessage, callID string) []string {
	out := []string{}
	for _, msg := range msgs {
		if msg.Role == "tool" && msg.ToolCallID == callID {
			out = append(out, msg.Content)
		}
	}
	return out
}
