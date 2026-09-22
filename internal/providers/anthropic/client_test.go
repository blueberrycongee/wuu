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

	"github.com/blueberrycongee/wuu/internal/config"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestAnthropicMessagesURL(t *testing.T) {
	tests := map[string]string{
		"https://api.anthropic.com":           "https://api.anthropic.com/v1/messages",
		"https://api.kimi.com/coding/":        "https://api.kimi.com/coding/v1/messages",
		"https://api.kimi.com/coding/v1":      "https://api.kimi.com/coding/v1/messages",
		"https://proxy.example/v1/messages":   "https://proxy.example/v1/messages",
		"https://proxy.example/anthropic/v1/": "https://proxy.example/anthropic/v1/messages",
	}
	for baseURL, want := range tests {
		if got := anthropicMessagesURL(baseURL); got != want {
			t.Errorf("anthropicMessagesURL(%q) = %q, want %q", baseURL, got, want)
		}
	}
}

func TestChat_TextResponse(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if r.Header.Get("x-api-key") != "test-key" {
			t.Fatalf("missing api key header")
		}
		betas := r.Header.Get("anthropic-beta")
		for _, expected := range []string{"interleaved-thinking-2025-05-14", "prompt-caching-2024-07-31", "token-efficient-tools-2026-03-28"} {
			if !strings.Contains(betas, expected) {
				t.Fatalf("default Anthropic beta header %q missing from %q", expected, betas)
			}
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

func TestChat_RejectsInvalidToolSurfaceBeforeRequest(t *testing.T) {
	called := false
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		called = true
		t.Fatalf("request should not reach provider")
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{{
			Role:    "user",
			Content: "hi",
		}},
		Tools: []providers.ToolDefinition{{
			Name:        "bad.tool.name",
			Description: "Bad tool",
			InputSchema: map[string]any{"type": "object"},
		}},
	})
	if err == nil || !strings.Contains(err.Error(), "bad.tool.name") {
		t.Fatalf("expected local tool surface validation error, got %v", err)
	}
	if called {
		t.Fatal("provider was called")
	}
}

func TestChat_ToolSearchNativeAddsBetaHeaderWhenExplicitlyEnabled(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("anthropic-beta"); !strings.Contains(got, toolSearchBetaHeader1P) {
			t.Fatalf("expected tool search beta header, got %q", got)
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
		Model:                       "claude-sonnet-4-5",
		Messages:                    []providers.ChatMessage{{Role: "user", Content: "hello"}},
		NativeDeferredToolDiscovery: true,
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}},
		},
		ProviderOptions: map[string]any{"anthropicToolSearch": true},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_ToolSearchNativeMergesConfiguredBetaHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		got := r.Header.Get("anthropic-beta")
		if !strings.Contains(got, toolSearchBetaHeader1P) || !strings.Contains(got, "fast-mode-2026-02-01") {
			t.Fatalf("expected merged beta header, got %q", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Headers: map[string]string{"anthropic-beta": "fast-mode-2026-02-01"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:                       "claude-sonnet-4-5",
		Messages:                    []providers.ChatMessage{{Role: "user", Content: "hello"}},
		NativeDeferredToolDiscovery: true,
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}},
		},
		ProviderOptions: map[string]any{"anthropicToolSearch": true},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_DisablingDefaultBetasKeepsConfiguredBetaHeader(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-beta"); got != "fast-mode-2026-02-01" {
			t.Fatalf("configured beta header = %q, want only fast mode", got)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Headers: map[string]string{"anthropic-beta": "fast-mode-2026-02-01"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:           "compatible-model",
		Messages:        []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{"anthropic_default_betas": false},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
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

func TestChat_AnthropicReplaysEmptySignedThinkingForAssistantToolUse(t *testing.T) {
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
		thinkingText, present := thinking["thinking"]
		if !present || thinkingText != "" || thinking["type"] != "thinking" || thinking["signature"] != "sig_empty" {
			t.Fatalf("empty signed thinking must be replayed verbatim, got %#v", thinking)
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
				Role: "assistant",
				ReasoningBlocks: []providers.ReasoningBlock{
					{Type: "thinking", Thinking: "", Signature: "sig_empty"},
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
		if !ok || len(msgs) != 3 {
			t.Fatalf("expected 3 non-system messages, got %#v", body["messages"])
		}

		// With the sliding tail marker strategy, only the last message should
		// have a cache_control marker on its last cacheable block.
		// Check that earlier messages have no markers.
		for i := 0; i < len(msgs)-1; i++ {
			msg, ok := msgs[i].(map[string]any)
			if !ok {
				t.Fatalf("unexpected message: %#v", msgs[i])
			}
			content, ok := msg["content"].([]any)
			if !ok || len(content) == 0 {
				continue
			}
			for _, blk := range content {
				block, ok := blk.(map[string]any)
				if !ok {
					continue
				}
				if _, exists := block["cache_control"]; exists {
					t.Fatalf("did not expect cache_control on non-final message: %#v", block)
				}
			}
		}

		// The last message should have cache_control on its last cacheable block.
		lastMsg, ok := msgs[len(msgs)-1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected last message: %#v", msgs[len(msgs)-1])
		}
		content, ok := lastMsg["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("unexpected content blocks: %#v", lastMsg["content"])
		}
		lastBlock, ok := content[len(content)-1].(map[string]any)
		if !ok {
			t.Fatalf("unexpected content block: %#v", content[len(content)-1])
		}
		cacheCtl, ok = lastBlock["cache_control"].(map[string]any)
		if !ok || cacheCtl["type"] != "ephemeral" {
			t.Fatalf("expected cache_control on last block of final message, got %#v", lastBlock["cache_control"])
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
	content, ok := last.Content[0].Content.(string)
	if !ok {
		t.Fatalf("expected string tool_result content, got %#v", last.Content[0].Content)
	}
	if !strings.Contains(content, `{"exit_code":0}`) {
		t.Fatalf("expected tool output to be preserved, got %q", content)
	}
	if !strings.Contains(content, "<system-reminder>") {
		t.Fatalf("expected system reminder to be folded into tool_result, got %q", content)
	}
}

func TestBuildAnthropicRequest_ReplaysInvalidToolArgumentsWithErrorResult(t *testing.T) {
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "update TODO"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				{ID: "call_todo", Name: "update_todo", Arguments: `{"todos": `},
			}},
			{Role: "tool", ToolCallID: "call_todo", Name: "update_todo", Content: `{"error":"invalid tool arguments: unexpected EOF","ok":false}`},
		},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if len(payload.Messages) != 3 {
		t.Fatalf("expected user, assistant, tool result messages, got %+v", payload.Messages)
	}
	assistant := payload.Messages[1]
	if assistant.Role != "assistant" || len(assistant.Content) != 1 {
		t.Fatalf("expected assistant tool_use, got %+v", assistant)
	}
	toolUse := assistant.Content[0]
	if toolUse.Type != "tool_use" || toolUse.ID != "call_todo" || toolUse.Name != "update_todo" {
		t.Fatalf("unexpected tool_use block: %+v", toolUse)
	}
	input, ok := toolUse.Input.(map[string]any)
	if !ok || len(input) != 0 {
		t.Fatalf("expected invalid arguments to replay as empty Anthropic input object, got %#v", toolUse.Input)
	}
	result := payload.Messages[2]
	if result.Role != "user" || len(result.Content) != 1 {
		t.Fatalf("expected tool result message, got %+v", result)
	}
	if result.Content[0].Type != "tool_result" || result.Content[0].ToolUseID != "call_todo" {
		t.Fatalf("unexpected tool_result block: %+v", result.Content[0])
	}
	content, ok := result.Content[0].Content.(string)
	if !ok || !strings.Contains(content, "invalid tool arguments") {
		t.Fatalf("expected invalid tool arguments result, got %#v", result.Content[0].Content)
	}
}

func TestBuildAnthropicRequest_CacheBoundaryKeepsSystemReminderAfterToolResult(t *testing.T) {
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
		CacheHint: &providers.CacheHint{StablePrefixMessages: 3},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}

	if len(payload.Messages) != 3 {
		t.Fatalf("expected 3 messages after merge, got %d", len(payload.Messages))
	}
	if payload.Messages[1].Content[0].CacheControl != nil {
		t.Fatalf("did not expect cache marker to stop at tool_use, got %+v", payload.Messages[1].Content[0].CacheControl)
	}
	last := payload.Messages[2]
	if len(last.Content) != 2 {
		t.Fatalf("expected tool_result plus volatile reminder, got %+v", last.Content)
	}
	if last.Content[0].Type != "tool_result" {
		t.Fatalf("expected tool_result block, got %+v", last.Content[0])
	}
	if last.Content[0].CacheControl == nil || last.Content[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("expected cache marker on stable tool_result block, got %+v", last.Content[0].CacheControl)
	}
	content, ok := last.Content[0].Content.(string)
	if !ok {
		t.Fatalf("expected string tool_result content, got %#v", last.Content[0].Content)
	}
	if strings.Contains(content, "<system-reminder>") {
		t.Fatalf("did not expect volatile reminder in cached tool_result, got %q", content)
	}
	if last.Content[1].Type != "text" || !strings.Contains(last.Content[1].Text, "<system-reminder>") {
		t.Fatalf("expected volatile system reminder after cache boundary, got %+v", last.Content[1])
	}
	if last.Content[1].CacheControl != nil {
		t.Fatalf("did not expect cache marker on volatile reminder, got %+v", last.Content[1].CacheControl)
	}
}

func TestBuildAnthropicRequest_TailMarkerSkipsHiddenTrailingContext(t *testing.T) {
	reminder := wuucontext.FormatSystemReminder(wuucontext.EnvInfo{
		CWD:  "/tmp/project",
		Date: "2026-04-21",
	})

	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "check repo"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID: "tool_1", Name: "read_file", Arguments: `{"path":"README.md"}`,
			}}},
			{Role: "tool", ToolCallID: "tool_1", Name: "read_file", Content: `{"content":"hello"}`},
			{Role: "user", Name: wuucontext.SystemReminderMessageName, Content: reminder, Hidden: true},
		},
		CacheHint: &providers.CacheHint{StableSystem: true, TurnPrefixMessages: 1},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if len(payload.Messages) != 3 {
		t.Fatalf("expected 3 messages after merge, got %d", len(payload.Messages))
	}
	// The sliding tail marker lands on the last cacheable block from a
	// non-hidden source: the tool_result. The per-request reminder that
	// merged in after it stays outside the cached prefix, and the earlier
	// user message carries no marker of its own.
	first := payload.Messages[0]
	if first.Role != "user" || len(first.Content) != 1 {
		t.Fatalf("unexpected first message: %+v", first)
	}
	if first.Content[0].CacheControl != nil {
		t.Fatalf("did not expect marker on earlier user message, got %+v", first.Content[0].CacheControl)
	}
	last := payload.Messages[2]
	if len(last.Content) != 2 || last.Content[0].Type != "tool_result" || last.Content[1].Type != "text" {
		t.Fatalf("expected marked tool_result followed by reminder text, got %+v", last.Content)
	}
	if last.Content[0].CacheControl == nil || last.Content[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("expected sliding tail marker on stable tool_result, got %+v", last.Content[0].CacheControl)
	}
	if last.Content[1].CacheControl != nil {
		t.Fatalf("did not expect marker on volatile reminder, got %+v", last.Content[1].CacheControl)
	}
	if !strings.Contains(last.Content[1].Text, "<system-reminder>") {
		t.Fatalf("expected volatile reminder after the cache boundary, got %q", last.Content[1].Text)
	}
}

