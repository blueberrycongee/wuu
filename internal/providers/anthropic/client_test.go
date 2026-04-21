package anthropic

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestChat_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing api key header")
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"hello"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if resp.Content != "hello" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if len(resp.ToolCalls) != 0 {
		t.Fatalf("unexpected tool calls: %+v", resp.ToolCalls)
	}
}

func TestChat_AnthropicReplaysReasoningBlocksForAssistantToolUse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %#v", body["messages"])
		}
		assistant, ok := msgs[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected assistant message: %#v", msgs[1])
		}
		content, ok := assistant["content"].([]any)
		if !ok || len(content) != 2 {
			t.Fatalf("expected thinking + tool_use content, got %#v", assistant["content"])
		}
		thinking, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected thinking block: %#v", content[0])
		}
		if thinking["type"] != "thinking" || thinking["thinking"] != "inspect repo before tool use" || thinking["signature"] != "sig_1" {
			t.Fatalf("unexpected thinking block payload: %#v", thinking)
		}
		toolUse, ok := content[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected tool_use block: %#v", content[1])
		}
		if toolUse["type"] != "tool_use" || toolUse["id"] != "call_1" || toolUse["name"] != "list_files" {
			t.Fatalf("unexpected tool_use payload: %#v", toolUse)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "inspect repo"},
			{
				Role:             "assistant",
				ReasoningContent: "inspect repo before tool use",
				ReasoningBlocks: []providers.ReasoningBlock{
					{Type: "thinking", Thinking: "inspect repo before tool use", Signature: "sig_1"},
				},
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "list_files", Arguments: `{}`},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Name: "list_files", Content: "[]"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_AnthropicFallsBackToReasoningContentReplay(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) != 3 {
			t.Fatalf("expected 3 messages, got %#v", body["messages"])
		}
		assistant, ok := msgs[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected assistant message: %#v", msgs[1])
		}
		content, ok := assistant["content"].([]any)
		if !ok || len(content) != 2 {
			t.Fatalf("expected synthetic thinking + tool_use content, got %#v", assistant["content"])
		}
		thinking, ok := content[0].(map[string]any)
		if !ok || thinking["type"] != "thinking" || thinking["thinking"] != "inspect repo before tool use" {
			t.Fatalf("unexpected synthetic thinking block payload: %#v", content[0])
		}
		if _, ok := thinking["signature"]; ok {
			t.Fatalf("did not expect synthetic signature on legacy replay: %#v", thinking)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "inspect repo"},
			{
				Role:             "assistant",
				ReasoningContent: "inspect repo before tool use",
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "list_files", Arguments: `{}`},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Name: "list_files", Content: "[]"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_ParsesReasoningBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{
  "content": [
    {"type":"thinking","thinking":"inspect repo before tool use","signature":"sig_1"},
    {"type":"tool_use","id":"call_1","name":"list_files","input":{}}
  ],
  "stop_reason":"tool_use"
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "inspect repo"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.ReasoningContent != "inspect repo before tool use" {
		t.Fatalf("unexpected reasoning content: %q", resp.ReasoningContent)
	}
	if len(resp.ReasoningBlocks) != 1 {
		t.Fatalf("expected 1 reasoning block, got %+v", resp.ReasoningBlocks)
	}
	if resp.ReasoningBlocks[0].Signature != "sig_1" {
		t.Fatalf("unexpected reasoning block: %+v", resp.ReasoningBlocks[0])
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_1" {
		t.Fatalf("unexpected tool calls: %+v", resp.ToolCalls)
	}
}

func TestChat_AnthropicAddsCacheControlFromHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		system, ok := body["system"].([]any)
		if !ok || len(system) != 1 {
			t.Fatalf("expected system blocks, got %#v", body["system"])
		}
		sysBlock, ok := system[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected system block: %#v", system[0])
		}
		cacheCtl, ok := sysBlock["cache_control"].(map[string]any)
		if !ok || cacheCtl["type"] != "ephemeral" {
			t.Fatalf("expected system cache_control, got %#v", sysBlock["cache_control"])
		}
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) < 2 {
			t.Fatalf("expected messages, got %#v", body["messages"])
		}
		second, ok := msgs[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected message payload: %#v", msgs[1])
		}
		content, ok := second["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("unexpected content blocks: %#v", second["content"])
		}
		lastBlock, ok := content[len(content)-1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected content block: %#v", content[len(content)-1])
		}
		cacheCtl, ok = lastBlock["cache_control"].(map[string]any)
		if !ok || cacheCtl["type"] != "ephemeral" {
			t.Fatalf("expected message cache_control, got %#v", lastBlock["cache_control"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "stable reply"},
			{Role: "user", Content: "latest"},
		},
		CacheHint: &providers.CacheHint{StableSystem: true, StablePrefixMessages: 2},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestBuildAnthropicRequest_SmooshesSystemReminderIntoToolResult(t *testing.T) {
	reminder := wuucontext.FormatSystemReminder(wuucontext.EnvInfo{
		CWD:       "/tmp/project",
		Date:      "2026-04-21",
		GitBranch: "main",
		GitStatus: "clean",
	})

	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "check repo"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				{ID: "tool_1", Name: "git", Arguments: `{"subcommand":"status"}`},
			}},
			{Role: "tool", ToolCallID: "tool_1", Name: "git", Content: `{"exit_code":0}`},
			{Role: "user", Name: wuucontext.SystemReminderMessageName, Content: reminder},
		},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}

	if len(payload.Messages) != 3 {
		t.Fatalf("expected 3 messages after merge, got %d", len(payload.Messages))
	}
	last := payload.Messages[2]
	if last.Role != "user" {
		t.Fatalf("expected final message to be user, got %q", last.Role)
	}
	if len(last.Content) != 1 {
		t.Fatalf("expected tool_result-only user content, got %+v", last.Content)
	}
	if last.Content[0].Type != "tool_result" {
		t.Fatalf("expected tool_result block, got %+v", last.Content[0])
	}
	if !strings.Contains(last.Content[0].Content, `{"exit_code":0}`) {
		t.Fatalf("expected tool output to be preserved, got %q", last.Content[0].Content)
	}
	if !strings.Contains(last.Content[0].Content, "<system-reminder>") {
		t.Fatalf("expected system reminder to be folded into tool_result, got %q", last.Content[0].Content)
	}
}

func TestBuildAnthropicRequest_LeavesRegularUserTextOutsideToolResult(t *testing.T) {
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "check repo"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				{ID: "tool_1", Name: "git", Arguments: `{"subcommand":"status"}`},
			}},
			{Role: "tool", ToolCallID: "tool_1", Name: "git", Content: `{"exit_code":0}`},
			{Role: "user", Content: "real follow-up"},
		},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}

	if len(payload.Messages) != 3 {
		t.Fatalf("expected 3 messages after merge, got %d", len(payload.Messages))
	}
	last := payload.Messages[2]
	if len(last.Content) != 2 {
		t.Fatalf("expected tool_result + text siblings for real user input, got %+v", last.Content)
	}
	if last.Content[0].Type != "tool_result" || last.Content[1].Type != "text" {
		t.Fatalf("unexpected block order: %+v", last.Content)
	}
	if got := last.Content[1].Text; got != "real follow-up" {
		t.Fatalf("unexpected trailing user text: %q", got)
	}
}

