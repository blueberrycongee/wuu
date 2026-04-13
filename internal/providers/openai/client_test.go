package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestChat_SendsRequestAndParsesToolCall(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer test-key" {
			t.Fatalf("unexpected auth header: %q", got)
		}
		if got := r.Header.Get("X-Test"); got != "ok" {
			t.Fatalf("missing custom header, got %q", got)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["model"] != "gpt-test" {
			t.Fatalf("unexpected model: %v", body["model"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [
    {
      "message": {
        "content": "",
        "tool_calls": [
          {
            "id": "call_1",
            "type": "function",
            "function": {
              "name": "run_shell",
              "arguments": "{\"command\":\"ls\"}"
            }
          }
        ]
      }
    }
  ]
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		Headers: map[string]string{"X-Test": "ok"},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "run_shell", Description: "run shell", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(resp.ToolCalls))
	}
	if resp.ToolCalls[0].Name != "run_shell" {
		t.Fatalf("unexpected tool name: %s", resp.ToolCalls[0].Name)
	}
}

func TestChat_SendsPromptCacheKeyForOpenAICompatible(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["promptCacheKey"] != "cache-key-1" {
			t.Fatalf("expected promptCacheKey, got %#v", body["promptCacheKey"])
		}
		if _, exists := body["prompt_cache_key"]; exists {
			t.Fatalf("did not expect prompt_cache_key on standard OpenAI payload: %#v", body["prompt_cache_key"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		CacheHint: &providers.CacheHint{PromptCacheKey: "cache-key-1"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsSnakeCasePromptCacheKeyForOpenRouter(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["prompt_cache_key"] != "cache-key-2" {
			t.Fatalf("expected prompt_cache_key, got %#v", body["prompt_cache_key"])
		}
		if _, exists := body["promptCacheKey"]; exists {
			t.Fatalf("did not expect promptCacheKey on OpenRouter payload: %#v", body["promptCacheKey"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key", Headers: map[string]string{"HTTP-Referer": "https://openrouter.ai/app"}})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "openrouter-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		CacheHint: &providers.CacheHint{PromptCacheKey: "cache-key-2"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_OmitsPromptCacheKeyWithoutHint(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, exists := body["promptCacheKey"]; exists {
			t.Fatalf("did not expect promptCacheKey without hint: %#v", body["promptCacheKey"])
		}
		if _, exists := body["prompt_cache_key"]; exists {
			t.Fatalf("did not expect prompt_cache_key without hint: %#v", body["prompt_cache_key"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestStreamIdleTimeout_DefaultMatchesCodex(t *testing.T) {
	t.Setenv("WUU_STREAM_IDLE_TIMEOUT_MS", "")
	if got := streamIdleTimeout(); got != 5*time.Minute {
		t.Fatalf("expected 5m default stream idle timeout, got %s", got)
	}
}

func TestStreamConnectTimeout_DefaultMatchesCodexStyleSplitDeadline(t *testing.T) {
	t.Setenv("WUU_STREAM_CONNECT_TIMEOUT_MS", "")
	if got := streamConnectTimeout(); got != 30*time.Second {
		t.Fatalf("expected 30s default stream connect timeout, got %s", got)
	}
}

func TestChat_SendsImageContentParts(t *testing.T) {
	t.Helper()

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

		textPart, ok := content[0].(map[string]any)
		if !ok || textPart["type"] != "text" || textPart["text"] != "look at this" {
			t.Fatalf("unexpected text part: %#v", content[0])
		}

		imagePart, ok := content[1].(map[string]any)
		if !ok || imagePart["type"] != "image_url" {
			t.Fatalf("unexpected image part: %#v", content[1])
		}
		imageURL, ok := imagePart["image_url"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected image_url payload: %#v", imagePart["image_url"])
		}
		if imageURL["url"] != "data:image/png;base64,AAA" {
			t.Fatalf("unexpected image data url: %#v", imageURL["url"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{
				Role:    "user",
				Content: "look at this",
				Images: []providers.InputImage{
					{MediaType: "image/png", Data: "AAA"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsPromptCacheKeyAliases(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			PromptCacheKey string `json:"promptCacheKey"`
			AltCacheKey    string `json:"prompt_cache_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.PromptCacheKey != "cache-key-1" {
			t.Fatalf("unexpected promptCacheKey: %q", body.PromptCacheKey)
		}
		if body.AltCacheKey != "" {
			t.Fatalf("unexpected prompt_cache_key on OpenAI-compatible payload: %q", body.AltCacheKey)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		CacheHint: &providers.CacheHint{PromptCacheKey: "cache-key-1"},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsReasoningContentInAssistantToolCallMessage(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role             string `json:"role"`
				Content          any    `json:"content"`
				ReasoningContent string `json:"reasoning_content"`
				ToolCalls        []any  `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Messages) != 3 {
			t.Fatalf("expected 3 messages, got %d", len(body.Messages))
		}
		assistant := body.Messages[1]
		if assistant.Role != "assistant" {
			t.Fatalf("expected assistant role, got %q", assistant.Role)
		}
		if assistant.ReasoningContent != "inspect repo before tool use" {
			t.Fatalf("unexpected reasoning_content: %q", assistant.ReasoningContent)
		}
		if len(assistant.ToolCalls) != 1 {
			t.Fatalf("expected tool_calls to be present, got %#v", assistant.ToolCalls)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "review this repo"},
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

func TestChat_ParsesReasoningContent(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "choices": [
    {
      "message": {
        "content": "",
        "reasoning_content": "inspect repo before tool use",
        "tool_calls": [
          {
            "id": "call_1",
            "type": "function",
            "function": {
              "name": "list_files",
              "arguments": "{}"
            }
          }
        ]
      }
    }
  ]
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.ReasoningContent != "inspect repo before tool use" {
		t.Fatalf("unexpected reasoning content: %q", resp.ReasoningContent)
	}
}

func TestChat_HandlesProviderError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.WriteHeader(http.StatusBadRequest)
		_, _ = w.Write([]byte(`{"error":"bad request"}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err == nil {
		t.Fatal("expected provider error")
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
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
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
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected response content: %q", resp.Content)
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
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	})
	if err == nil {
		t.Fatal("expected auth error")
	}
	if got := attempts.Load(); got != 1 {
		t.Fatalf("expected 1 attempt for auth failure, got %d", got)
	}
}

func TestNewStreamingHTTPClient_DisablesOverallTimeout(t *testing.T) {
	base := &http.Client{
		Timeout:       5 * time.Second,
		Transport:     http.DefaultTransport,
		CheckRedirect: func(*http.Request, []*http.Request) error { return http.ErrUseLastResponse },
	}

	streamClient := newStreamingHTTPClient(base, providers.StreamTransportConfig{
		ConnectTimeout: time.Second,
		IdleTimeout:    5 * time.Second,
	})

	if streamClient == base {
		t.Fatal("expected streaming client to clone the base client")
	}
	if streamClient.Timeout != 0 {
		t.Fatalf("expected streaming client timeout disabled, got %s", streamClient.Timeout)
	}
	if streamClient.Transport == base.Transport {
		t.Fatal("expected streaming client transport to be cloned")
	}
	if streamClient.CheckRedirect == nil {
		t.Fatal("expected streaming client to preserve redirect policy")
	}
	if base.Timeout != 5*time.Second {
		t.Fatalf("expected base client timeout unchanged, got %s", base.Timeout)
	}
}

func TestStreamChat_ConnectTimeout(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		time.Sleep(200 * time.Millisecond)
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
		RetryConfig: &providers.RetryConfig{
			MaxRetries:   0,
			InitialDelay: time.Millisecond,
			MaxDelay:     time.Millisecond,
		},
		StreamConfig: &providers.StreamTransportConfig{
			ConnectTimeout: 50 * time.Millisecond,
			IdleTimeout:    time.Second,
		},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	start := time.Now()
	_, err = client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	elapsed := time.Since(start)
	if err == nil {
		t.Fatal("expected connect timeout error")
	}
	if elapsed >= 250*time.Millisecond {
		t.Fatalf("expected connect timeout to fail quickly, took %s", elapsed)
	}
}

func TestStreamChat_SSE(t *testing.T) {
	ssePayload := "data: {\"choices\":[{\"delta\":{\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"test.go\\\"}\"}}]}},{\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: [DONE]\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
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
		Model:    "gpt-test",
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

	// Verify tool call events.
	var toolStarts, toolEnds int
	var endToolCall *providers.ToolCall
	for _, ev := range events {
		switch ev.Type {
		case providers.EventToolUseStart:
			toolStarts++
			if ev.ToolCall == nil || ev.ToolCall.Name != "read_file" {
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
	if endToolCall == nil || endToolCall.ID != "call_1" {
		t.Fatalf("unexpected tool end call: %+v", endToolCall)
	}
	if endToolCall.Arguments != `{"path":"test.go"}` {
		t.Fatalf("unexpected tool arguments: %q", endToolCall.Arguments)
	}

	// Verify EventDone is the last event.
	last := events[len(events)-1]
	if last.Type != providers.EventDone {
		t.Fatalf("expected last event to be EventDone, got %s", last.Type)
	}
}

func TestStreamChat_EmitsThinkingEventsForReasoningContent(t *testing.T) {
	ssePayload := "data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"inspect \"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"reasoning_content\":\"repo\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"list_files\",\"arguments\":\"{}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n" +
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
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	if len(events) < 5 {
		t.Fatalf("expected thinking/tool events, got %v", events)
	}
	if events[0].Type != providers.EventThinkingDelta || events[0].Content != "inspect " {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != providers.EventThinkingDelta || events[1].Content != "repo" {
		t.Fatalf("unexpected second event: %+v", events[1])
	}
	if events[2].Type != providers.EventThinkingDone {
		t.Fatalf("expected thinking done before tool call, got %+v", events[2])
	}
	if events[3].Type != providers.EventToolUseStart {
		t.Fatalf("expected tool start after thinking, got %+v", events[3])
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

func TestStreamChat_MissingDoneYieldsIncompleteError(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
	}))
	defer server.Close()

	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "k"})
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "m",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
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
	if events[0].Type != providers.EventContentDelta || events[0].Content != "hi" {
		t.Fatalf("unexpected first event: %+v", events[0])
	}
	if events[1].Type != providers.EventError {
		t.Fatalf("expected terminal error, got %+v", events[1])
	}
	if events[1].Error == nil || !providers.IsRetryable(events[1].Error) {
		t.Fatalf("expected retryable incomplete stream error, got %v", events[1].Error)
	}
}

func TestStreamChat_IdleWatchdogFires(t *testing.T) {
	// Set a very short idle timeout for the test.
	t.Setenv("WUU_STREAM_IDLE_TIMEOUT_MS", "100")

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		// Write one chunk then hang forever — the watchdog should fire.
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"}}]}\n\n"))
		if f, ok := w.(http.Flusher); ok {
			f.Flush()
		}
		// Block until the client disconnects.
		<-r.Context().Done()
	}))
	defer server.Close()

	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "k"})
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "m",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var gotContent bool
	var gotError bool
	var errMsg string
	for ev := range ch {
		switch ev.Type {
		case providers.EventContentDelta:
			gotContent = true
		case providers.EventError:
			gotError = true
			if ev.Error != nil {
				errMsg = ev.Error.Error()
			}
		}
	}
	if !gotContent {
		t.Fatal("expected at least one content delta before timeout")
	}
	if !gotError {
		t.Fatal("expected error event from idle watchdog")
	}
	if !errors.Is(fmt.Errorf("wrap: %w", context.DeadlineExceeded), context.DeadlineExceeded) {
		t.Fatal("sanity check failed")
	}
	if errMsg == "" || !strings.Contains(errMsg, "idle timeout") {
		t.Fatalf("expected idle timeout error, got: %q", errMsg)
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

func TestChunkUsage_AsTokenUsage_Cached(t *testing.T) {
	// gpt-4o reports cached_tokens as a SUBSET of prompt_tokens. The
	// helper has to split it out so wuu's auto-compact accounts for
	// the cache portion explicitly.
	u := &chunkUsage{
		PromptTokens:     5000,
		CompletionTokens: 200,
		PromptTokensDetails: &struct {
			CachedTokens int `json:"cached_tokens,omitempty"`
		}{CachedTokens: 4500},
	}
	got := u.asTokenUsage()
	want := &providers.TokenUsage{
		InputTokens:     500, // 5000 - 4500
		OutputTokens:    200,
		CacheReadTokens: 4500,
	}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
	// And TotalContextTokens should still equal the original
	// prompt_tokens + completion_tokens.
	if total := got.TotalContextTokens(); total != 5200 {
		t.Fatalf("expected total 5200, got %d", total)
	}
}

func TestChunkUsage_AsTokenUsage_NoCacheDetails(t *testing.T) {
	// Older OpenAI / OpenRouter / proxy responses without
	// prompt_tokens_details should still parse cleanly.
	u := &chunkUsage{PromptTokens: 1000, CompletionTokens: 300}
	got := u.asTokenUsage()
	want := &providers.TokenUsage{InputTokens: 1000, OutputTokens: 300}
	if !reflect.DeepEqual(got, want) {
		t.Fatalf("got %+v, want %+v", got, want)
	}
}

func TestChunkUsage_AsTokenUsage_Nil(t *testing.T) {
	var u *chunkUsage
	if got := u.asTokenUsage(); got != nil {
		t.Fatalf("expected nil for nil receiver, got %+v", got)
	}
}