func TestBuildAnthropicRequest_ClampsOverlargeStablePrefixCacheHint(t *testing.T) {
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "first"},
			{Role: "assistant", Content: "stable reply"},
		},
		CacheHint: &providers.CacheHint{StablePrefixMessages: 99},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}

	if len(payload.Messages) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(payload.Messages))
	}
	last := payload.Messages[1]
	if len(last.Content) != 1 || last.Content[0].Type != "text" {
		t.Fatalf("expected assistant text block, got %+v", last.Content)
	}
	if last.Content[0].CacheControl == nil || last.Content[0].CacheControl.Type != "ephemeral" {
		t.Fatalf("expected cache marker on clamped stable prefix, got %+v", last.Content[0].CacheControl)
	}
}

func TestBuildAnthropicRequest_ScrubsClaudeToolCallIDs(t *testing.T) {
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-opus-4.7",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "check repo"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				{ID: "tool.1/2", Name: "git", Arguments: `{"subcommand":"status"}`},
			}},
			{Role: "tool", ToolCallID: "tool.1/2", Name: "git", Content: `{"exit_code":0}`},
		},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}

	if got := payload.Messages[1].Content[0].ID; got != "tool_1_2" {
		t.Fatalf("tool_use id = %q", got)
	}
	if got := payload.Messages[2].Content[0].ToolUseID; got != "tool_1_2" {
		t.Fatalf("tool_result id = %q", got)
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

func TestBuildAnthropicRequest_ToolsCarryNoCacheControl(t *testing.T) {
	// Tools render before system in Anthropic's cache-prefix order, so the
	// system-block marker already covers them; a per-tool marker would only
	// waste one of the four breakpoints.
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "read_file", InputSchema: map[string]any{"type": "object"}, CacheStable: true},
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}, CacheStable: true},
			{Name: "mcp_docs_search", InputSchema: map[string]any{"type": "object"}},
		},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if len(payload.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %+v", payload.Tools)
	}
	for i, tool := range payload.Tools {
		if tool.CacheControl != nil {
			t.Fatalf("did not expect cache_control on tool %d (%s): %+v", i, tool.Name, tool.CacheControl)
		}
	}
}

func TestBuildAnthropicRequest_ToolSearchNativeEnabledUsesToolReferences(t *testing.T) {
	payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
		Model:                       "claude-sonnet-4-5",
		NativeDeferredToolDiscovery: true,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "find docs"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID:        "search_1",
				Name:      "tool_search",
				Kind:      providers.ToolCallKindToolSearch,
				Arguments: `{"query":"docs"}`,
			}}},
			{
				Role:           "tool",
				Name:           "tool_search",
				ToolCallID:     "search_1",
				ToolResultKind: providers.ToolCallKindToolSearch,
				Content: `{
  "loadable_tools": [
    {
      "type": "function",
      "name": "mcp_docs_search",
      "description": "Search docs through MCP",
      "input_schema": {"type":"object","properties":{"query":{"type":"string"}}},
      "defer_loading": true
    }
  ]
}`,
			},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}, CacheStable: true},
			{Name: "mcp_docs_search", InputSchema: map[string]any{"type": "object"}},
		},
	}, 1024, false, anthropicToolSearchSupport{BaseURL: "https://api.anthropic.com"})
	if err != nil {
		t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
	}
	if len(payload.Betas) != 1 || payload.Betas[0] != toolSearchBetaHeader1P {
		t.Fatalf("expected tool search beta, got %+v", payload.Betas)
	}
	if len(payload.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %+v", payload.Tools)
	}
	if payload.Tools[0].DeferLoading {
		t.Fatalf("tool_search itself should not be defer_loading: %+v", payload.Tools[0])
	}
	if !payload.Tools[1].DeferLoading {
		t.Fatalf("discovered tool should be defer_loading: %+v", payload.Tools[1])
	}
	last := payload.Messages[len(payload.Messages)-1]
	if len(last.Content) != 1 || last.Content[0].Type != "tool_result" {
		t.Fatalf("unexpected final message: %+v", last)
	}
	refs, ok := last.Content[0].Content.([]anthropicBlock)
	if !ok || len(refs) != 1 {
		t.Fatalf("expected tool_reference content, got %#v", last.Content[0].Content)
	}
	if refs[0].Type != "tool_reference" || refs[0].ToolName != "mcp_docs_search" {
		t.Fatalf("unexpected tool_reference: %+v", refs[0])
	}
}

func TestBuildAnthropicRequest_RegularToolResultCanDiscoverDeferredTools(t *testing.T) {
	payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
		Model:                       "claude-sonnet-4-5",
		NativeDeferredToolDiscovery: true,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "start a reviewer"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID:        "spawn_1",
				Name:      "spawn_agent",
				Arguments: `{"description":"Review","prompt":"Review."}`,
			}}},
			{
				Role:       "tool",
				Name:       "spawn_agent",
				ToolCallID: "spawn_1",
				Content:    `{"action":"spawn_agent","agent_id":"agent_1"}`,
				DiscoveredTools: []providers.LoadableToolDefinition{{
					Type:        "function",
					Name:        "await_agents",
					Description: "Wait for subagents",
					InputSchema: map[string]any{"type": "object"},
				}},
			},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}, CacheStable: true},
			{Name: "spawn_agent", InputSchema: map[string]any{"type": "object"}, CacheStable: true},
			{Name: "await_agents", InputSchema: map[string]any{"type": "object"}, DeferLoading: true},
		},
	}, 1024, false, anthropicToolSearchSupport{BaseURL: "https://api.anthropic.com"})
	if err != nil {
		t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
	}
	if len(payload.Tools) != 3 {
		t.Fatalf("expected 3 tools, got %+v", payload.Tools)
	}
	if payload.Tools[1].Name != "spawn_agent" || payload.Tools[1].DeferLoading {
		t.Fatalf("spawn_agent should remain directly visible: %+v", payload.Tools[1])
	}
	if payload.Tools[2].Name != "await_agents" || !payload.Tools[2].DeferLoading {
		t.Fatalf("discovered management tool should stay defer_loading: %+v", payload.Tools[2])
	}

	last := payload.Messages[len(payload.Messages)-1]
	if len(last.Content) != 1 || last.Content[0].Type != "tool_result" {
		t.Fatalf("unexpected final message: %+v", last)
	}
	blocks, ok := last.Content[0].Content.([]anthropicBlock)
	if !ok || len(blocks) != 2 {
		t.Fatalf("expected text plus tool_reference content, got %#v", last.Content[0].Content)
	}
	if blocks[0].Type != "text" || blocks[0].Text == "" {
		t.Fatalf("expected original tool result text first, got %+v", blocks[0])
	}
	if blocks[1].Type != "tool_reference" || blocks[1].ToolName != "await_agents" {
		t.Fatalf("unexpected discovered tool_reference: %+v", blocks[1])
	}
}