func TestChat_AnthropicPrefersCompactSummaryAsCacheAnchor(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) != 3 {
			t.Fatalf("expected three non-system messages, got %#v", body["messages"])
		}
		first, ok := msgs[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected first message: %#v", msgs[0])
		}
		firstContent, ok := first["content"].([]any)
		if !ok || len(firstContent) != 1 {
			t.Fatalf("unexpected first content payload: %#v", first["content"])
		}
		firstBlock, ok := firstContent[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected first block: %#v", firstContent[0])
		}
		if firstBlock["text"] != "stable summary payload" {
			t.Fatalf("unexpected summary payload: %#v", firstBlock)
		}
		cacheControl, ok := firstBlock["cache_control"].(map[string]any)
		if !ok || cacheControl["type"] != "ephemeral" {
			t.Fatalf("expected cache_control on compact summary anchor, got %#v", firstBlock["cache_control"])
		}

		second, ok := msgs[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected second message: %#v", msgs[1])
		}
		secondContent, ok := second["content"].([]any)
		if !ok || len(secondContent) != 1 {
			t.Fatalf("unexpected second content payload: %#v", second["content"])
		}
		secondBlock, ok := secondContent[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected second block: %#v", secondContent[0])
		}
		if _, exists := secondBlock["cache_control"]; exists {
			t.Fatalf("did not expect cache_control on latest stable message when compact summary is present: %#v", secondBlock)
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "[Conversation summary]\nrewritten history"},
			{Role: "user", Content: "stable summary payload"},
			{Role: "assistant", Content: "older stable answer"},
			{Role: "user", Content: "latest ask"},
		},
		CacheHint: &providers.CacheHint{
			StableSystem:         true,
			StablePrefixMessages: 2,
			HasCompactSummary:    true,
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestChat_AnthropicOmitsCacheControlWithoutHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := body["system"].([]any); ok {
			t.Fatalf("did not expect structured system blocks: %#v", body["system"])
		}
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) == 0 {
			t.Fatalf("expected messages, got %#v", body["messages"])
		}
		first, ok := msgs[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected message payload: %#v", msgs[0])
		}
		content, ok := first["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("unexpected content blocks: %#v", first["content"])
		}
		block, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected content block: %#v", content[0])
		}
		if _, ok := block["cache_control"]; ok {
			t.Fatalf("did not expect cache_control: %#v", block["cache_control"])
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestChat_AddsCacheControlToStableAnthropicPrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		system, ok := body["system"].([]any)
		if !ok || len(system) != 1 {
			t.Fatalf("unexpected system payload: %#v", body["system"])
		}
		sysBlock, ok := system[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected system block: %#v", system[0])
		}
		if sysBlock["text"] != "sys" {
			t.Fatalf("unexpected system text: %#v", sysBlock["text"])
		}
		cacheControl, ok := sysBlock["cache_control"].(map[string]any)
		if !ok || cacheControl["type"] != "ephemeral" {
			t.Fatalf("unexpected system cache_control: %#v", sysBlock["cache_control"])
		}

		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) != 3 {
			t.Fatalf("unexpected messages payload: %#v", body["messages"])
		}

		// First message (user "stable context") — no cache_control.
		firstMsg, ok := msgs[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected first message: %#v", msgs[0])
		}
		content, ok := firstMsg["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("unexpected first content payload: %#v", firstMsg["content"])
		}
		textBlock, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected first text block: %#v", content[0])
		}
		if _, exists := textBlock["cache_control"]; exists {
			t.Fatalf("did not expect cache_control on first stable message: %#v", textBlock)
		}

		// Second message (assistant "stable reply") — cache_control on stable prefix boundary.
		secondMsg, ok := msgs[1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected second message: %#v", msgs[1])
		}
		content, ok = secondMsg["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("unexpected second content payload: %#v", secondMsg["content"])
		}
		textBlock, ok = content[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected second text block: %#v", content[0])
		}
		cacheControl, ok = textBlock["cache_control"].(map[string]any)
		if !ok || cacheControl["type"] != "ephemeral" {
			t.Fatalf("unexpected message cache_control: %#v", textBlock["cache_control"])
		}

		// Third message (user "volatile ask") — no cache_control.
		thirdMsg, ok := msgs[2].(map[string]any)
		if !ok {
			t.Fatalf("unexpected third message: %#v", msgs[2])
		}
		content, ok = thirdMsg["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("unexpected third content payload: %#v", thirdMsg["content"])
		}
		textBlock, ok = content[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected third text block: %#v", content[0])
		}
		if _, exists := textBlock["cache_control"]; exists {
			t.Fatalf("did not expect cache_control on volatile message: %#v", textBlock)
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "stable context"},
			{Role: "assistant", Content: "stable reply"},
			{Role: "user", Content: "volatile ask"},
		},
		CacheHint: &providers.CacheHint{
			StableSystem:         true,
			StablePrefixMessages: 2,
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestChat_OmitsCacheControlWithoutHint(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if system, exists := body["system"]; exists {
			t.Fatalf("did not expect structured system payload: %#v", system)
		}
		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) != 1 {
			t.Fatalf("unexpected messages payload: %#v", body["messages"])
		}
		msg, ok := msgs[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected message: %#v", msgs[0])
		}
		content, ok := msg["content"].([]any)
		if !ok || len(content) != 1 {
			t.Fatalf("unexpected content payload: %#v", msg["content"])
		}
		textBlock, ok := content[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected text block: %#v", content[0])
		}
		if _, exists := textBlock["cache_control"]; exists {
			t.Fatalf("did not expect cache_control without hint: %#v", textBlock)
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestStreamIdleTimeout_DefaultMatchesCC(t *testing.T) {
	t.Setenv("WUU_STREAM_IDLE_TIMEOUT_MS", "")
	if got := streamIdleTimeout(); got != 90*time.Second {
		t.Fatalf("expected 90s default stream idle timeout, got %s", got)
	}
}

func TestChat_ToolUseResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"tool_use","id":"call-1","name":"read_file","input":{"path":"README.md"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "read readme"},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(resp.ToolCalls))
	}
	call := resp.ToolCalls[0]
	if call.ID != "call-1" || call.Name != "read_file" {
		t.Fatalf("unexpected tool call: %+v", call)
	}

	var args map[string]any
	if err := json.Unmarshal([]byte(call.Arguments), &args); err != nil {
		t.Fatalf("parse arguments: %v", err)
	}
	if args["path"] != "README.md" {
		t.Fatalf("unexpected arguments: %+v", args)
	}
}

