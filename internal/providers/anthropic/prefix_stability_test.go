package anthropic

import (
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// stripCacheControlForCompare clones a payload message with every
// cache_control marker removed. The sliding tail marker moves between
// rounds by design; the API's cache key hashes prompt content, not marker
// placement, so prefix-stability assertions must ignore the markers.
func stripCacheControlForCompare(msg anthropicMessage) anthropicMessage {
	out := msg
	out.Content = make([]anthropicBlock, len(msg.Content))
	for i, block := range msg.Content {
		block.CacheControl = nil
		out.Content[i] = block
	}
	return out
}

// TestPrefixStabilityAcrossRounds verifies that the prompt-cache prefix
// remains stable across consecutive requests in a single turn (intra-turn
// tool loops). This is a regression guard: if tools or system change between
// requests, or if message ordering becomes non-deterministic, this test
// will catch it before cache keying breaks.
func TestPrefixStabilityAcrossRounds(t *testing.T) {
	// Shared conversational state across two rounds
	systemPrompt := "You are a helpful assistant."
	tools := []providers.ToolDefinition{
		{
			Name:        "list_files",
			Description: "List files in a directory",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		},
		{
			Name:        "read_file",
			Description: "Read file contents",
			InputSchema: map[string]any{
				"type": "object",
				"properties": map[string]any{
					"path": map[string]any{"type": "string"},
				},
			},
		},
	}

	// Round 1: Initial user request
	round1Messages := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "List the files in /tmp"},
	}

	// Round 2: After first tool call (assistant response + tool result + new user message)
	round2Messages := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "List the files in /tmp"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "list_files", Arguments: `{"path":"/tmp"}`},
		}},
		{Role: "tool", ToolCallID: "call_1", Content: "file1.txt\nfile2.txt"},
		{Role: "user", Content: "Now read file1.txt"},
	}

	// Build cache hints for both rounds (minimal hints for this test)
	hint1 := &providers.CacheHint{
		StableSystem:         true,
		StablePrefixMessages: 0,
		TurnPrefixMessages:   0,
	}
	hint2 := &providers.CacheHint{
		StableSystem:         true,
		StablePrefixMessages: 0,
		TurnPrefixMessages:   0,
	}

	// Build anthropic requests
	req1, err := buildAnthropicRequest(providers.ChatRequest{
		Model:     "claude-3-5-sonnet",
		Messages:  round1Messages,
		Tools:     tools,
		CacheHint: hint1,
	}, 1024, false)
	if err != nil {
		t.Fatalf("round 1 buildAnthropicRequest: %v", err)
	}

	req2, err := buildAnthropicRequest(providers.ChatRequest{
		Model:     "claude-3-5-sonnet",
		Messages:  round2Messages,
		Tools:     tools,
		CacheHint: hint2,
	}, 1024, false)
	if err != nil {
		t.Fatalf("round 2 buildAnthropicRequest: %v", err)
	}

	// Assertion (a): Tools and System are byte-identical across requests
	toolsJSON1, err := json.Marshal(req1.Tools)
	if err != nil {
		t.Fatalf("marshal round 1 tools: %v", err)
	}
	toolsJSON2, err := json.Marshal(req2.Tools)
	if err != nil {
		t.Fatalf("marshal round 2 tools: %v", err)
	}
	if string(toolsJSON1) != string(toolsJSON2) {
		t.Fatalf("tools JSON diverged between rounds:\nround 1: %s\nround 2: %s", toolsJSON1, toolsJSON2)
	}

	systemJSON1, err := json.Marshal(req1.System)
	if err != nil {
		t.Fatalf("marshal round 1 system: %v", err)
	}
	systemJSON2, err := json.Marshal(req2.System)
	if err != nil {
		t.Fatalf("marshal round 2 system: %v", err)
	}
	if string(systemJSON1) != string(systemJSON2) {
		t.Fatalf("system JSON diverged between rounds:\nround 1: %s\nround 2: %s", systemJSON1, systemJSON2)
	}

	// Assertion (b): Round 1's message JSON is a strict prefix of round 2's
	// (same leading messages, unchanged content)
	if len(req1.Messages) > len(req2.Messages) {
		t.Fatalf("round 1 has more messages (%d) than round 2 (%d)", len(req1.Messages), len(req2.Messages))
	}

	// For each message in round 1, verify its content is identical to the
	// corresponding message in round 2. Cache markers are stripped first:
	// the sliding tail marker moves between rounds by design.
	for i, msg1 := range req1.Messages {
		msg2 := req2.Messages[i]
		json1, err := json.Marshal(stripCacheControlForCompare(msg1))
		if err != nil {
			t.Fatalf("marshal round 1 message %d: %v", i, err)
		}
		json2, err := json.Marshal(stripCacheControlForCompare(msg2))
		if err != nil {
			t.Fatalf("marshal round 2 message %d: %v", i, err)
		}
		if string(json1) != string(json2) {
			t.Fatalf("message %d diverged between rounds:\nround 1: %s\nround 2: %s", i, json1, json2)
		}
	}

	// Assertion (c): System and Tools remain identical across rounds
	// (already verified above, but including in comment for clarity)
}