func TestBuildAnthropicRequest_CompactedDiscoveredToolsRestoreAsVisibleTools(t *testing.T) {
	payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
		Model:                       "claude-sonnet-4-5",
		NativeDeferredToolDiscovery: true,
		Messages: []providers.ChatMessage{
			{
				Role:    "system",
				Content: "[Conversation summary]\nSummary:\nOlder turns discovered the docs search tool.",
				DiscoveredTools: []providers.LoadableToolDefinition{{
					Type:        "function",
					Name:        "mcp_docs_search",
					Description: "Search docs through MCP",
					InputSchema: map[string]any{"type": "object"},
				}},
			},
			{Role: "user", Content: "continue"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}, CacheStable: true},
			{Name: "mcp_docs_search", InputSchema: map[string]any{"type": "object"}, DeferLoading: true},
		},
	}, 1024, false, anthropicToolSearchSupport{BaseURL: "https://api.anthropic.com"})
	if err != nil {
		t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
	}
	if len(payload.Betas) != 1 || payload.Betas[0] != toolSearchBetaHeader1P {
		t.Fatalf("expected tool search beta, got %+v", payload.Betas)
	}
	if len(payload.Tools) != 2 {
		t.Fatalf("expected 2 tools, got %+v", payload.Tools)
	}
	if payload.Tools[1].Name != "mcp_docs_search" || payload.Tools[1].DeferLoading {
		t.Fatalf("compacted discovered tool should be restored as visible schema, got %+v", payload.Tools[1])
	}
}

func TestBuildAnthropicRequest_ToolSearchDisabledForProxyByDefault(t *testing.T) {
	payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
		Model: "claude-sonnet-4-5",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "find docs"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID:        "search_1",
				Name:      "tool_search",
				Kind:      providers.ToolCallKindToolSearch,
				Arguments: `{"query":"docs"}`,
			}}},
			{
				Role:           "tool",
				Name:           "tool_search",
				ToolCallID:     "search_1",
				ToolResultKind: providers.ToolCallKindToolSearch,
				Content:        `{"loadable_tools":[{"type":"function","name":"mcp_docs_search","input_schema":{"type":"object"}}]}`,
			},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}},
			{Name: "mcp_docs_search", InputSchema: map[string]any{"type": "object"}},
		},
	}, 1024, false, anthropicToolSearchSupport{BaseURL: "https://anthropic-proxy.example.com"})
	if err != nil {
		t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
	}
	if len(payload.Betas) != 0 {
		t.Fatalf("proxy default should not enable tool search beta, got %+v", payload.Betas)
	}
	if payload.Tools[1].DeferLoading {
		t.Fatalf("proxy default should not send defer_loading: %+v", payload.Tools[1])
	}
	last := payload.Messages[len(payload.Messages)-1]
	if _, ok := last.Content[0].Content.(string); !ok {
		t.Fatalf("proxy default should keep string tool_result content, got %#v", last.Content[0].Content)
	}
}

func TestBuildAnthropicRequest_ToolSearchDisabledWithoutNativeDeferredRequest(t *testing.T) {
	for _, tc := range []struct {
		name    string
		baseURL string
		model   string
	}{
		{name: "first party endpoint", baseURL: "https://api.anthropic.com", model: "claude-sonnet-4-5"},
		{name: "compatible proxy", baseURL: "https://anthropic-proxy.example.com", model: "claude-sonnet-4-5"},
		{name: "compatible model endpoint", baseURL: "https://compatible.example.com/anthropic", model: "generic-coder"},
	} {
		t.Run(tc.name, func(t *testing.T) {
			payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
				Model: tc.model,
				Messages: []providers.ChatMessage{
					{Role: "user", Content: "run a worker"},
				},
				Tools: []providers.ToolDefinition{
					{Name: "tool_search", InputSchema: map[string]any{"type": "object"}},
					{Name: "await_agents", InputSchema: map[string]any{"type": "object"}, DeferLoading: true},
				},
			}, 1024, false, anthropicToolSearchSupport{BaseURL: tc.baseURL})
			if err != nil {
				t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
			}
			if len(payload.Betas) != 0 {
				t.Fatalf("endpoint default should not enable tool search beta, got %+v", payload.Betas)
			}
			if payload.Tools[1].DeferLoading {
				t.Fatalf("endpoint default should not send defer_loading: %+v", payload.Tools[1])
			}
		})
	}
}

func TestBuildAnthropicRequest_ToolSearchEnabledForCompatibleEndpointWithExplicitOptIn(t *testing.T) {
	payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
		Model:                       "generic-coder",
		NativeDeferredToolDiscovery: true,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "run a worker"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}},
			{Name: "await_agents", InputSchema: map[string]any{"type": "object"}, DeferLoading: true},
		},
		ProviderOptions: map[string]any{"anthropicToolSearch": true},
	}, 1024, false, anthropicToolSearchSupport{BaseURL: "https://compatible.example.com/anthropic"})
	if err != nil {
		t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
	}
	if len(payload.Betas) != 1 || payload.Betas[0] != toolSearchBetaHeader1P {
		t.Fatalf("expected explicit opt-in tool search beta, got %+v", payload.Betas)
	}
	if len(payload.Tools) != 2 || !payload.Tools[1].DeferLoading {
		t.Fatalf("expected explicit opt-in to defer load management tool, got %+v", payload.Tools)
	}
}

func TestBuildAnthropicRequest_ToolSearchEnabledForCompatibleEndpointWithNativeDeferredRequest(t *testing.T) {
	payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
		Model: "generic-coder",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "run a worker"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}},
			{Name: "await_agents", InputSchema: map[string]any{"type": "object"}, DeferLoading: true},
		},
		NativeDeferredToolDiscovery: true,
	}, 1024, false, anthropicToolSearchSupport{BaseURL: "https://compatible.example.com/anthropic"})
	if err != nil {
		t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
	}
	if len(payload.Betas) != 1 || payload.Betas[0] != toolSearchBetaHeader1P {
		t.Fatalf("expected native-deferred request to enable tool search beta, got %+v", payload.Betas)
	}
	if len(payload.Tools) != 2 || !payload.Tools[1].DeferLoading {
		t.Fatalf("expected native-deferred request to defer load management tool, got %+v", payload.Tools)
	}
}

func TestBuildAnthropicRequest_ToolSearchDisabledForHaiku(t *testing.T) {
	payload, err := buildAnthropicRequestWithSupport(providers.ChatRequest{
		Model: "claude-3-5-haiku-latest",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "find docs"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", InputSchema: map[string]any{"type": "object"}},
			{Name: "mcp_docs_search", InputSchema: map[string]any{"type": "object"}, DeferLoading: true},
		},
	}, 1024, false, anthropicToolSearchSupport{BaseURL: "https://api.anthropic.com"})
	if err != nil {
		t.Fatalf("buildAnthropicRequestWithSupport: %v", err)
	}
	if len(payload.Betas) != 0 {
		t.Fatalf("haiku should not enable tool search beta, got %+v", payload.Betas)
	}
	if payload.Tools[1].DeferLoading {
		t.Fatalf("haiku should not send defer_loading: %+v", payload.Tools[1])
	}
}

func TestBuildAnthropicRequest_SplitsStableSystemBlocks(t *testing.T) {
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "base prompt"},
			{Role: "system", Content: "conversation summary"},
			{Role: "user", Content: "hello"},
		},
		CacheHint: &providers.CacheHint{StableSystem: true},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	blocks, ok := payload.System.([]anthropicSystemBlock)
	if !ok {
		t.Fatalf("expected system blocks, got %#v", payload.System)
	}
	if len(blocks) != 2 {
		t.Fatalf("expected two system blocks, got %+v", blocks)
	}
	if blocks[0].Text != "base prompt" || blocks[0].CacheControl != nil {
		t.Fatalf("unexpected first system block: %+v", blocks[0])
	}
	if blocks[1].Text != "conversation summary" || blocks[1].CacheControl == nil || blocks[1].CacheControl.Type != "ephemeral" {
		t.Fatalf("unexpected cached system boundary: %+v", blocks[1])
	}
}

