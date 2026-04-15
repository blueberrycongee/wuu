package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// TestWorkerResultMessageStructure verifies that the message sequence
// after worker completion produces valid role alternation with no
// mixed tool_result+text content blocks in any user message.
//
// This is the root cause test for the deterministic reconnect loop
// on proxies after worker completion.
func TestWorkerResultMessageStructure(t *testing.T) {
	// Simulate the EXACT message sequence after worker completion:
	//
	// 1. user: original prompt
	// 2. assistant: spawns a worker (tool_use)
	// 3. tool: spawn result (maps to user+tool_result)
	// 4. assistant: "" (empty — model stopped after tool result)
	// 5. user: <worker-result>... (injected on completion)
	// 6. user: env context (BeforeStep injection)
	history := []providers.ChatMessage{
		{Role: "system", Content: "You are a coding agent."},
		{Role: "user", Content: "Run git pull"},
		{Role: "assistant", Content: "I'll spawn a worker.", ToolCalls: []providers.ToolCall{
			{ID: "call_001", Name: "spawn_agent", Arguments: `{"description":"git pull","prompt":"run git pull"}`},
		}},
		{Role: "tool", ToolCallID: "call_001", Name: "spawn_agent", Content: `{"agent_id":"w-123","status":"running"}`},
		// Key: empty assistant persisted for alternation (the fix).
		{Role: "assistant", Content: ""},
		// Worker result injected after completion.
		{Role: "user", Content: "<worker-result agent-id=\"w-123\">\nDone: git pull succeeded\n</worker-result>"},
		// BeforeStep env context injection.
		{Role: "user", Content: "[Environment context]: cwd=/workspace"},
	}

	var capturedBody map[string]any
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if err := json.NewDecoder(r.Body).Decode(&capturedBody); err != nil {
			t.Fatalf("decode: %v", err)
		}
		// Return a minimal valid SSE response.
		w.Header().Set("Content-Type", "text/event-stream")
		w.Write([]byte("event: message_start\ndata: {\"type\":\"message_start\",\"message\":{\"id\":\"m\",\"type\":\"message\",\"role\":\"assistant\",\"content\":[],\"model\":\"test\",\"usage\":{\"input_tokens\":1,\"output_tokens\":1}}}\n\n"))
		w.Write([]byte("event: content_block_start\ndata: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n"))
		w.Write([]byte("event: content_block_delta\ndata: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"OK\"}}\n\n"))
		w.Write([]byte("event: content_block_stop\ndata: {\"type\":\"content_block_stop\",\"index\":0}\n\n"))
		w.Write([]byte("event: message_delta\ndata: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":1}}\n\n"))
		w.Write([]byte("event: message_stop\ndata: {\"type\":\"message_stop\"}\n\n"))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:  server.URL,
		APIKey:   "test-key",
		MaxTokens: 1024,
	})
	if err != nil {
		t.Fatal(err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "claude-opus-4-6",
		Messages: history,
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	// Drain events.
	for range ch {
	}

	// Verify the captured request body.
	msgs, ok := capturedBody["messages"].([]any)
	if !ok {
		t.Fatalf("no messages in request body")
	}

	// Check role alternation.
	prevRole := ""
	for i, raw := range msgs {
		msg := raw.(map[string]any)
		role := msg["role"].(string)
		if role == prevRole {
			t.Fatalf("consecutive same role at index %d: %s (prev also %s)", i, role, prevRole)
		}
		prevRole = role

		// Check no mixed tool_result+text blocks in any user message.
		if role == "user" {
			content := msg["content"].([]any)
			hasToolResult := false
			hasText := false
			for _, block := range content {
				b := block.(map[string]any)
				switch b["type"].(string) {
				case "tool_result":
					hasToolResult = true
				case "text":
					hasText = true
				}
			}
			if hasToolResult && hasText {
				t.Fatalf("user message at index %d has mixed tool_result+text blocks: %+v", i, content)
			}
		}
	}

	t.Logf("✓ %d messages, strict alternation, no mixed blocks", len(msgs))

	// Also print role sequence for clarity.
	roles := ""
	for i, raw := range msgs {
		msg := raw.(map[string]any)
		if i > 0 {
			roles += " → "
		}
		role := msg["role"].(string)
		content := msg["content"].([]any)
		types := ""
		for j, block := range content {
			b := block.(map[string]any)
			if j > 0 {
				types += "+"
			}
			types += b["type"].(string)
		}
		roles += role + "(" + types + ")"
	}
	t.Logf("  sequence: %s", roles)
}

// TestWorkerResultWithoutFix_WouldProduceMixedBlocks demonstrates the
// bug that existed before the fix: without the empty assistant message,
// tool_result and worker-result text merge into one user message.
func TestWorkerResultWithoutFix_WouldProduceMixedBlocks(t *testing.T) {
	// Same sequence but WITHOUT the empty assistant (pre-fix state).
	history := []providers.ChatMessage{
		{Role: "system", Content: "You are a coding agent."},
		{Role: "user", Content: "Run git pull"},
		{Role: "assistant", Content: "I'll spawn a worker.", ToolCalls: []providers.ToolCall{
			{ID: "call_001", Name: "spawn_agent", Arguments: `{"description":"git pull","prompt":"run git pull"}`},
		}},
		{Role: "tool", ToolCallID: "call_001", Name: "spawn_agent", Content: `{"agent_id":"w-123","status":"running"}`},
		// NO empty assistant here — this is the pre-fix state.
		{Role: "user", Content: "<worker-result>Done</worker-result>"},
	}

	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model:    "claude-opus-4-6",
		Messages: history,
	}, 1024, false)
	if err != nil {
		t.Fatal(err)
	}

	// The tool message (role=tool) maps to role=user with tool_result.
	// The next message is also role=user (worker-result text).
	// They get MERGED into one user message.
	for i, msg := range payload.Messages {
		if msg.Role != "user" {
			continue
		}
		hasToolResult := false
		hasText := false
		for _, block := range msg.Content {
			if block.Type == "tool_result" {
				hasToolResult = true
			}
			if block.Type == "text" {
				hasText = true
			}
		}
		if hasToolResult && hasText {
			t.Logf("✓ Confirmed: pre-fix produces mixed tool_result+text at msg[%d] (%d blocks)", i, len(msg.Content))
			return
		}
	}
	t.Fatal("expected mixed blocks in pre-fix scenario but didn't find them")
}