func TestChat_SendsImageBlocks(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) != 1 {
			t.Fatalf("unexpected messages payload: %#v", body["messages"])
		}

		msg, ok := msgs[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected message type: %#v", msgs[0])
		}
		content, ok := msg["content"].([]any)
		if !ok || len(content) != 2 {
			t.Fatalf("unexpected content payload: %#v", msg["content"])
		}

		textBlock, ok := content[0].(map[string]any)
		if !ok || textBlock["type"] != "text" || textBlock["text"] != "describe this" {
			t.Fatalf("unexpected text block: %#v", content[0])
		}

		imageBlock, ok := content[1].(map[string]any)
		if !ok || imageBlock["type"] != "image" {
			t.Fatalf("unexpected image block: %#v", content[1])
		}
		source, ok := imageBlock["source"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected source payload: %#v", imageBlock["source"])
		}
		if source["type"] != "base64" || source["media_type"] != "image/png" || source["data"] != "AAA" {
			t.Fatalf("unexpected image source: %#v", source)
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{
				Role:    "user",
				Content: "describe this",
				Images: []providers.InputImage{
					{MediaType: "image/png", Data: "AAA"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestChat_AppliesCacheControlToStablePrefix(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			System []struct {
				Type         string `json:"type"`
				Text         string `json:"text"`
				CacheControl *struct {
					Type string `json:"type"`
				} `json:"cache_control,omitempty"`
			} `json:"system"`
			Messages []struct {
				Role    string `json:"role"`
				Content []struct {
					Type         string `json:"type"`
					Text         string `json:"text,omitempty"`
					CacheControl *struct {
						Type string `json:"type"`
					} `json:"cache_control,omitempty"`
				} `json:"content"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.System) != 1 {
			t.Fatalf("expected one system block, got %#v", body.System)
		}
		if body.System[0].Text != "sys" {
			t.Fatalf("unexpected system text: %q", body.System[0].Text)
		}
		if body.System[0].CacheControl == nil || body.System[0].CacheControl.Type != "ephemeral" {
			t.Fatalf("expected cache_control on system block, got %#v", body.System[0].CacheControl)
		}
		if len(body.Messages) != 2 {
			t.Fatalf("expected two non-system messages, got %d", len(body.Messages))
		}
		if body.Messages[0].Role != "user" {
			t.Fatalf("unexpected first role: %q", body.Messages[0].Role)
		}
		lastBlock := body.Messages[0].Content[len(body.Messages[0].Content)-1]
		if lastBlock.CacheControl == nil || lastBlock.CacheControl.Type != "ephemeral" {
			t.Fatalf("expected cache_control on stable prefix boundary, got %#v", lastBlock.CacheControl)
		}
		if len(body.Messages[1].Content) == 0 {
			t.Fatal("expected follow-up content")
		}
		followUpLast := body.Messages[1].Content[len(body.Messages[1].Content)-1]
		if followUpLast.CacheControl != nil {
			t.Fatalf("did not expect cache_control on volatile message, got %#v", followUpLast.CacheControl)
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "second"},
		},
		CacheHint: &providers.CacheHint{
			StableSystem:         true,
			StablePrefixMessages: 1,
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestChat_RetriesTransientServerError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if attempts.Add(1) == 1 {
			w.WriteHeader(http.StatusInternalServerError)
			_, _ = w.Write([]byte(`{"error":"upstream unavailable"}`))
			return
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	rc := providers.RetryConfig{
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
		MaxDelay:     2 * time.Millisecond,
	}
	client, err := New(ClientConfig{
		BaseURL:     server.URL,
		APIKey:      "test-key",
		RetryConfig: &rc,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
}

func TestChat_DoesNotRetryAuthError(t *testing.T) {
	var attempts atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		attempts.Add(1)
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"unauthorized"}`))
	}))
	defer server.Close()

	rc := providers.RetryConfig{
		MaxRetries:   2,
		InitialDelay: time.Millisecond,
		MaxDelay:     2 * time.Millisecond,
	}
	client, err := New(ClientConfig{
		BaseURL:     server.URL,
		APIKey:      "test-key",
		RetryConfig: &rc,
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt for auth failure, got %d", got)
	}
}

func TestStreamChat_SSE(t *testing.T) {
	ssePayload := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\" world\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":5}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Verify content deltas arrive in order.
	var contentParts []string
	for _, ev := range events {
		if ev.Type == providers.EventContentDelta {
			contentParts = append(contentParts, ev.Content)
		}
	}
	if len(contentParts) != 2 || contentParts[0] != "Hello" || contentParts[1] != " world" {
		t.Fatalf("unexpected content deltas: %v", contentParts)
	}

	// Verify EventDone is the last event.
	last := events[len(events)-1]
	if last.Type != providers.EventDone {
		t.Fatalf("expected last event to be EventDone, got %s", last.Type)
	}

	// Verify usage in done event.
	if last.Usage == nil {
		t.Fatal("expected usage in done event")
	}
	if last.Usage.InputTokens != 10 {
		t.Fatalf("expected 10 input tokens, got %d", last.Usage.InputTokens)
	}
	if last.Usage.OutputTokens != 5 {
		t.Fatalf("expected 5 output tokens, got %d", last.Usage.OutputTokens)
	}
}