func TestBuildAnthropicRequest_SendsProviderOptions(t *testing.T) {
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		ProviderOptions: map[string]any{
			"effort":      "high",
			"speed":       "fast",
			"temperature": 1.0,
			"topP":        0.95,
			"topK":        40,
			"thinking": map[string]any{
				"type":         "enabled",
				"budgetTokens": 4096,
				"display":      "none",
			},
		},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if payload.OutputConfig == nil || payload.OutputConfig.Effort != "high" {
		t.Fatalf("unexpected output_config: %+v", payload.OutputConfig)
	}
	if payload.Thinking == nil {
		t.Fatal("expected thinking payload")
	}
	if payload.Thinking.Type != "enabled" || payload.Thinking.BudgetTokens != 4096 || payload.Thinking.Display != "none" {
		t.Fatalf("unexpected thinking payload: %+v", payload.Thinking)
	}
	if payload.Speed != "fast" {
		t.Fatalf("unexpected speed: %q", payload.Speed)
	}
	if payload.Temperature == nil || *payload.Temperature != 1.0 {
		t.Fatalf("unexpected temperature: %+v", payload.Temperature)
	}
	if payload.TopP == nil || *payload.TopP != 0.95 {
		t.Fatalf("unexpected top_p: %+v", payload.TopP)
	}
	if payload.TopK == nil || *payload.TopK != 40 {
		t.Fatalf("unexpected top_k: %+v", payload.TopK)
	}
}