// TestPrefixStabilityWithUncacheableTrail verifies that prefix stability
// is maintained even when the final message has uncacheable blocks (e.g.,
// thinking, image). The sliding tail marker should find the last cacheable
// block, and the prefix should still remain stable.
func TestPrefixStabilityWithUncacheableTrail(t *testing.T) {
	systemPrompt := "You are a helpful assistant."
	tools := []providers.ToolDefinition{
		{
			Name:        "search",
			Description: "Search for information",
			InputSchema: map[string]any{"type": "object"},
		},
	}

	// Round 1: simple text message
	round1Messages := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "What is 2+2?"},
	}

	// Round 2: same initial messages, but second user message ends with image
	// (which is uncacheable)
	round2Messages := []providers.ChatMessage{
		{Role: "system", Content: systemPrompt},
		{Role: "user", Content: "What is 2+2?"},
		{Role: "assistant", Content: "4"},
		{Role: "user", Content: "Show me more", Images: []providers.InputImage{
			{MediaType: "image/png", Data: "base64data"},
		}},
	}

	hint1 := &providers.CacheHint{StableSystem: true}
	hint2 := &providers.CacheHint{StableSystem: true}

	req1, err := buildAnthropicRequest(providers.ChatRequest{
		Model:     "claude-3-5-sonnet",
		Messages:  round1Messages,
		Tools:     tools,
		CacheHint: hint1,
	}, 1024, false)
	if err != nil {
		t.Fatalf("round 1: %v", err)
	}

	req2, err := buildAnthropicRequest(providers.ChatRequest{
		Model:     "claude-3-5-sonnet",
		Messages:  round2Messages,
		Tools:     tools,
		CacheHint: hint2,
	}, 1024, false)
	if err != nil {
		t.Fatalf("round 2: %v", err)
	}

	// Verify tools and system are identical
	toolsJSON1, _ := json.Marshal(req1.Tools)
	toolsJSON2, _ := json.Marshal(req2.Tools)
	if string(toolsJSON1) != string(toolsJSON2) {
		t.Fatalf("tools diverged")
	}

	systemJSON1, _ := json.Marshal(req1.System)
	systemJSON2, _ := json.Marshal(req2.System)
	if string(systemJSON1) != string(systemJSON2) {
		t.Fatalf("system diverged")
	}

	// Verify round 1 messages are a content-prefix of round 2 (cache
	// markers stripped — the tail marker moves between rounds by design).
	if len(req1.Messages) > len(req2.Messages) {
		t.Fatalf("round 1 has more messages")
	}
	for i, msg1 := range req1.Messages {
		msg2 := req2.Messages[i]
		json1, _ := json.Marshal(stripCacheControlForCompare(msg1))
		json2, _ := json.Marshal(stripCacheControlForCompare(msg2))
		if string(json1) != string(json2) {
			t.Fatalf("message %d diverged", i)
		}
	}
}