func TestStreamChat_ToolUse(t *testing.T) {
	ssePayload := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":15}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"tool_use\",\"id\":\"tu_1\",\"name\":\"read_file\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"{\\\"path\\\":\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"input_json_delta\",\"partial_json\":\"\\\"test.go\\\"}\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"output_tokens\":8}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "read file"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	// Verify tool use start.
	var toolStarts, toolEnds int
	var endToolCall *providers.ToolCall
	for _, ev := range events {
		switch ev.Type {
		case providers.EventToolUseStart:
			toolStarts++
			if ev.ToolCall == nil || ev.ToolCall.Name != "read_file" || ev.ToolCall.ID != "tu_1" {
				t.Fatalf("unexpected tool start: %+v", ev.ToolCall)
			}
		case providers.EventToolUseEnd:
			toolEnds++
			endToolCall = ev.ToolCall
		}
	}
	if toolStarts != 1 {
		t.Fatalf("expected 1 tool start, got %d", toolStarts)
	}
	if toolEnds != 1 {
		t.Fatalf("expected 1 tool end, got %d", toolEnds)
	}
	if endToolCall == nil || endToolCall.ID != "tu_1" {
		t.Fatalf("unexpected tool end: %+v", endToolCall)
	}
	if endToolCall.Arguments != `{"path":"test.go"}` {
		t.Fatalf("unexpected tool arguments: %q", endToolCall.Arguments)
	}

	// Verify done is last.
	last := events[len(events)-1]
	if last.Type != providers.EventDone {
		t.Fatalf("expected EventDone last, got %s", last.Type)
	}
}

func TestStreamChat_ThinkingDoneIncludesReasoningBlock(t *testing.T) {
	ssePayload := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":15}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"thinking\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"thinking_delta\",\"thinking\":\"inspect repo\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"signature_delta\",\"signature\":\"sig_1\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":8}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	if len(events) != 3 {
		t.Fatalf("expected thinking delta + thinking done + done, got %d events", len(events))
	}
	if events[0].Type != providers.EventThinkingDelta || events[0].Content != "inspect repo" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != providers.EventThinkingDone || events[1].ReasoningBlock == nil {
		t.Fatalf("expected thinking_done with reasoning block, got %+v", events[1])
	}
	if events[1].ReasoningBlock.Signature != "sig_1" || events[1].ReasoningBlock.Thinking != "inspect repo" {
		t.Fatalf("unexpected reasoning block: %+v", events[1].ReasoningBlock)
	}
}