func TestBuildAnthropicRequest_DoesNotOverrideExplicitTemperatureWithProviderOption(t *testing.T) {
	payload, err := buildAnthropicRequest(providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		Temperature: 0.2,
		ProviderOptions: map[string]any{
			"temperature": 1.0,
		},
	}, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if payload.Temperature == nil || *payload.Temperature != 0.2 {
		t.Fatalf("unexpected temperature: %+v", payload.Temperature)
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

		// Check that middle message has no cache_control.
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
			t.Fatalf("did not expect cache_control on middle message when compact summary is present: %#v", secondBlock)
		}

		// Check that the last message (sliding tail marker) has cache_control.
		third, ok := msgs[2].(map[string]any)
		if !ok {
			t.Fatalf("unexpected third message: %#v", msgs[2])
		}
		thirdContent, ok := third["content"].([]any)
		if !ok || len(thirdContent) != 1 {
			t.Fatalf("unexpected third content payload: %#v", third["content"])
		}
		thirdBlock, ok := thirdContent[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected third block: %#v", thirdContent[0])
		}
		cacheControl, ok = thirdBlock["cache_control"].(map[string]any)
		if !ok || cacheControl["type"] != "ephemeral" {
			t.Fatalf("expected cache_control on last message tail, got %#v", thirdBlock["cache_control"])
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

		// With the new sliding tail marker strategy, the marker should be on the
		// last message only, not on the stable prefix boundary.

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
			t.Fatalf("did not expect cache_control on first message: %#v", textBlock)
		}

		// Second message (assistant "stable reply") — no cache_control (marker moved to tail).
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
		if _, exists := textBlock["cache_control"]; exists {
			t.Fatalf("did not expect cache_control on second message: %#v", textBlock)
		}

		// Third message (user "volatile ask") — has cache_control (sliding tail marker).
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
		cacheControl, ok = textBlock["cache_control"].(map[string]any)
		if !ok || cacheControl["type"] != "ephemeral" {
			t.Fatalf("expected cache_control on last message (sliding tail), got %#v", textBlock["cache_control"])
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
		Model:      "claude-test",
		MediaInput: providers.MediaInputPolicy{Image: true},
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

func TestChat_SendsDocumentBlocks(t *testing.T) {
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

		documentBlock, ok := content[1].(map[string]any)
		if !ok || documentBlock["type"] != "document" {
			t.Fatalf("unexpected document block: %#v", content[1])
		}
		source, ok := documentBlock["source"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected source payload: %#v", documentBlock["source"])
		}
		if source["type"] != "base64" || source["media_type"] != "application/pdf" || source["data"] != "JVBERi0xLjQ=" {
			t.Fatalf("unexpected document source: %#v", source)
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
		Model:      "claude-test",
		MediaInput: providers.MediaInputPolicy{File: true},
		Messages: []providers.ChatMessage{
			{
				Role:    "user",
				Content: "read this",
				Files: []providers.InputFile{
					{MediaType: "application/pdf", Data: "JVBERi0xLjQ=", Filename: "brief.pdf"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
}

func TestChat_AppliesCacheControlToStableTail(t *testing.T) {
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
		// Sliding tail: the earlier user message carries no marker; the
		// marker rides on the last cacheable block of the final message.
		firstLast := body.Messages[0].Content[len(body.Messages[0].Content)-1]
		if firstLast.CacheControl != nil {
			t.Fatalf("did not expect cache_control on earlier message, got %#v", firstLast.CacheControl)
		}
		if len(body.Messages[1].Content) == 0 {
			t.Fatal("expected follow-up content")
		}
		tailBlock := body.Messages[1].Content[len(body.Messages[1].Content)-1]
		if tailBlock.CacheControl == nil || tailBlock.CacheControl.Type != "ephemeral" {
			t.Fatalf("expected sliding tail cache_control on final message, got %#v", tailBlock.CacheControl)
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

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	req, err := providers.EnsureInferenceExecutionContext(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hi"},
		},
	}, providers.InferenceOperationAgentRound, providers.InferenceProfileBackgroundAgent)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := providers.ExecuteChat(context.Background(), client, req, providers.InferenceOperationAgentRound, providers.InferenceProfileBackgroundAgent)
	if err != nil {
		t.Fatalf("chat error: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	ledger := req.Execution.Snapshot()
	if ledger.Attempts != 2 || len(ledger.Submissions) != 2 {
		t.Fatalf("inference ledger = %+v, want one execution attempt and two physical submissions", ledger)
	}
	for _, submission := range ledger.Submissions {
		if submission.Provider != "anthropic" || submission.Protocol != "messages" || submission.Mode != "unary" {
			t.Fatalf("unexpected submission: %+v", submission)
		}
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
	var usageEvents []providers.StreamEvent
	for _, ev := range events {
		if ev.Type == providers.EventContentDelta {
			contentParts = append(contentParts, ev.Content)
		}
		if ev.Type == providers.EventUsage {
			usageEvents = append(usageEvents, ev)
		}
	}
	if len(contentParts) != 2 || contentParts[0] != "Hello" || contentParts[1] != " world" {
		t.Fatalf("unexpected content deltas: %v", contentParts)
	}
	if len(usageEvents) != 1 || usageEvents[0].Usage == nil || usageEvents[0].Usage.OutputTokens != 5 {
		t.Fatalf("unexpected usage events: %+v", usageEvents)
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

	if len(events) != 4 {
		t.Fatalf("expected thinking delta + thinking done + usage + done, got %d events", len(events))
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
	if events[2].Type != providers.EventUsage || events[2].Usage == nil || events[2].Usage.OutputTokens != 8 {
		t.Fatalf("expected usage event with output tokens, got %+v", events[2])
	}
	if events[3].Type != providers.EventDone {
		t.Fatalf("expected done event, got %+v", events[3])
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

func TestStreamChat_NativeErrorTypeClassifiesRetryable(t *testing.T) {
	// Native Anthropic error events carry error.type, not error.code. An
	// api_error with an atypical message must still classify as retryable
	// from the type alone, not fall to message-substring matching.
	ssePayload := "event: error\n" +
		"data: {\"type\":\"error\",\"error\":{\"type\":\"api_error\",\"message\":\"Something went sideways\"}}\n\n"

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
	if len(events) != 1 || events[0].Type != providers.EventError {
		t.Fatalf("expected a single error event, got %+v", events)
	}
	if events[0].Error == nil || !providers.IsRetryable(events[0].Error) {
		t.Fatalf("native api_error must classify retryable, got %v", events[0].Error)
	}
	var streamErr *providers.StreamError
	if !errors.As(events[0].Error, &streamErr) || streamErr.Code != "api_error" {
		t.Fatalf("expected StreamError with code=api_error, got %v", events[0].Error)
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

func TestReliableStreamChat_KimiUsageLimit403StopsWithoutReconnect(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		w.Header().Set("content-type", "application/json")
		w.WriteHeader(http.StatusForbidden)
		_, _ = fmt.Fprint(w, `{"error":{"type":"permission_error","message":"The usage limit has been reached"}}`)
	}))
	defer server.Close()

	rawClient, err := New(ClientConfig{BaseURL: server.URL, APIKey: "kimi-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	client := providers.NewReliableStreamClient(rawClient, nil)
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "kimi-for-coding",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hi"},
		},
		Operation: providers.NewInferenceOperation(
			providers.InferenceOperationAgentRound,
			providers.InferenceProfileInteractive,
		),
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var reconnects, finalErrors int
	for event := range ch {
		if event.Type == providers.EventLifecycle && event.Lifecycle != nil && event.Lifecycle.Phase == providers.StreamPhaseReconnecting {
			reconnects++
		}
		if event.Type == providers.EventError {
			finalErrors++
		}
	}

	if got := hits.Load(); got != 1 {
		t.Fatalf("physical requests = %d, want 1", got)
	}
	if reconnects != 0 || finalErrors != 1 {
		t.Fatalf("reconnects/final errors = %d/%d, want 0/1", reconnects, finalErrors)
	}
}

func TestStreamChat_RejectsInvalidMessageSequenceBeforeRequest(t *testing.T) {
	var hits atomic.Int32
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		hits.Add(1)
		t.Fatal("request should not reach server for invalid local history")
	}))
	defer server.Close()

	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "k"})
	_, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "m",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "system", Content: "late system"},
		},
	})
	if err == nil {
		t.Fatal("expected invalid sequence error")
	}
	if hits.Load() != 0 {
		t.Fatalf("expected zero requests, got %d", hits.Load())
	}
	if !strings.Contains(err.Error(), "invalid message sequence after tool-call history repair") {
		t.Fatalf("unexpected error: %v", err)
	}
}

func TestStampCacheCreationFlag(t *testing.T) {
	t.Run("explicit compatible endpoint flag forces unknown", func(t *testing.T) {
		client := &Client{baseURL: "https://compatible.example.com/anthropic", cacheCreationInputTokensOmitted: true}
		usage := &providers.TokenUsage{CacheCreationTokens: 12345}
		client.stampCacheCreationFlag(usage)
		if !usage.CacheCreationUnknown {
			t.Errorf("expected CacheCreationUnknown=true for explicitly flagged endpoint, got false (CacheCreationTokens=%d)", usage.CacheCreationTokens)
		}
	})
	t.Run("explicit flag forces unknown even when field present in payload", func(t *testing.T) {
		client := &Client{baseURL: "https://compatible.example.com/anthropic", cacheCreationInputTokensOmitted: true}
		usage := &providers.TokenUsage{CacheCreationTokens: 0}
		usage.CacheCreationUnknown = false
		client.stampCacheCreationFlag(usage)
		if !usage.CacheCreationUnknown {
			t.Error("expected stamp to override false to true for explicitly flagged endpoint")
		}
	})
	t.Run("anthropic native leaves flag at default", func(t *testing.T) {
		client := &Client{baseURL: "https://api.anthropic.com"}
		usage := &providers.TokenUsage{CacheCreationTokens: 12345}
		client.stampCacheCreationFlag(usage)
		if usage.CacheCreationUnknown {
			t.Error("expected CacheCreationUnknown=false for native anthropic endpoint")
		}
	})
	t.Run("nil usage does not panic", func(t *testing.T) {
		client := &Client{baseURL: "https://compatible.example.com/anthropic", cacheCreationInputTokensOmitted: true}
		client.stampCacheCreationFlag(nil)
	})
	t.Run("repeated stamps are idempotent", func(t *testing.T) {
		client := &Client{baseURL: "https://compatible.example.com/anthropic", cacheCreationInputTokensOmitted: true}
		usage := &providers.TokenUsage{}
		client.stampCacheCreationFlag(usage)
		client.stampCacheCreationFlag(usage)
		client.stampCacheCreationFlag(usage)
		if !usage.CacheCreationUnknown {
			t.Error("expected flag to remain true after repeated stamps")
		}
	})
}

func TestNormalizeInclusiveInput(t *testing.T) {
	t.Run("flag off leaves input unchanged (native anthropic)", func(t *testing.T) {
		client := &Client{baseURL: "https://api.anthropic.com"}
		usage := &providers.TokenUsage{InputTokens: 1000, CacheReadTokens: 800, OutputTokens: 50}
		client.normalizeInclusiveInput(usage)
		if usage.InputTokens != 1000 {
			t.Errorf("expected input unchanged=1000, got %d", usage.InputTokens)
		}
		if usage.CacheReadTokens != 800 {
			t.Errorf("expected cache_read preserved=800, got %d", usage.CacheReadTokens)
		}
	})
	t.Run("flag on subtracts cache_read from input, preserves cache_read", func(t *testing.T) {
		client := &Client{baseURL: "https://x.example.com", inputTokensIncludeCacheRead: true}
		usage := &providers.TokenUsage{InputTokens: 1000, CacheReadTokens: 800, OutputTokens: 50}
		client.normalizeInclusiveInput(usage)
		if usage.InputTokens != 200 {
			t.Errorf("expected fresh input=200, got %d", usage.InputTokens)
		}
		if usage.CacheReadTokens != 800 {
			t.Errorf("expected cache_read preserved=800, got %d", usage.CacheReadTokens)
		}
		// TotalContextTokens must equal input+output post-normalize (cache_read cancels).
		if got := usage.TotalContextTokens(); got != 200+800+50 {
			t.Errorf("TotalContextTokens formula unchanged: got %d, want %d", got, 1050)
		}
		// Equivalent occupancy resolves to raw_input+output.
		if usage.InputTokens+usage.OutputTokens != 250 {
			t.Errorf("expected input+output=250 (raw_input-cache_read+output), got %d", usage.InputTokens+usage.OutputTokens)
		}
	})
	t.Run("flag on floors negative fresh input at zero", func(t *testing.T) {
		client := &Client{baseURL: "https://x.example.com", inputTokensIncludeCacheRead: true}
		usage := &providers.TokenUsage{InputTokens: 500, CacheReadTokens: 900}
		client.normalizeInclusiveInput(usage)
		if usage.InputTokens != 0 {
			t.Errorf("expected floored input=0, got %d", usage.InputTokens)
		}
		if usage.CacheReadTokens != 900 {
			t.Errorf("expected cache_read preserved=900, got %d", usage.CacheReadTokens)
		}
	})
	t.Run("nil usage does not panic", func(t *testing.T) {
		client := &Client{baseURL: "https://x.example.com", inputTokensIncludeCacheRead: true}
		client.normalizeInclusiveInput(nil)
	})
	t.Run("no base_url auto-detection, explicit config only", func(t *testing.T) {
		// MiniMax's anthropic endpoint was live-probed EXCLUSIVE on
		// 2026-07-06; the former "minimaxi" substring auto-detect would
		// corrupt its (correct) usage by zeroing fresh input. Usage
		// semantics are volatile vendor behavior: the flag is an explicit
		// per-provider config decision, never inferred from the URL.
		client, err := New(ClientConfig{BaseURL: "https://api.minimaxi.com/anthropic", APIKey: "k"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if client.inputTokensIncludeCacheRead {
			t.Error("base_url must not auto-enable inclusive input normalization")
		}
		usage := &providers.TokenUsage{InputTokens: 1000, CacheReadTokens: 800}
		client.normalizeInclusiveInput(usage)
		if usage.InputTokens != 1000 {
			t.Errorf("expected exclusive input untouched=1000, got %d", usage.InputTokens)
		}
	})
	t.Run("native anthropic base_url does not auto-enable", func(t *testing.T) {
		client, err := New(ClientConfig{BaseURL: "https://api.anthropic.com", APIKey: "k"})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		if client.inputTokensIncludeCacheRead {
			t.Error("expected native anthropic to keep inclusive-input normalization off")
		}
	})
}

// TestChat_SlidingTailMarkerOnToolLoops verifies that the sliding tail marker
// strategy works correctly for intra-turn tool loops. Three successive requests
// represent rounds of a single turn: each adds assistant tool_call+tool result
// pairs, and the cache marker should move to the tail of each request.
func TestChat_SlidingTailMarkerOnToolLoops(t *testing.T) {
	callCount := 0
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		callCount++
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		msgs, ok := body["messages"].([]any)
		if !ok || len(msgs) == 0 {
			t.Fatalf("expected messages, got %#v", body["messages"])
		}

		// Count how many message pairs we have and verify the tail marker
		// is on the last message's last cacheable block in each round.
		// System marker should always be present.
		system, ok := body["system"].([]any)
		if !ok || len(system) != 1 {
			t.Fatalf("round %d: expected system block, got %#v", callCount, body["system"])
		}
		sysBlock, ok := system[0].(map[string]any)
		if !ok {
			t.Fatalf("round %d: unexpected system block: %#v", callCount, system[0])
		}
		cacheCtl, ok := sysBlock["cache_control"].(map[string]any)
		if !ok || cacheCtl["type"] != "ephemeral" {
			t.Fatalf("round %d: expected system cache_control, got %#v", callCount, sysBlock["cache_control"])
		}

		// Verify no middle messages have cache markers.
		for i := 0; i < len(msgs)-1; i++ {
			msg, ok := msgs[i].(map[string]any)
			if !ok {
				continue
			}
			content, ok := msg["content"].([]any)
			if !ok || len(content) == 0 {
				continue
			}
			for _, blk := range content {
				block, ok := blk.(map[string]any)
				if !ok {
					continue
				}
				if _, exists := block["cache_control"]; exists {
					t.Fatalf("round %d: did not expect cache_control on message %d: %#v", callCount, i, block)
				}
			}
		}

		// Verify the last message has exactly one cache_control on its tail.
		lastMsg, ok := msgs[len(msgs)-1].(map[string]any)
		if !ok {
			t.Fatalf("round %d: unexpected last message: %#v", callCount, msgs[len(msgs)-1])
		}
		content, ok := lastMsg["content"].([]any)
		if !ok || len(content) == 0 {
			t.Fatalf("round %d: unexpected content blocks: %#v", callCount, lastMsg["content"])
		}
		lastBlock, ok := content[len(content)-1].(map[string]any)
		if !ok {
			t.Fatalf("round %d: unexpected content block: %#v", callCount, content[len(content)-1])
		}
		cacheCtl, ok = lastBlock["cache_control"].(map[string]any)
		if !ok || cacheCtl["type"] != "ephemeral" {
			t.Fatalf("round %d: expected cache_control on tail, got %#v", callCount, lastBlock["cache_control"])
		}

		// Verify no tools carry cache_control.
		tools, ok := body["tools"].([]any)
		if ok {
			for _, toolAny := range tools {
				tool, ok := toolAny.(map[string]any)
				if !ok {
					continue
				}
				if _, exists := tool["cache_control"]; exists {
					t.Fatalf("round %d: did not expect cache_control on tool: %#v", callCount, tool)
				}
			}
		}

		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("new client: %v", err)
	}

	// Round 1: initial user request + system + tool definition
	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "run tool A"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_a", Description: "Tool A", InputSchema: map[string]any{"type": "object"}},
		},
		CacheHint: &providers.CacheHint{StableSystem: true, StablePrefixMessages: 0},
	})
	if err != nil {
		t.Fatalf("round 1 error: %v", err)
	}

	// Round 2: add assistant tool_call + tool result
	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "run tool A"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "tool_a", Arguments: "{}"},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "result of tool A"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_a", Description: "Tool A", InputSchema: map[string]any{"type": "object"}},
		},
		CacheHint: &providers.CacheHint{StableSystem: true, StablePrefixMessages: 0},
	})
	if err != nil {
		t.Fatalf("round 2 error: %v", err)
	}

	// Round 3: add another assistant tool_call + tool result
	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "claude-test",
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "run tool A"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				{ID: "call_1", Name: "tool_a", Arguments: "{}"},
			}},
			{Role: "tool", ToolCallID: "call_1", Content: "result of tool A"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{
				{ID: "call_2", Name: "tool_a", Arguments: "{}"},
			}},
			{Role: "tool", ToolCallID: "call_2", Content: "result of tool A again"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_a", Description: "Tool A", InputSchema: map[string]any{"type": "object"}},
		},
		CacheHint: &providers.CacheHint{StableSystem: true, StablePrefixMessages: 0},
	})
	if err != nil {
		t.Fatalf("round 3 error: %v", err)
	}

	if callCount != 3 {
		t.Fatalf("expected 3 chat calls, got %d", callCount)
	}
}

// TestChat_CacheTTLOption verifies that the cacheTTL provider option is
// correctly wired to the cache_control.ttl field in the request payload.
func TestChat_CacheTTLOption(t *testing.T) {
	testCases := []struct {
		name     string
		ttl      any
		expected string
	}{
		{"empty string uses default", "", ""},
		{"5m TTL is applied", "5m", "5m"},
		{"1h TTL is applied", "1h", "1h"},
		{"invalid TTL is ignored", "2h", ""},
		{"non-string option is ignored", 123, ""},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode request body: %v", err)
				}

				// Check system block cache_control TTL
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
				ttlVal, hasttl := cacheCtl["ttl"]
				if tc.expected == "" && hasttl {
					t.Fatalf("did not expect ttl field when empty, got %v", ttlVal)
				} else if tc.expected != "" && ttlVal != tc.expected {
					t.Fatalf("expected ttl=%q, got %v", tc.expected, ttlVal)
				}

				// Check message cache_control TTL
				msgs, ok := body["messages"].([]any)
				if !ok || len(msgs) == 0 {
					t.Fatalf("expected messages, got %#v", body["messages"])
				}
				lastMsg, ok := msgs[len(msgs)-1].(map[string]any)
				if !ok {
					t.Fatalf("unexpected last message: %#v", msgs[len(msgs)-1])
				}
				content, ok := lastMsg["content"].([]any)
				if !ok || len(content) == 0 {
					t.Fatalf("unexpected content blocks: %#v", lastMsg["content"])
				}
				lastBlock, ok := content[len(content)-1].(map[string]any)
				if !ok {
					t.Fatalf("unexpected content block: %#v", content[len(content)-1])
				}
				msgCacheCtl, ok := lastBlock["cache_control"].(map[string]any)
				if !ok || msgCacheCtl["type"] != "ephemeral" {
					t.Fatalf("expected cache_control on last block, got %#v", lastBlock["cache_control"])
				}
				msgTTLVal, hasMsgTTL := msgCacheCtl["ttl"]
				if tc.expected == "" && hasMsgTTL {
					t.Fatalf("did not expect ttl field in message when empty, got %v", msgTTLVal)
				} else if tc.expected != "" && msgTTLVal != tc.expected {
					t.Fatalf("expected message ttl=%q, got %v", tc.expected, msgTTLVal)
				}

				w.Header().Set("content-type", "application/json")
				_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
			}))
			defer server.Close()

			client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			opts := map[string]any{}
			if tc.ttl != nil {
				opts["cacheTTL"] = tc.ttl
			}

			_, err = client.Chat(context.Background(), providers.ChatRequest{
				Model: "claude-test",
				Messages: []providers.ChatMessage{
					{Role: "system", Content: "sys"},
					{Role: "user", Content: "hello"},
				},
				CacheHint:       &providers.CacheHint{StableSystem: true},
				ProviderOptions: opts,
			})
			if err != nil {
				t.Fatalf("chat error: %v", err)
			}
		})
	}
}