func TestStreamChat_ErrorEventSurfacesProviderError(t *testing.T) {
	ssePayload := "event: error\n" +
		"data: {\"error\":{\"code\":\"1305\",\"message\":\"该模型当前访问量过大，请您稍后再试\"}}\n\n" +
		"data: [DONE]\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 1 {
		t.Fatalf("expected 1 stream event, got %d", len(events))
	}
	if events[0].Type != providers.EventError {
		t.Fatalf("expected error event, got %+v", events[0])
	}
	if events[0].Error == nil || !providers.IsRetryable(events[0].Error) {
		t.Fatalf("expected retryable provider stream error, got %v", events[0].Error)
	}
}

func TestStreamChat_MissingMessageStopYieldsIncompleteError(t *testing.T) {
	ssePayload := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":10}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"Hello\"}}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) != 2 {
		t.Fatalf("expected content delta + terminal error, got %d events", len(events))
	}
	if events[0].Type != providers.EventContentDelta || events[0].Content != "Hello" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != providers.EventError {
		t.Fatalf("expected terminal error, got %+v", events[1])
	}
	if events[1].Error == nil || !providers.IsRetryable(events[1].Error) {
		t.Fatalf("expected retryable incomplete stream error, got %v", events[1].Error)
	}
}

func TestStreamChat_MessageDeltaCanBackfillInputTokens(t *testing.T) {
	ssePayload := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"测试\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"input_tokens\":10,\"output_tokens\":2,\"cache_read_input_tokens\":0}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	last := events[len(events)-1]
	if last.Type != providers.EventDone {
		t.Fatalf("expected EventDone last, got %s", last.Type)
	}
	if last.Usage == nil {
		t.Fatal("expected usage in done event")
	}
	if last.Usage.InputTokens != 10 {
		t.Fatalf("expected backfilled input tokens 10, got %d", last.Usage.InputTokens)
	}
	if last.Usage.OutputTokens != 2 {
		t.Fatalf("expected output tokens 2, got %d", last.Usage.OutputTokens)
	}
}

func TestStreamChat_ValidationErrors(t *testing.T) {
	client, _ := New(ClientConfig{BaseURL: "http://localhost", APIKey: "k"})

	_, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "", Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for empty model")
	}

	_, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "m", Messages: nil,
	})
	if err == nil {
		t.Fatal("expected error for empty messages")
	}
}

func TestStreamChat_ServerError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusInternalServerError)
		fmt.Fprint(w, "internal error")
	}))
	defer server.Close()

	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "k"})
	_, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "m", Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error for 500 response")
	}
	var httpErr *providers.HTTPError
	if !errors.As(err, &httpErr) {
		t.Fatalf("expected HTTPError, got %T (%v)", err, err)
	}
	if httpErr.StatusCode != http.StatusInternalServerError {
		t.Fatalf("unexpected status code: %d", httpErr.StatusCode)
	}
}