// TestChat_AnthropicCacheKillSwitch verifies that the anthropicCache=off
// option disables all cache_control markers in the request payload.
func TestChat_AnthropicCacheKillSwitch(t *testing.T) {
	testCases := []struct {
		name     string
		cache    any
		expected bool // whether cache_control should be present
	}{
		{"cache=auto enables markers", "auto", true},
		{"cache=off disables markers", "off", false},
		{"default enables markers", nil, true},
		{"empty string uses default", "", true},
	}

	for _, tc := range testCases {
		t.Run(tc.name, func(t *testing.T) {
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode request body: %v", err)
				}

				// Check if system block has cache_control
				system, ok := body["system"].([]any)
				if ok && len(system) > 0 {
					sysBlock, ok := system[0].(map[string]any)
					if ok {
						_, hasCacheControl := sysBlock["cache_control"]
						if tc.expected && !hasCacheControl {
							t.Fatalf("expected cache_control in system, got none")
						}
						if !tc.expected && hasCacheControl {
							t.Fatalf("did not expect cache_control in system, got %v", sysBlock["cache_control"])
						}
					}
				}

				// Check if message blocks have cache_control
				msgs, ok := body["messages"].([]any)
				if ok && len(msgs) > 0 {
					lastMsg, ok := msgs[len(msgs)-1].(map[string]any)
					if ok {
						content, ok := lastMsg["content"].([]any)
						if ok && len(content) > 0 {
							lastBlock, ok := content[len(content)-1].(map[string]any)
							if ok {
								_, hasCacheControl := lastBlock["cache_control"]
								if tc.expected && !hasCacheControl {
									t.Fatalf("expected cache_control in message, got none")
								}
								if !tc.expected && hasCacheControl {
									t.Fatalf("did not expect cache_control in message, got %v", lastBlock["cache_control"])
								}
							}
						}
					}
				}

				w.Header().Set("content-type", "application/json")
				_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"ok"}]}`))
			}))
			defer server.Close()

			client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
			if err != nil {
				t.Fatalf("new client: %v", err)
			}

			opts := map[string]any{}
			if tc.cache != nil {
				opts["anthropicCache"] = tc.cache
			}

			_, err = client.Chat(context.Background(), providers.ChatRequest{
				Model: "claude-test",
				Messages: []providers.ChatMessage{
					{Role: "system", Content: "sys"},
					{Role: "user", Content: "hello"},
				},
				CacheHint:       &providers.CacheHint{StableSystem: true},
				ProviderOptions: opts,
			})
			if err != nil {
				t.Fatalf("chat error: %v", err)
			}
		})
	}
}

func TestBuildAnthropicRequest_ForcedToolChoice(t *testing.T) {
	base := providers.ChatRequest{
		Model:    "claude-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "close out"}},
		Tools: []providers.ToolDefinition{
			{Name: "agent_report", InputSchema: map[string]any{"type": "object"}},
		},
	}

	forced := base
	forced.ForceToolName = "agent_report"
	payload, err := buildAnthropicRequest(forced, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	choice, ok := payload.ToolChoice.(map[string]any)
	if !ok || choice["type"] != "tool" || choice["name"] != "agent_report" {
		t.Fatalf("expected forced tool_choice, got %#v", payload.ToolChoice)
	}

	payload, err = buildAnthropicRequest(base, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if payload.ToolChoice != nil {
		t.Fatalf("expected no tool_choice without force, got %#v", payload.ToolChoice)
	}

	// Without tools the force must not leak into the wire payload.
	toolless := providers.ChatRequest{
		Model:         "claude-test",
		Messages:      []providers.ChatMessage{{Role: "user", Content: "close out"}},
		ForceToolName: "agent_report",
	}
	payload, err = buildAnthropicRequest(toolless, 1024, false)
	if err != nil {
		t.Fatalf("buildAnthropicRequest: %v", err)
	}
	if payload.ToolChoice != nil {
		t.Fatalf("expected no tool_choice without tools, got %#v", payload.ToolChoice)
	}
}

// TestStreamChat_MessageDeltaNormalizesInclusiveInput locks the streaming
// coverage of normalizeInclusiveInput at the message_delta site. Endpoints
// like MiniMax report the real usage only in message_delta (message_start
// carries input_tokens=0), so a flagged inclusive endpoint must be normalized
// exactly when the delta re-reports input_tokens — and must NOT be normalized
// twice when a later delta updates only other fields.
func TestStreamChat_MessageDeltaNormalizesInclusiveInput(t *testing.T) {
	ssePayload := "event: message_start\n" +
		"data: {\"type\":\"message_start\",\"message\":{\"usage\":{\"input_tokens\":0,\"output_tokens\":0}}}\n\n" +
		"event: content_block_start\n" +
		"data: {\"type\":\"content_block_start\",\"index\":0,\"content_block\":{\"type\":\"text\",\"text\":\"\"}}\n\n" +
		"event: content_block_delta\n" +
		"data: {\"type\":\"content_block_delta\",\"index\":0,\"delta\":{\"type\":\"text_delta\",\"text\":\"ok\"}}\n\n" +
		"event: content_block_stop\n" +
		"data: {\"type\":\"content_block_stop\",\"index\":0}\n\n" +
		// Inclusive endpoint: input 1000 includes the 800 cache read.
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"usage\":{\"input_tokens\":1000,\"output_tokens\":1,\"cache_read_input_tokens\":800}}\n\n" +
		// A second delta without input_tokens must not re-subtract.
		"event: message_delta\n" +
		"data: {\"type\":\"message_delta\",\"delta\":{\"stop_reason\":\"end_turn\"},\"usage\":{\"output_tokens\":2}}\n\n" +
		"event: message_stop\n" +
		"data: {\"type\":\"message_stop\"}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key", InputTokensIncludeCacheRead: true})
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
	if last.Usage.InputTokens != 200 {
		t.Fatalf("expected normalized fresh input 200 (1000-800, subtracted once), got %d", last.Usage.InputTokens)
	}
	if last.Usage.CacheReadTokens != 800 {
		t.Fatalf("expected cache_read preserved 800, got %d", last.Usage.CacheReadTokens)
	}
	if last.Usage.OutputTokens != 2 {
		t.Fatalf("expected output tokens 2, got %d", last.Usage.OutputTokens)
	}
}

// TestThinkingReplayModes locks the thinking_replay degradation ladder:
// "full" (default) replays native thinking blocks with signatures, "text"
// degrades reasoning to a plain text block (redacted dropped), "off" drops
// historical reasoning entirely. Compatible endpoints that reject thinking
// blocks or foreign signatures need the degraded tiers.
func TestThinkingReplayModes(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "user", Content: "question"},
		{Role: "assistant", Content: "answer", ReasoningBlocks: []providers.ReasoningBlock{
			{Type: "thinking", Thinking: "step one", Signature: "sig-abc"},
			{Type: "redacted_thinking", Data: "opaque"},
		}},
		{Role: "user", Content: "follow-up"},
	}
	build := func(mode string) anthropicRequest {
		opts := map[string]any{}
		if mode != "" {
			opts["thinking_replay"] = mode
		}
		req, err := buildAnthropicRequest(providers.ChatRequest{
			Model: "claude-test", Messages: history, ProviderOptions: opts,
		}, 1024, false)
		if err != nil {
			t.Fatalf("buildAnthropicRequest(%q): %v", mode, err)
		}
		return req
	}
	kinds := func(req anthropicRequest) []string {
		var out []string
		for _, b := range req.Messages[1].Content {
			out = append(out, b.Type)
		}
		return out
	}

	full := build("")
	if got := kinds(full); len(got) != 3 || got[0] != "thinking" || got[1] != "redacted_thinking" || got[2] != "text" {
		t.Fatalf("full replay blocks = %v, want [thinking redacted_thinking text]", got)
	}
	if signature := full.Messages[1].Content[0].Signature; signature == nil || *signature != "sig-abc" {
		t.Fatalf("full replay must preserve signature")
	}

	text := build("text")
	if got := kinds(text); len(got) != 2 || got[0] != "text" || got[1] != "text" {
		t.Fatalf("text replay blocks = %v, want [text text]", got)
	}
	if !strings.Contains(text.Messages[1].Content[0].Text, "step one") || strings.Contains(text.Messages[1].Content[0].Text, "opaque") {
		t.Fatalf("text replay must textify thinking and drop redacted payloads: %q", text.Messages[1].Content[0].Text)
	}

	off := build("off")
	if got := kinds(off); len(got) != 1 || got[0] != "text" {
		t.Fatalf("off replay blocks = %v, want [text]", got)
	}

	if got := kinds(build("bogus")); got[0] != "thinking" {
		t.Fatalf("invalid mode must fall back to full, got %v", got)
	}
}

func TestEmptyThinkingSignatureCompatibility(t *testing.T) {
	history := []providers.ChatMessage{
		{Role: "user", Content: "question"},
		{Role: "assistant", ReasoningBlocks: []providers.ReasoningBlock{
			{Type: "thinking", Thinking: "internal reasoning", Signature: ""},
		}},
		{Role: "user", Content: "follow-up"},
	}
	build := func(options map[string]any) anthropicRequest {
		req, err := buildAnthropicRequest(providers.ChatRequest{
			Model: "compatible-model", Messages: history, ProviderOptions: options,
		}, 1024, false)
		if err != nil {
			t.Fatalf("buildAnthropicRequest: %v", err)
		}
		return req
	}

	withoutCompat := build(nil)
	withoutCompatBlock := withoutCompat.Messages[1].Content[0]
	if withoutCompatBlock.Type != "thinking" || withoutCompatBlock.Signature != nil {
		t.Fatalf("default behavior must omit an empty signature, got %+v", withoutCompatBlock)
	}
	withoutCompatJSON, err := json.Marshal(withoutCompatBlock)
	if err != nil {
		t.Fatalf("marshal default thinking block: %v", err)
	}
	if strings.Contains(string(withoutCompatJSON), `"signature"`) {
		t.Fatalf("default behavior must omit signature from JSON, got %s", withoutCompatJSON)
	}

	withCompat := build(map[string]any{"allow_empty_signature": true})
	block := withCompat.Messages[1].Content[0]
	if block.Type != "thinking" || block.Signature == nil || *block.Signature != "" {
		t.Fatalf("compatible endpoint must preserve explicit empty signature, got %+v", block)
	}
	encoded, err := json.Marshal(block)
	if err != nil {
		t.Fatalf("marshal thinking block: %v", err)
	}
	var raw map[string]any
	if err := json.Unmarshal(encoded, &raw); err != nil {
		t.Fatalf("decode thinking block: %v", err)
	}
	if signature, present := raw["signature"]; !present || signature != "" {
		t.Fatalf("expected explicit empty signature in JSON, got %s", encoded)
	}
}

func TestKimiK3MultiRoundToolReplay(t *testing.T) {
	providerName, provider := modelcatalog.EnrichProvider("kimi-for-coding", config.ProviderConfig{
		Type:  "anthropic",
		Model: "k3",
	}, "k3")
	selection := modelvariant.ResolveForProvider(providerName, provider, "k3", "max", "")
	model := provider.Models["k3"]
	if model.Limit == nil {
		t.Fatal("expected K3 limits")
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/v1/messages" {
			t.Fatalf("unexpected K3 path: %s", r.URL.Path)
		}
		if got := r.Header.Get("User-Agent"); got != "KimiCLI/1.5" {
			t.Fatalf("unexpected K3 User-Agent: %q", got)
		}
		if got := r.Header.Get("anthropic-beta"); got != "" {
			t.Fatalf("Kimi request inherited Anthropic beta headers: %q", got)
		}
		var body anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode K3 request: %v", err)
		}
		if body.Model != "k3" || body.MaxTokens != 131_072 {
			t.Fatalf("unexpected K3 model request: model=%q max_tokens=%d", body.Model, body.MaxTokens)
		}
		if body.Thinking == nil || body.Thinking.Type != "adaptive" || body.Thinking.Display != "summarized" {
			t.Fatalf("unexpected K3 thinking config: %+v", body.Thinking)
		}
		if body.OutputConfig == nil || body.OutputConfig.Effort != "max" {
			t.Fatalf("unexpected K3 output config: %+v", body.OutputConfig)
		}
		if len(body.Messages) != 3 {
			t.Fatalf("unexpected K3 message count: %+v", body.Messages)
		}
		assistant := body.Messages[1]
		if assistant.Role != "assistant" || len(assistant.Content) != 3 {
			t.Fatalf("unexpected K3 assistant replay: %+v", assistant)
		}
		thinking := assistant.Content[0]
		if thinking.Type != "thinking" || thinking.Thinking == nil || *thinking.Thinking != "inspect the repository" || thinking.Signature == nil || *thinking.Signature != "" {
			t.Fatalf("unexpected K3 thinking replay: %+v", thinking)
		}
		if redacted := assistant.Content[1]; redacted.Type != "redacted_thinking" || redacted.Data != "opaque-reasoning" {
			t.Fatalf("unexpected K3 redacted replay: %+v", redacted)
		}
		if toolUse := assistant.Content[2]; toolUse.Type != "tool_use" || toolUse.ID != "call_1" || toolUse.Name != "list_files" {
			t.Fatalf("unexpected K3 tool replay: %+v", toolUse)
		}
		if followUp := body.Messages[2].Content; len(followUp) != 2 || followUp[0].Type != "tool_result" || followUp[0].ToolUseID != "call_1" || followUp[1].Type != "text" {
			t.Fatalf("unexpected K3 tool result sequence: %+v", followUp)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"done"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:   server.URL,
		APIKey:    "test-key",
		Headers:   provider.Headers,
		MaxTokens: model.Limit.Output,
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "k3",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "inspect the repository"},
			{
				Role: "assistant",
				ReasoningBlocks: []providers.ReasoningBlock{
					{Type: "thinking", Thinking: "inspect the repository", Signature: ""},
					{Type: "redacted_thinking", Data: "opaque-reasoning"},
				},
				ToolCalls: []providers.ToolCall{
					{ID: "call_1", Name: "list_files", Arguments: `{}`},
				},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "README.md"},
			{Role: "user", Content: "summarize it"},
		},
		ProviderOptions: selection.ProviderOptions,
	})
	if err != nil {
		t.Fatalf("K3 chat: %v", err)
	}
	if resp.Content != "done" {
		t.Fatalf("unexpected K3 response: %+v", resp)
	}
}

func TestMiniMaxCatalogPreservesDocumentedFeaturesWithoutAnthropicBetas(t *testing.T) {
	providerName, provider := modelcatalog.EnrichProvider("minimax", config.ProviderConfig{
		Type:  "anthropic",
		Model: "MiniMax-M3",
	}, "MiniMax-M3")
	selection := modelvariant.ResolveForProvider(providerName, provider, "MiniMax-M3", "", "")
	if got := selection.ProviderOptions["anthropic_default_betas"]; got != false {
		t.Fatalf("anthropic_default_betas = %#v, want false", got)
	}

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if got := r.Header.Get("anthropic-beta"); got != "" {
			t.Fatalf("MiniMax request inherited Anthropic beta headers: %q", got)
		}
		var body anthropicRequest
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode MiniMax request: %v", err)
		}
		if body.Thinking == nil || body.Thinking.Type != "adaptive" {
			t.Fatalf("MiniMax thinking = %+v", body.Thinking)
		}
		if len(body.Tools) != 1 || body.Tools[0].Name != "list_files" {
			t.Fatalf("MiniMax tools = %+v", body.Tools)
		}
		if len(body.Messages) != 1 || len(body.Messages[0].Content) != 1 || body.Messages[0].Content[0].CacheControl == nil {
			t.Fatalf("MiniMax cache_control marker missing: %+v", body.Messages)
		}
		w.Header().Set("content-type", "application/json")
		_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"done"}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key", MaxTokens: 1024})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:           "MiniMax-M3",
		Messages:        []providers.ChatMessage{{Role: "user", Content: "hello"}},
		Tools:           []providers.ToolDefinition{{Name: "list_files", InputSchema: map[string]any{"type": "object"}}},
		CacheHint:       &providers.CacheHint{StablePrefixMessages: 1},
		ProviderOptions: selection.ProviderOptions,
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

// TestTemperatureGating locks the per-model temperature switch and the
// thinking mutual exclusion: "temperature": false (arriving as the
// temperatureSupported option) or an active thinking config must drop the
// sampling field entirely.
func TestTemperatureGating(t *testing.T) {
	base := providers.ChatRequest{
		Model:       "claude-test",
		Temperature: 0.7,
		Messages:    []providers.ChatMessage{{Role: "user", Content: "hi"}},
	}
	build := func(req providers.ChatRequest) anthropicRequest {
		out, err := buildAnthropicRequest(req, 1024, false)
		if err != nil {
			t.Fatalf("buildAnthropicRequest: %v", err)
		}
		return out
	}

	if got := build(base); got.Temperature == nil || *got.Temperature != 0.7 {
		t.Fatalf("supported default must send temperature, got %+v", got.Temperature)
	}

	off := base
	off.ProviderOptions = map[string]any{"temperatureSupported": false}
	if got := build(off); got.Temperature != nil {
		t.Fatalf("temperatureSupported=false must drop temperature, got %v", *got.Temperature)
	}

	thinking := base
	thinking.Effort = "high" // enables adaptive thinking
	if got := build(thinking); got.Temperature != nil {
		t.Fatalf("active thinking must drop temperature, got %v", *got.Temperature)
	}
}

func TestLatestClaudeCatalogToolContinuation(t *testing.T) {
	for _, model := range []string{"claude-opus-5-5", "claude-fable-5-1"} {
		for _, effort := range []string{"", "none", "max"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				name, provider := modelcatalog.EnrichProvider("anthropic", config.ProviderConfig{Type: "anthropic", Model: model}, model)
				selection := modelvariant.ResolveForProvider(name, provider, model, effort, "")
				wantEffort := effort
				if effort == "" {
					wantEffort = provider.Models[model].DefaultVariant
				} else if effort == "none" {
					wantEffort = "low"
				}
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if !strings.Contains(r.Header.Get("anthropic-beta"), "thinking-binding-controls-2026-08-01") {
						t.Error("missing thinking binding header")
					}
					var body anthropicRequest
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(http.StatusBadRequest)
						return
					}
					if body.Thinking == nil || body.Thinking.Type != "adaptive" || body.Thinking.BudgetTokens != 0 || body.Thinking.Display != "summarized" || body.Thinking.BlockBinding["prefix_mismatch_behavior"] != "drop_block" {
						t.Errorf("invalid thinking request: %+v", body.Thinking)
					}
					if body.OutputConfig == nil || body.OutputConfig.Effort != wantEffort {
						t.Errorf("effort = %+v, want %s", body.OutputConfig, wantEffort)
					}
					if body.Temperature != nil || body.TopP != nil || body.TopK != nil {
						t.Error("unsupported sampling options reached the wire")
					}
					choice, ok := body.ToolChoice.(map[string]any)
					if !ok || choice["type"] != "auto" {
						t.Errorf("unsupported tool choice: %#v", body.ToolChoice)
					}
					if len(body.Messages) != 4 || body.Messages[3].Role != "system" {
						t.Errorf("lost tool continuation or closing instruction: %+v", body.Messages)
					} else {
						block := body.Messages[1].Content[0]
						if block.Signature == nil || *block.Signature != "signed-prefix" || block.Thinking == nil || *block.Thinking != "inspect files" {
							t.Errorf("signed thinking changed: %+v", block)
						}
						if body.Messages[2].Content[0].ToolUseID != "call_1" {
							t.Error("lost tool result")
						}
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"content":[{"type":"text","text":"done"}],"stop_reason":"end_turn"}`))
				}))
				defer server.Close()
				client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
				if err != nil {
					t.Fatal(err)
				}
				// Persisted options from an older model must not re-enable manual
				// budgets or sampling fields rejected by the selected model.
				selection.ProviderOptions["thinking"] = map[string]any{"type": "enabled", "budgetTokens": 4096}
				selection.ProviderOptions["temperature"] = 0.7
				selection.ProviderOptions["topP"] = 0.9
				selection.ProviderOptions["topK"] = 20
				resp, err := client.Chat(context.Background(), providers.ChatRequest{
					Model: model, Temperature: 0.7, ProviderOptions: selection.ProviderOptions,
					ForceToolName: "agent_report",
					Tools:         []providers.ToolDefinition{{Name: "agent_report", InputSchema: map[string]any{"type": "object"}}},
					Messages: []providers.ChatMessage{
						{Role: "system", Content: "Updated context after compaction"},
						{Role: "user", Content: "inspect the files"},
						{Role: "assistant", ReasoningBlocks: []providers.ReasoningBlock{{Type: "thinking", Thinking: "inspect files", Signature: "signed-prefix"}}, ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "list_files", Arguments: `{}`}}},
						{Role: "tool", ToolCallID: "call_1", Content: "README.md"},
					},
				})
				if err != nil || resp.Content != "done" {
					t.Fatalf("Chat = %+v, %v", resp, err)
				}
			})
		}
	}
}
