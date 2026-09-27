package openai

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"reflect"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelcatalog"
	"github.com/blueberrycongee/wuu/internal/modelvariant"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
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
        "phase": "commentary",
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
	if resp.Phase != providers.MessagePhaseCommentary {
		t.Fatalf("unexpected phase: %q", resp.Phase)
	}
	if resp.ToolCalls[0].Name != "run_shell" {
		t.Fatalf("unexpected tool name: %s", resp.ToolCalls[0].Name)
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
		Model: "gpt-test",
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

func TestOpenAIToolSurfaceValidationTargetReservesFirstPartyNames(t *testing.T) {
	firstParty := openAIToolSurfaceValidationTarget("https://chatgpt.com/backend-api/codex/", "gpt-test")
	if !reflect.DeepEqual(firstParty.ReservedToolNames, []string{"browser"}) {
		t.Fatalf("first-party reserved names = %v", firstParty.ReservedToolNames)
	}

	compatible := openAIToolSurfaceValidationTarget("https://openai-compatible.example.com/v1", "gpt-test")
	if len(compatible.ReservedToolNames) != 0 {
		t.Fatalf("compatible endpoint inherited first-party reserved names: %v", compatible.ReservedToolNames)
	}
}

func TestChat_SendsMaxTokensAndReasoningEffort(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["max_tokens"] != float64(321) {
			t.Fatalf("expected max_tokens=321, got %#v", body["max_tokens"])
		}
		if body["reasoning_effort"] != "high" {
			t.Fatalf("expected reasoning_effort=high, got %#v", body["reasoning_effort"])
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
		Model:     "gpt-test",
		Messages:  []providers.ChatMessage{{Role: "user", Content: "hello"}},
		MaxTokens: 321,
		Effort:    "high",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsOpenRouterReasoningEffortShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Fatalf("did not expect reasoning_effort for OpenRouter payload: %#v", body)
		}
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "high" {
			t.Fatalf("expected reasoning.effort=high, got %#v", body["reasoning"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL + "/openrouter.ai/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "openai/gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		Effort:   "high",
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsProviderOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["reasoning_effort"] != "medium" {
			t.Fatalf("expected reasoning_effort=medium, got %#v", body["reasoning_effort"])
		}
		if body["verbosity"] != "low" {
			t.Fatalf("expected verbosity=low, got %#v", body["verbosity"])
		}
		if body["service_tier"] != "priority" {
			t.Fatalf("expected service_tier=priority, got %#v", body["service_tier"])
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
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{
			"reasoningEffort": "medium",
			"textVerbosity":   "low",
			"serviceTier":     "priority",
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsOpenRouterProviderOptionsShape(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, ok := body["reasoning_effort"]; ok {
			t.Fatalf("did not expect reasoning_effort for OpenRouter payload: %#v", body)
		}
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "high" {
			t.Fatalf("expected reasoning.effort=high, got %#v", body["reasoning"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"ok"}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL + "/openrouter.ai/v1", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "openai/gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{
			"reasoningEffort": "high",
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_OmitsPromptCacheKeyForUnsupportedCompatibleProvider(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, exists := body["promptCacheKey"]; exists {
			t.Fatalf("did not expect promptCacheKey: %#v", body)
		}
		if _, exists := body["prompt_cache_key"]; exists {
			t.Fatalf("did not expect prompt_cache_key: %#v", body)
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

func TestCatalogOpenAICompatibleWireContracts(t *testing.T) {
	tests := []struct {
		name         string
		providerID   string
		model        string
		variant      string
		wantEffort   string
		wantThinking string
	}{
		{
			name:         "GLM coding plan",
			providerID:   "zai-coding-plan",
			model:        "glm-5.2",
			wantThinking: "enabled",
		},
		{
			name:         "DeepSeek V4",
			providerID:   "deepseek",
			model:        "deepseek-v4-pro",
			variant:      "high",
			wantEffort:   "high",
			wantThinking: "enabled",
		},
	}

	for _, tc := range tests {
		t.Run(tc.name, func(t *testing.T) {
			providerName, provider := modelcatalog.EnrichProvider(tc.providerID, config.ProviderConfig{
				Type:  "openai-compatible",
				Model: tc.model,
			}, tc.model)
			selection := modelvariant.ResolveForProvider(providerName, provider, tc.model, tc.variant, "")

			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var body map[string]any
				if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
					t.Fatalf("decode request body: %v", err)
				}
				if body["model"] != tc.model {
					t.Fatalf("model = %#v", body["model"])
				}
				thinking, ok := body["thinking"].(map[string]any)
				if !ok || thinking["type"] != tc.wantThinking {
					t.Fatalf("thinking = %#v; body=%#v", body["thinking"], body)
				}
				if got, _ := body["reasoning_effort"].(string); got != tc.wantEffort {
					t.Fatalf("reasoning_effort = %q, want %q", got, tc.wantEffort)
				}
				if _, exists := body["prompt_cache_key"]; exists {
					t.Fatalf("compatible provider inherited prompt_cache_key: %#v", body)
				}
				if tools, ok := body["tools"].([]any); !ok || len(tools) != 1 {
					t.Fatalf("tools = %#v", body["tools"])
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
				Provider:        providerName,
				Model:           tc.model,
				Messages:        []providers.ChatMessage{{Role: "user", Content: "inspect the repository"}},
				Tools:           []providers.ToolDefinition{{Name: "list_files", InputSchema: map[string]any{"type": "object"}}},
				CacheHint:       &providers.CacheHint{PromptCacheKey: "thread-cache-key"},
				ProviderOptions: selection.ProviderOptions,
			})
			if err != nil {
				t.Fatalf("Chat: %v", err)
			}
		})
	}
}

func TestChat_FiltersUnsupportedProviderOptions(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		for _, key := range []string{"include", "toolStreaming", "thinkingConfig", "reasoningConfig", "modelParams", "gateway", "temperatureSupported", "temperature_supported", "promptCacheKeySupported"} {
			if _, exists := body[key]; exists {
				t.Fatalf("chat payload should filter %s: %#v", key, body)
			}
		}
		if body["metadata"] == nil {
			t.Fatalf("chat payload should keep ordinary provider options: %#v", body)
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
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{
			"include":                 []any{"reasoning.encrypted_content"},
			"toolStreaming":           false,
			"thinkingConfig":          map[string]any{"includeThoughts": true},
			"reasoningConfig":         map[string]any{"type": "enabled"},
			"modelParams":             map[string]any{"reasoning_effort": "high"},
			"gateway":                 map[string]any{"caching": "auto"},
			"promptCacheKeySupported": true,
			"temperatureSupported":    false,
			"temperature_supported":   false,
			"metadata":                map[string]any{"eval": "provider-options"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsSamplingProviderOptions(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["temperature"] != float64(1) {
			t.Fatalf("expected temperature=1, got %#v", body["temperature"])
		}
		if body["top_p"] != 0.95 {
			t.Fatalf("expected top_p=0.95, got %#v", body["top_p"])
		}
		if body["top_k"] != float64(40) {
			t.Fatalf("expected top_k=40, got %#v", body["top_k"])
		}
		if _, exists := body["topP"]; exists {
			t.Fatalf("did not expect camel-case topP on wire: %#v", body)
		}
		if _, exists := body["topK"]; exists {
			t.Fatalf("did not expect camel-case topK on wire: %#v", body)
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
		Model:    "minimax-m2.1",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{
			"temperature": 1.0,
			"topP":        0.95,
			"topK":        40,
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_DoesNotOverrideExplicitTemperatureWithProviderOption(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["temperature"] != 0.2 {
			t.Fatalf("expected explicit temperature=0.2, got %#v", body["temperature"])
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
		Model:       "minimax-m2.1",
		Messages:    []providers.ChatMessage{{Role: "user", Content: "hello"}},
		Temperature: 0.2,
		ProviderOptions: map[string]any{
			"temperature": 1.0,
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
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
		Model:      "gpt-test",
		MediaInput: providers.MediaInputPolicy{Image: true},
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

func TestChat_KeepsImagePartsWhenMergingAdjacentUserMessages(t *testing.T) {
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
		if !ok || len(content) != 3 {
			t.Fatalf("unexpected content payload: %#v", msg["content"])
		}
		first, ok := content[0].(map[string]any)
		if !ok || first["type"] != "text" || first["text"] != "look at this" {
			t.Fatalf("unexpected first part: %#v", content[0])
		}
		imagePart, ok := content[1].(map[string]any)
		if !ok || imagePart["type"] != "image_url" {
			t.Fatalf("unexpected image part: %#v", content[1])
		}
		imageURL, ok := imagePart["image_url"].(map[string]any)
		if !ok || imageURL["url"] != "data:image/png;base64,AAA" {
			t.Fatalf("unexpected image data url: %#v", imagePart["image_url"])
		}
		reminder, ok := content[2].(map[string]any)
		if !ok || reminder["type"] != "text" || reminder["text"] != "cwd: /tmp" {
			t.Fatalf("unexpected reminder part: %#v", content[2])
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
		Model:      "gpt-test",
		MediaInput: providers.MediaInputPolicy{Image: true},
		Messages: []providers.ChatMessage{
			{
				Role:    "user",
				Content: "look at this",
				Images: []providers.InputImage{
					{MediaType: "image/png", Data: "AAA"},
				},
			},
			{Role: "user", Content: "cwd: /tmp"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsFileContentParts(t *testing.T) {
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
		filePart, ok := content[1].(map[string]any)
		if !ok || filePart["type"] != "file" {
			t.Fatalf("unexpected file part: %#v", content[1])
		}
		filePayload, ok := filePart["file"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected file payload: %#v", filePart["file"])
		}
		if filePayload["filename"] != "brief.pdf" || filePayload["file_data"] != "data:application/pdf;base64,JVBERi0xLjQ=" {
			t.Fatalf("unexpected file payload: %#v", filePayload)
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
		Model:      "gpt-test",
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
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_KeepsFilePartsWhenMergingAdjacentUserMessages(t *testing.T) {
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
		if !ok || len(content) != 3 {
			t.Fatalf("unexpected content payload: %#v", msg["content"])
		}
		filePart, ok := content[1].(map[string]any)
		if !ok || filePart["type"] != "file" {
			t.Fatalf("unexpected file part: %#v", content[1])
		}
		filePayload, ok := filePart["file"].(map[string]any)
		if !ok || filePayload["filename"] != "brief.pdf" {
			t.Fatalf("unexpected file payload: %#v", filePart["file"])
		}
		reminder, ok := content[2].(map[string]any)
		if !ok || reminder["type"] != "text" || reminder["text"] != "cwd: /tmp" {
			t.Fatalf("unexpected reminder part: %#v", content[2])
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
		Model:      "gpt-test",
		MediaInput: providers.MediaInputPolicy{File: true},
		Messages: []providers.ChatMessage{
			{
				Role:    "user",
				Content: "read this",
				Files: []providers.InputFile{
					{MediaType: "application/pdf", Data: "JVBERi0xLjQ=", Filename: "brief.pdf"},
				},
			},
			{Role: "user", Content: "cwd: /tmp"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_SendsSupportedPromptCacheKey(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			CamelCacheKey  string `json:"promptCacheKey"`
			PromptCacheKey string `json:"prompt_cache_key"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body.PromptCacheKey != "cache-key-1" {
			t.Fatalf("unexpected prompt_cache_key: %q", body.PromptCacheKey)
		}
		if body.CamelCacheKey != "" {
			t.Fatalf("unexpected promptCacheKey: %q", body.CamelCacheKey)
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
		CacheHint:       &providers.CacheHint{PromptCacheKey: "cache-key-1"},
		ProviderOptions: map[string]any{"promptCacheKeySupported": true},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_AdjacentMessageWireHistory(t *testing.T) {
	cases := []struct {
		name     string
		model    string
		messages []providers.ChatMessage
		want     string
	}{
		{
			name: "text before tool call",
			messages: []providers.ChatMessage{
				{Role: "user", Content: "Read the file"},
				{Role: "assistant", Content: "I will read it."},
				{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_read", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
				{Role: "tool", ToolCallID: "call_read", Name: "read_file", Content: "file contents"},
			},
			want: `[
				{"role":"user","content":"Read the file"},
				{"role":"assistant","content":"I will read it."},
				{"role":"assistant","content":"","tool_calls":[{"id":"call_read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]},
				{"role":"tool","tool_call_id":"call_read","name":"read_file","content":"file contents"}
			]`,
		},
		{
			name: "reasoning and multiple tool results",
			messages: []providers.ChatMessage{
				{Role: "user", Content: "Compare files"},
				{Role: "assistant", Content: "Planning", ReasoningContent: "Need both files"},
				{Role: "assistant", Content: "Reading", ReasoningContent: "Read them together", ToolCalls: []providers.ToolCall{
					{ID: "call_a", Name: "read_file", Arguments: `{"path":"a.txt"}`},
					{ID: "call_b", Name: "read_file", Arguments: `{"path":"b.txt"}`},
				}},
				{Role: "tool", ToolCallID: "call_a", Name: "read_file", Content: "contents A"},
				{Role: "tool", ToolCallID: "call_b", Name: "read_file", Content: "contents B"},
			},
			want: `[
				{"role":"user","content":"Compare files"},
				{"role":"assistant","content":"Planning","reasoning_content":"Need both files"},
				{"role":"assistant","content":"Reading","reasoning_content":"Read them together","tool_calls":[
					{"id":"call_a","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"a.txt\"}"}},
					{"id":"call_b","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"b.txt\"}"}}
				]},
				{"role":"tool","tool_call_id":"call_a","name":"read_file","content":"contents A"},
				{"role":"tool","tool_call_id":"call_b","name":"read_file","content":"contents B"}
			]`,
		},
		{
			name: "reasoning around plain assistant text",
			messages: []providers.ChatMessage{
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "First", ReasoningContent: "First reasoning"},
				{Role: "assistant", Content: "Middle"},
				{Role: "assistant", Content: "Last", ReasoningContent: "Last reasoning"},
			},
			want: `[
				{"role":"user","content":"Continue"},
				{"role":"assistant","content":"First","reasoning_content":"First reasoning"},
				{"role":"assistant","content":"Middle"},
				{"role":"assistant","content":"Last","reasoning_content":"Last reasoning"}
			]`,
		},
		{
			name:  "deepseek empty reasoning",
			model: "deepseek-reasoner",
			messages: []providers.ChatMessage{
				{Role: "user", Content: "Continue"},
				{Role: "assistant", Content: "First", ReasoningContent: "First reasoning"},
				{Role: "assistant", Content: "Last"},
			},
			want: `[
				{"role":"user","content":"Continue"},
				{"role":"assistant","content":"First","reasoning_content":"First reasoning"},
				{"role":"assistant","content":"Last","reasoning_content":""}
			]`,
		},
		{
			name: "named participants",
			messages: []providers.ChatMessage{
				{Role: "user", Name: "alice", Content: "First"},
				{Role: "user", Name: "bob", Content: "Second"},
			},
			want: `[
				{"role":"user","name":"alice","content":"First"},
				{"role":"user","name":"bob","content":"Second"}
			]`,
		},
		{
			name: "safe text and media merges",
			messages: []providers.ChatMessage{
				{Role: "system", Content: "First instruction"},
				{Role: "system", Content: "Second instruction"},
				{Role: "user", Content: "Inspect", Images: []providers.InputImage{{MediaType: "image/png", Data: "AAAA"}}},
				{Role: "user", Files: []providers.InputFile{
					{MediaType: "application/pdf", Filename: "brief.pdf", Data: "BBBB"},
					{MediaType: "video/mp4", Data: "CCCC"},
				}},
				{Role: "user", Hidden: true, Content: "Runtime context"},
				{Role: "assistant", Content: "First reply"},
				{Role: "assistant", Content: "Second reply"},
			},
			want: `[
				{"role":"system","content":"First instruction\nSecond instruction"},
				{"role":"user","content":[
					{"type":"text","text":"Inspect"},
					{"type":"image_url","image_url":{"url":"data:image/png;base64,AAAA"}},
					{"type":"file","file":{"filename":"brief.pdf","file_data":"data:application/pdf;base64,BBBB"}},
					{"type":"video_url","video_url":{"url":"data:video/mp4;base64,CCCC"}},
					{"type":"text","text":"Runtime context"}
				]},
				{"role":"assistant","content":"First reply\nSecond reply"}
			]`,
		},
	}
	for _, tc := range cases {
		for _, stream := range []bool{false, true} {
			t.Run(fmt.Sprintf("%s/stream=%v", tc.name, stream), func(t *testing.T) {
				if err := providers.ValidateToolCallHistory(tc.messages); err != nil {
					t.Fatalf("invalid input history: %v", err)
				}
				original := providers.CloneChatMessages(tc.messages)
				wire := make(chan []byte, 1)
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					body, err := io.ReadAll(r.Body)
					if err != nil {
						t.Errorf("read request: %v", err)
						http.Error(w, "read request failed", http.StatusBadRequest)
						return
					}
					wire <- body
					if stream {
						w.Header().Set("Content-Type", "text/event-stream")
						_, _ = io.WriteString(w, "data: {\"choices\":[{\"delta\":{\"content\":\"ok\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
					} else {
						w.Header().Set("Content-Type", "application/json")
						_, _ = io.WriteString(w, `{"choices":[{"message":{"content":"ok"},"finish_reason":"stop"}]}`)
					}
				}))
				defer server.Close()
				client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test"})
				if err != nil {
					t.Fatal(err)
				}
				model := tc.model
				if model == "" {
					model = "gpt-test"
				}
				req := providers.ChatRequest{
					Model: model, Messages: tc.messages,
					ProviderOptions: map[string]any{"video_input": "video_url"},
				}
				if stream {
					events, err := client.StreamChat(context.Background(), req)
					if err != nil {
						t.Fatal(err)
					}
					for event := range events {
						if event.Error != nil {
							t.Fatal(event.Error)
						}
					}
				} else if _, err := client.Chat(context.Background(), req); err != nil {
					t.Fatal(err)
				}
				body := <-wire
				t.Logf("wire=%s", body)
				var request struct {
					Messages []chatMessage `json:"messages"`
				}
				if err := json.Unmarshal(body, &request); err != nil {
					t.Fatal(err)
				}
				var history []providers.ChatMessage
				for _, msg := range request.Messages {
					decoded := providers.ChatMessage{Role: msg.Role, ToolCallID: msg.ToolCallID}
					for _, call := range msg.ToolCalls {
						decoded.ToolCalls = append(decoded.ToolCalls, providers.ToolCall{ID: call.ID, Name: call.Function.Name, Arguments: call.Function.Arguments})
					}
					history = append(history, decoded)
				}
				if err := providers.ValidateToolCallHistory(history); err != nil {
					t.Errorf("valid input became invalid wire history: %v", err)
				}
				var want []chatMessage
				if err := json.Unmarshal([]byte(tc.want), &want); err != nil {
					t.Fatal(err)
				}
				if !reflect.DeepEqual(request.Messages, want) {
					t.Errorf("wire messages lost content or metadata; want %s", tc.want)
				}
				if !reflect.DeepEqual(tc.messages, original) {
					t.Error("request mapping mutated caller history")
				}
			})
		}
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

func TestChat_SendsEmptyReasoningContentForDeepSeekAssistantReplay(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []map[string]any `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Messages) != 2 {
			t.Fatalf("expected 2 messages, got %d", len(body.Messages))
		}
		assistant := body.Messages[1]
		value, exists := assistant["reasoning_content"]
		if !exists || value != "" {
			t.Fatalf("expected empty reasoning_content key, got exists=%v value=%#v body=%#v", exists, value, assistant)
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
		Model: "deepseek-v4",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", Content: "previous answer"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestChat_AppliesMistralMessageCompatibility(t *testing.T) {
	t.Helper()

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var body struct {
			Messages []struct {
				Role       string `json:"role"`
				Content    string `json:"content"`
				ToolCallID string `json:"tool_call_id"`
				ToolCalls  []struct {
					ID string `json:"id"`
				} `json:"tool_calls"`
			} `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if len(body.Messages) != 5 {
			t.Fatalf("expected assistant separator message, got %#v", body.Messages)
		}
		scrubbed := body.Messages[1].ToolCalls[0].ID
		if len(scrubbed) != 9 || scrubbed == "call_123456789_extra" {
			t.Fatalf("expected 9-char scrubbed Mistral tool call ID, got %#v", body.Messages)
		}
		if body.Messages[2].ToolCallID != scrubbed {
			t.Fatalf("expected matching scrubbed Mistral tool call IDs, got %#v", body.Messages)
		}
		if body.Messages[3].Role != "assistant" || body.Messages[3].Content != "Done." || body.Messages[4].Role != "user" {
			t.Fatalf("expected Done separator before user, got %#v", body.Messages)
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
		Model: "mistral-large-latest",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_123456789_extra", Name: "read", Arguments: `{}`}}},
			{Role: "tool", ToolCallID: "call_123456789_extra", Content: "ok"},
			{Role: "user", Content: "next"},
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

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "test-key",
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	req, err := providers.EnsureInferenceExecutionContext(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}, providers.InferenceOperationAgentRound, providers.InferenceProfileBackgroundAgent)
	if err != nil {
		t.Fatal(err)
	}
	resp, err := providers.ExecuteChat(context.Background(), client, req, providers.InferenceOperationAgentRound, providers.InferenceProfileBackgroundAgent)
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if resp.Content != "ok" {
		t.Fatalf("unexpected response content: %q", resp.Content)
	}
	if got := attempts.Load(); got != 2 {
		t.Fatalf("expected 2 attempts, got %d", got)
	}
	ledger := req.Execution.Snapshot()
	if ledger.Attempts != 2 || len(ledger.Submissions) != 2 {
		t.Fatalf("inference ledger = %+v, want one execution attempt and two physical submissions", ledger)
	}
	for _, submission := range ledger.Submissions {
		if submission.Protocol != "chat_completions" || submission.Transport != "http" || submission.Mode != "unary" {
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
		StreamConfig: &providers.StreamTransportConfig{
			ConnectTimeout: 50 * time.Millisecond,
			// Header wait is bounded separately from dial/TLS; this test
			// delays the response headers, so bound that stage tightly too.
			HeaderTimeout: 50 * time.Millisecond,
			IdleTimeout:   time.Second,
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
	ssePayload := "data: {\"choices\":[{\"delta\":{\"phase\":\"final_answer\",\"content\":\"Hello\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"content\":\" world\"}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"{\\\"path\\\":\"}}]}}]}\n\n" +
		"data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"function\":{\"arguments\":\"\\\"test.go\\\"}\"}}]}},{\"finish_reason\":\"tool_calls\"}]}\n\n" +
		"data: {\"choices\":[],\"usage\":{\"prompt_tokens\":10,\"completion_tokens\":3}}\n\n" +
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
	var phases []providers.MessagePhase
	for _, ev := range events {
		if ev.Type == providers.EventContentDelta {
			contentParts = append(contentParts, ev.Content)
			phases = append(phases, ev.Phase)
		}
	}
	if len(contentParts) != 2 || contentParts[0] != "Hello" || contentParts[1] != " world" {
		t.Fatalf("unexpected content deltas: %v", contentParts)
	}
	if len(phases) != 2 || phases[0] != providers.MessagePhaseFinalAnswer || phases[1] != providers.MessagePhaseFinalAnswer {
		t.Fatalf("unexpected content phases: %v", phases)
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
	var usageEvents []providers.StreamEvent
	for _, ev := range events {
		if ev.Type == providers.EventUsage {
			usageEvents = append(usageEvents, ev)
		}
	}
	if len(usageEvents) != 1 || usageEvents[0].Usage == nil || usageEvents[0].Usage.OutputTokens != 3 {
		t.Fatalf("unexpected usage events: %+v", usageEvents)
	}

	// Verify EventDone is the last event.
	last := events[len(events)-1]
	if last.Type != providers.EventDone {
		t.Fatalf("expected last event to be EventDone, got %s", last.Type)
	}
	if last.Usage == nil || last.Usage.InputTokens != 10 || last.Usage.OutputTokens != 3 {
		t.Fatalf("unexpected done usage: %+v", last.Usage)
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

func TestStreamChat_ExplicitFinishReasonAllowsCleanEOFWithoutDone(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"hi\"},\"finish_reason\":\"stop\"}]}\n\n"))
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
	if len(events) != 2 || events[0].Type != providers.EventContentDelta || events[1].Type != providers.EventDone {
		t.Fatalf("expected content delta + terminal done, got %+v", events)
	}
	if events[1].StopReason != "stop" || events[1].FinishReason != providers.FinishReasonStop {
		t.Fatalf("unexpected terminal event: %+v", events[1])
	}
}

func TestStreamChat_SingleChunkToolArgumentsSurviveCleanEOF(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"call_1\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":\"{\\\"path\\\":\\\"test.go\\\"}\"}}]},\"finish_reason\":\"tool_calls\"}]}\n\n"))
	}))
	defer server.Close()

	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "k"})
	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "m",
		Messages: []providers.ChatMessage{{Role: "user", Content: "read"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}
	var end *providers.ToolCall
	for _, event := range events {
		if event.Type == providers.EventToolUseEnd {
			end = event.ToolCall
		}
	}
	if end == nil || end.ID != "call_1" || end.Name != "read_file" || end.Arguments != `{"path":"test.go"}` {
		t.Fatalf("single-chunk tool call was not reconstructed: events=%+v", events)
	}
	if events[len(events)-1].Type != providers.EventDone || events[len(events)-1].FinishReason != providers.FinishReasonToolCalls {
		t.Fatalf("unexpected terminal event: %+v", events[len(events)-1])
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

func TestResponsesChat_SendsResponsesPayloadAndParsesToolCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if _, exists := body["messages"]; exists {
			t.Fatalf("responses payload must not include chat messages: %#v", body["messages"])
		}
		if body["model"] != "gpt-test" {
			t.Fatalf("unexpected model: %#v", body["model"])
		}
		if body["instructions"] != "sys" {
			t.Fatalf("unexpected instructions: %#v", body["instructions"])
		}
		if body["max_output_tokens"] != float64(123) {
			t.Fatalf("expected max_output_tokens=123, got %#v", body["max_output_tokens"])
		}
		if body["store"] != false {
			t.Fatalf("expected store=false by default, got %#v", body["store"])
		}

		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("unexpected tools payload: %#v", body["tools"])
		}
		tool, ok := tools[0].(map[string]any)
		if !ok || tool["type"] != "function" || tool["name"] != "read_file" {
			t.Fatalf("unexpected responses tool: %#v", tools[0])
		}
		if tool["strict"] != false {
			t.Fatalf("expected strict=false like Codex Responses tools, got %#v", tool["strict"])
		}
		if _, exists := tool["defer_loading"]; exists {
			t.Fatalf("ordinary responses tool must not include defer_loading: %#v", tool)
		}
		parameters, ok := tool["parameters"].(map[string]any)
		if !ok {
			t.Fatalf("unexpected parameters payload: %#v", tool["parameters"])
		}
		if properties, ok := parameters["properties"].(map[string]any); !ok || len(properties) != 0 {
			t.Fatalf("expected empty object properties for responses schema, got %#v", parameters["properties"])
		}
		if _, exists := tool["function"]; exists {
			t.Fatalf("responses tool must not use chat-completions function wrapper: %#v", tool)
		}

		input, ok := body["input"].([]any)
		if !ok || len(input) != 3 {
			t.Fatalf("unexpected input payload: %#v", body["input"])
		}
		callItem := input[1].(map[string]any)
		if callItem["type"] != "function_call" || callItem["call_id"] != "call_1" || callItem["name"] != "read_file" {
			t.Fatalf("unexpected function_call input: %#v", callItem)
		}
		outputItem := input[2].(map[string]any)
		if outputItem["type"] != "function_call_output" || outputItem["call_id"] != "call_1" || outputItem["output"] != "file contents" {
			t.Fatalf("unexpected function_call_output input: %#v", outputItem)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "status": "completed",
  "output": [
    {
      "type": "function_call",
      "call_id": "call_2",
      "name": "read_file",
      "arguments": "{\"path\":\"README.md\"}"
    }
  ],
  "usage": {
    "input_tokens": 10,
    "input_tokens_details": {"cached_tokens": 3},
    "output_tokens": 4
  }
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:     "gpt-test",
		MaxTokens: 123,
		Messages: []providers.ChatMessage{
			{Role: "system", Content: "sys"},
			{Role: "user", Content: "read README"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`}}},
			{Role: "tool", ToolCallID: "call_1", Content: "file contents"},
		},
		Tools: []providers.ToolDefinition{
			{Name: "read_file", Description: "read file", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].ID != "call_2" || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("unexpected tool calls: %+v", resp.ToolCalls)
	}
	if resp.ToolCalls[0].Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool args: %q", resp.ToolCalls[0].Arguments)
	}
	if resp.StopReason != "tool_calls" {
		t.Fatalf("expected tool_calls stop reason, got %q", resp.StopReason)
	}
	wantUsage := &providers.TokenUsage{InputTokens: 7, OutputTokens: 4, CacheReadTokens: 3}
	if !reflect.DeepEqual(resp.Usage, wantUsage) {
		t.Fatalf("got usage %+v, want %+v", resp.Usage, wantUsage)
	}
}

func TestResponsesChat_SerializesDeferredToolDefinition(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("unexpected tools payload: %#v", body["tools"])
		}
		tool, ok := tools[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected responses tool: %#v", tools[0])
		}
		if tool["defer_loading"] != true {
			t.Fatalf("expected defer_loading=true, got %#v", tool)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:                       "gpt-test",
		Messages:                    []providers.ChatMessage{{Role: "user", Content: "hello"}},
		NativeDeferredToolDiscovery: true,
		Tools: []providers.ToolDefinition{
			{
				Name:         "calendar_create",
				Description:  "create event",
				InputSchema:  map[string]any{"type": "object"},
				DeferLoading: true,
			},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesChat_SerializesToolSearchAsNativeToolAndParsesCall(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("unexpected tools payload: %#v", body["tools"])
		}
		tool, ok := tools[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected responses tool: %#v", tools[0])
		}
		if tool["type"] != "tool_search" || tool["execution"] != "client" {
			t.Fatalf("tool_search should use native Responses shape: %#v", tool)
		}
		if _, exists := tool["name"]; exists {
			t.Fatalf("native tool_search should not include function name: %#v", tool)
		}
		if _, exists := tool["strict"]; exists {
			t.Fatalf("native tool_search should not include strict: %#v", tool)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "status": "completed",
  "output": [
    {
      "type": "tool_search_call",
      "call_id": "search_1",
      "execution": "client",
      "arguments": {"query":"docs search","limit":1}
    }
  ]
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:                       "gpt-test",
		Messages:                    []providers.ChatMessage{{Role: "user", Content: "find a tool"}},
		NativeDeferredToolDiscovery: true,
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", Description: "Search deferred tools", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if len(resp.ToolCalls) != 1 {
		t.Fatalf("expected one tool call, got %+v", resp.ToolCalls)
	}
	call := resp.ToolCalls[0]
	if call.ID != "search_1" || call.Name != "tool_search" || call.Kind != providers.ToolCallKindToolSearch {
		t.Fatalf("unexpected tool_search call: %+v", call)
	}
	if call.Arguments != `{"query":"docs search","limit":1}` {
		t.Fatalf("unexpected tool_search arguments: %q", call.Arguments)
	}
}

func TestResponsesChat_RendersToolSearchHistoryAsNativeOutputAndOmitsDiscoveredTool(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("expected only tool_search top-level tool, got %#v", body["tools"])
		}
		topLevelTool := tools[0].(map[string]any)
		if topLevelTool["type"] != "tool_search" {
			t.Fatalf("discovered tool should not be reinjected as top-level function: %#v", tools)
		}

		input, ok := body["input"].([]any)
		if !ok || len(input) != 3 {
			t.Fatalf("unexpected input payload: %#v", body["input"])
		}
		callItem := input[1].(map[string]any)
		if callItem["type"] != "tool_search_call" || callItem["call_id"] != "search_1" || callItem["execution"] != "client" {
			t.Fatalf("unexpected tool_search_call input: %#v", callItem)
		}
		args, ok := callItem["arguments"].(map[string]any)
		if !ok || args["query"] != "docs search" {
			t.Fatalf("tool_search_call arguments should be an object, got %#v", callItem["arguments"])
		}
		outputItem := input[2].(map[string]any)
		if outputItem["type"] != "tool_search_output" || outputItem["call_id"] != "search_1" || outputItem["status"] != "completed" || outputItem["execution"] != "client" {
			t.Fatalf("unexpected tool_search_output input: %#v", outputItem)
		}
		outputTools, ok := outputItem["tools"].([]any)
		if !ok || len(outputTools) != 1 {
			t.Fatalf("unexpected tool_search_output tools: %#v", outputItem["tools"])
		}
		discovered := outputTools[0].(map[string]any)
		if discovered["type"] != "function" || discovered["name"] != "mcp_docs_search" || discovered["strict"] != false || discovered["defer_loading"] != true {
			t.Fatalf("unexpected loadable tool shape: %#v", discovered)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:                       "gpt-test",
		NativeDeferredToolDiscovery: true,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "find a docs tool"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID:        "search_1",
				Name:      "tool_search",
				Kind:      providers.ToolCallKindToolSearch,
				Arguments: `{"query":"docs search"}`,
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
			{Name: "tool_search", Description: "Search deferred tools", InputSchema: map[string]any{"type": "object"}},
			{Name: "mcp_docs_search", Description: "Search docs through MCP", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesRequest_KeepsTopLevelToolsStableAcrossToolSearchLifecycle(t *testing.T) {
	client := &Client{}
	toolSearch := providers.ToolDefinition{
		Name:        "tool_search",
		Description: "Search deferred tools",
		InputSchema: map[string]any{"type": "object"},
	}
	discovered := providers.ToolDefinition{
		Name:        "mcp_docs_search",
		Description: "Search docs through MCP",
		InputSchema: map[string]any{"type": "object"},
	}
	cacheHint := &providers.CacheHint{PromptCacheKey: "thread-cache-key"}

	base, err := client.buildResponsesRequest(providers.ChatRequest{
		Model:                       "gpt-test",
		Messages:                    []providers.ChatMessage{{Role: "user", Content: "find a docs tool"}},
		Tools:                       []providers.ToolDefinition{toolSearch},
		CacheHint:                   cacheHint,
		NativeDeferredToolDiscovery: true,
	}, false)
	if err != nil {
		t.Fatalf("build base request: %v", err)
	}

	followup, err := client.buildResponsesRequest(providers.ChatRequest{
		Model:                       "gpt-test",
		NativeDeferredToolDiscovery: true,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "find a docs tool"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID:        "search_1",
				Name:      "tool_search",
				Kind:      providers.ToolCallKindToolSearch,
				Arguments: `{"query":"docs search"}`,
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
		Tools:     []providers.ToolDefinition{toolSearch, discovered},
		CacheHint: cacheHint,
	}, false)
	if err != nil {
		t.Fatalf("build followup request: %v", err)
	}
	if !reflect.DeepEqual(base.Tools, followup.Tools) {
		t.Fatalf("top-level tools changed after tool_search history:\nbase=%+v\nfollowup=%+v", base.Tools, followup.Tools)
	}
	if len(followup.Tools) != 1 || followup.Tools[0].Type != "tool_search" {
		t.Fatalf("expected only native tool_search top-level tool, got %+v", followup.Tools)
	}
	if base.PromptCacheKey != "thread-cache-key" || followup.PromptCacheKey != base.PromptCacheKey {
		t.Fatalf("prompt cache key drifted: base=%q followup=%q", base.PromptCacheKey, followup.PromptCacheKey)
	}
	if len(followup.Input) != 3 {
		t.Fatalf("unexpected followup input: %+v", followup.Input)
	}
	outputItem := followup.Input[2]
	if outputItem.Type != "tool_search_output" || outputItem.Execution != "client" || outputItem.CallID != "search_1" {
		t.Fatalf("unexpected tool_search output item: %+v", outputItem)
	}
	outputTools, ok := outputItem.Tools.([]responsesToolDefinition)
	if !ok || len(outputTools) != 1 || outputTools[0].Name != "mcp_docs_search" || !outputTools[0].DeferLoading {
		t.Fatalf("unexpected tool_search output tools: %#v", outputItem.Tools)
	}

	compacted, err := client.buildResponsesRequest(providers.ChatRequest{
		Model:                       "gpt-test",
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
		Tools:     []providers.ToolDefinition{toolSearch, discovered},
		CacheHint: cacheHint,
	}, false)
	if err != nil {
		t.Fatalf("build compacted request: %v", err)
	}
	if !reflect.DeepEqual(base.Tools, compacted.Tools) {
		t.Fatalf("top-level tools changed after compact restore:\nbase=%+v\ncompacted=%+v", base.Tools, compacted.Tools)
	}
	if compacted.PromptCacheKey != base.PromptCacheKey {
		t.Fatalf("compacted prompt cache key drifted: base=%q compacted=%q", base.PromptCacheKey, compacted.PromptCacheKey)
	}
	if len(compacted.Input) != 2 || compacted.Input[0].Type != "additional_tools" || compacted.Input[0].Role != "developer" {
		t.Fatalf("unexpected compact restore input: %+v", compacted.Input)
	}
}

func TestResponsesRequest_SystemOnlyCompactionKeepsInputItem(t *testing.T) {
	client := &Client{}
	const summary = "[Conversation summary]\nContinue the current task."
	tests := []struct {
		name             string
		messages         []providers.ChatMessage
		wantInstructions string
	}{
		{
			name:     "summary only",
			messages: []providers.ChatMessage{{Role: "system", Content: summary}},
		},
		{
			name: "stable instructions and summary",
			messages: []providers.ChatMessage{
				{Role: "system", Content: "stable instructions"},
				{Role: "system", Content: summary},
			},
			wantInstructions: "stable instructions",
		},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			payload, err := client.buildResponsesRequest(providers.ChatRequest{
				Model:    "gpt-test",
				Messages: tt.messages,
			}, false)
			if err != nil {
				t.Fatalf("build request: %v", err)
			}
			if payload.Instructions != tt.wantInstructions {
				t.Fatalf("instructions = %q, want %q", payload.Instructions, tt.wantInstructions)
			}
			if len(payload.Input) != 1 {
				t.Fatalf("input = %+v, want one compacted system item", payload.Input)
			}
			if payload.Input[0].Role != "system" || payload.Input[0].Content != summary {
				t.Fatalf("compacted input item = %+v", payload.Input[0])
			}
		})
	}
}

func TestResponsesRequest_ForceToolNameSetsForcedToolChoice(t *testing.T) {
	client := &Client{}
	req := providers.ChatRequest{
		Model:         "gpt-test",
		Messages:      []providers.ChatMessage{{Role: "user", Content: "wrap up"}},
		Tools:         []providers.ToolDefinition{{Name: "submit_report", InputSchema: map[string]any{"type": "object"}}},
		ForceToolName: "submit_report",
	}

	payload, err := client.buildResponsesRequest(req, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	choice, ok := payload.ToolChoice.(map[string]any)
	if !ok {
		t.Fatalf("expected forced tool_choice map, got %T (%v)", payload.ToolChoice, payload.ToolChoice)
	}
	if choice["type"] != "function" || choice["name"] != "submit_report" {
		t.Fatalf("unexpected forced tool_choice: %+v", choice)
	}

	// The forced wire form must serialize flatter than Chat Completions
	// (no nested "function" object).
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal payload: %v", err)
	}
	if !strings.Contains(string(raw), `"tool_choice":{"name":"submit_report","type":"function"}`) &&
		!strings.Contains(string(raw), `"tool_choice":{"type":"function","name":"submit_report"}`) {
		t.Fatalf("forced tool_choice not serialized as expected: %s", raw)
	}

	// Without ForceToolName it stays "auto".
	req.ForceToolName = ""
	autoPayload, err := client.buildResponsesRequest(req, false)
	if err != nil {
		t.Fatalf("build auto request: %v", err)
	}
	if autoPayload.ToolChoice != "auto" {
		t.Fatalf("expected auto tool_choice, got %v", autoPayload.ToolChoice)
	}
}

func TestResponsesRequest_UsesFunctionShapesWhenNativeDeferredDisabled(t *testing.T) {
	client := &Client{}
	toolSearch := providers.ToolDefinition{
		Name:        "tool_search",
		Description: "Search deferred tools",
		InputSchema: map[string]any{"type": "object"},
	}
	discovered := providers.ToolDefinition{
		Name:        "mcp_docs_search",
		Description: "Search docs through MCP",
		InputSchema: map[string]any{"type": "object"},
	}

	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "find a docs tool"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID:        "search_1",
				Name:      "tool_search",
				Kind:      providers.ToolCallKindToolSearch,
				Arguments: `{"query":"docs search"}`,
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
      "input_schema": {"type":"object","properties":{"query":{"type":"string"}}}
    }
  ]
}`,
			},
		},
		Tools: []providers.ToolDefinition{toolSearch, discovered},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	if len(payload.Tools) != 2 {
		t.Fatalf("expected loaded tool to remain in top-level tools, got %+v", payload.Tools)
	}
	if payload.Tools[0].Type != "function" || payload.Tools[0].Name != "tool_search" {
		t.Fatalf("tool_search should serialize as ordinary function when native deferred is disabled: %+v", payload.Tools[0])
	}
	if payload.Tools[1].Type != "function" || payload.Tools[1].Name != "mcp_docs_search" || payload.Tools[1].DeferLoading {
		t.Fatalf("loaded tool should serialize as ordinary top-level function, got %+v", payload.Tools[1])
	}
	if len(payload.Input) != 3 {
		t.Fatalf("unexpected input items: %+v", payload.Input)
	}
	callItem := payload.Input[1]
	if callItem.Type != "function_call" || callItem.Name != "tool_search" || callItem.Arguments != `{"query":"docs search"}` {
		t.Fatalf("tool_search call should replay as ordinary function_call, got %+v", callItem)
	}
	outputItem := payload.Input[2]
	if outputItem.Type != "function_call_output" || outputItem.CallID != "search_1" || outputItem.Tools != nil {
		t.Fatalf("tool_search result should replay as ordinary function_call_output, got %+v", outputItem)
	}
}

func TestResponsesRequest_KeepsTopLevelToolsStableAcrossSpawnDiscovery(t *testing.T) {
	client := &Client{}
	toolSearch := providers.ToolDefinition{
		Name:        "tool_search",
		Description: "Search deferred tools",
		InputSchema: map[string]any{"type": "object"},
	}
	spawnAgent := providers.ToolDefinition{
		Name:        "spawn_agent",
		Description: "Start a subagent",
		InputSchema: map[string]any{"type": "object"},
	}
	awaitAgents := providers.ToolDefinition{
		Name:         "await_agents",
		Description:  "Wait for subagents",
		InputSchema:  map[string]any{"type": "object"},
		DeferLoading: true,
	}
	cacheHint := &providers.CacheHint{PromptCacheKey: "thread-cache-key"}

	base, err := client.buildResponsesRequest(providers.ChatRequest{
		Model:                       "gpt-test",
		Messages:                    []providers.ChatMessage{{Role: "user", Content: "start a reviewer"}},
		Tools:                       []providers.ToolDefinition{toolSearch, spawnAgent},
		CacheHint:                   cacheHint,
		NativeDeferredToolDiscovery: true,
	}, false)
	if err != nil {
		t.Fatalf("build base request: %v", err)
	}

	followup, err := client.buildResponsesRequest(providers.ChatRequest{
		Model:                       "gpt-test",
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
		Tools:     []providers.ToolDefinition{toolSearch, spawnAgent, awaitAgents},
		CacheHint: cacheHint,
	}, false)
	if err != nil {
		t.Fatalf("build followup request: %v", err)
	}
	if !reflect.DeepEqual(base.Tools, followup.Tools) {
		t.Fatalf("top-level tools changed after spawn discovery:\nbase=%+v\nfollowup=%+v", base.Tools, followup.Tools)
	}
	if len(followup.Tools) != 2 || followup.Tools[0].Type != "tool_search" || followup.Tools[1].Name != "spawn_agent" {
		t.Fatalf("expected only initial tool_search and spawn_agent top-level tools, got %+v", followup.Tools)
	}
	if followup.PromptCacheKey != base.PromptCacheKey {
		t.Fatalf("prompt cache key drifted: base=%q followup=%q", base.PromptCacheKey, followup.PromptCacheKey)
	}
	if len(followup.Input) != 4 {
		t.Fatalf("unexpected followup input: %+v", followup.Input)
	}
	if followup.Input[0].Type == "additional_tools" {
		t.Fatalf("ordinary spawn discovery should not be compacted to the request front: %+v", followup.Input)
	}
	if followup.Input[2].Type != "function_call_output" || followup.Input[2].CallID != "spawn_1" {
		t.Fatalf("expected spawn function output before discovery output, got %+v", followup.Input[2])
	}
	outputItem := followup.Input[3]
	if outputItem.Type != "additional_tools" || outputItem.Role != "developer" || outputItem.CallID != "" {
		t.Fatalf("unexpected discovered-tools output item: %+v", outputItem)
	}
	outputTools, ok := outputItem.Tools.([]responsesToolDefinition)
	if !ok || len(outputTools) != 1 || outputTools[0].Name != "await_agents" || outputTools[0].DeferLoading {
		t.Fatalf("unexpected discovered output tools: %#v", outputItem.Tools)
	}
}

func TestResponsesRequest_ReplaysReasoningBlocks(t *testing.T) {
	client := &Client{}
	reasoningRaw := `{"id":"rs_1","type":"reasoning","status":"completed","summary":[{"type":"summary_text","text":"inspect first"}],"encrypted_content":"enc_123"}`

	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "inspect"},
			{
				Role: "assistant",
				ReasoningBlocks: []providers.ReasoningBlock{{
					Type:     "reasoning",
					Thinking: "inspect first",
					Data:     reasoningRaw,
				}},
				ToolCalls: []providers.ToolCall{{
					ID:        "call_1",
					Name:      "read_file",
					Arguments: `{"path":"README.md"}`,
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "contents"},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	body, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	input, ok := decoded["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("unexpected input: %#v", decoded["input"])
	}
	reasoning, ok := input[1].(map[string]any)
	if !ok || reasoning["type"] != "reasoning" || reasoning["id"] != "rs_1" || reasoning["encrypted_content"] != "enc_123" {
		t.Fatalf("reasoning item was not replayed: %#v", input[1])
	}
	if _, exists := reasoning["status"]; exists {
		t.Fatalf("reasoning item must not replay the output-only status field: %#v", reasoning)
	}
	if input[2].(map[string]any)["type"] != "function_call" {
		t.Fatalf("expected function call after reasoning item, got %#v", input[2])
	}
}

func TestResponsesRequest_MessageItemsOmitStatus(t *testing.T) {
	client := &Client{}
	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "inspect"},
			{Role: "assistant", Content: "I'll inspect it.", ProviderItemID: "msg_1", ProviderItemModel: "gpt-test"},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	body, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	input := decoded["input"].([]any)
	msgItem, ok := input[1].(map[string]any)
	if !ok || msgItem["type"] != "message" {
		t.Fatalf("unexpected message input: %#v", input[1])
	}
	if _, exists := msgItem["status"]; exists {
		t.Fatalf("message input item must not include status: %#v", msgItem)
	}
}

func TestResponsesRequest_FunctionCallItemIDRequiresFCPrefix(t *testing.T) {
	client := &Client{}
	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "act"},
			{
				Role: "assistant",
				ToolCalls: []providers.ToolCall{{
					ID: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`,
					ProviderItemID: "ctc_custom_1", ProviderItemModel: "gpt-test",
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "contents"},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	body, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	input := decoded["input"].([]any)
	callItem, ok := input[1].(map[string]any)
	if !ok || callItem["type"] != "function_call" || callItem["call_id"] != "call_1" {
		t.Fatalf("unexpected function_call input: %#v", input[1])
	}
	if _, exists := callItem["id"]; exists {
		t.Fatalf("non-fc_ item id must be dropped from function_call input: %#v", callItem)
	}
}

func TestResponsesRequest_ClampsOversizedItemIDs(t *testing.T) {
	client := &Client{}
	longID := "fc_" + strings.Repeat("x", 80)
	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "act"},
			{
				Role: "assistant",
				ToolCalls: []providers.ToolCall{{
					ID: "call_1", Name: "read_file", Arguments: `{"path":"README.md"}`,
					ProviderItemID: longID, ProviderItemModel: "gpt-test",
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "contents"},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	body, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	input := decoded["input"].([]any)
	callItem := input[1].(map[string]any)
	id, _ := callItem["id"].(string)
	if len([]rune(id)) > 64 {
		t.Fatalf("item id must be clamped to 64 chars, got %d", len([]rune(id)))
	}
	if !strings.HasPrefix(id, "fc_") {
		t.Fatalf("item id lost fc_ prefix after clamping: %q", id)
	}
}

func TestResponsesRequest_EmitsOutputForEmptyToolResult(t *testing.T) {
	client := &Client{}
	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "act"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID: "call_1", Name: "computer", Arguments: `{"action":"press_key"}`,
			}}},
			{Role: "tool", ToolCallID: "call_1", ToolResult: &toolresult.Result{}},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}
	body, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	input := decoded["input"].([]any)
	output, ok := input[2].(map[string]any)
	if !ok || output["type"] != "function_call_output" {
		t.Fatalf("unexpected tool result input: %#v", input)
	}
	if output["output"] != "Tool completed without a textual result." {
		t.Fatalf("empty tool result output = %#v", output["output"])
	}
}

func TestResponsesRequest_ReplaysProviderItemIDsForSameModel(t *testing.T) {
	client := &Client{}

	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "inspect"},
			{
				Role:              "assistant",
				Content:           "I'll inspect it.",
				Phase:             providers.MessagePhaseCommentary,
				ProviderItemID:    "msg_1",
				ProviderItemModel: "gpt-test",
				ToolCalls: []providers.ToolCall{{
					ID:                "call_1",
					ProviderItemID:    "fc_1",
					ProviderItemModel: "gpt-test",
					Name:              "read_file",
					Arguments:         `{"path":"README.md"}`,
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "contents"},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	body, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	input, ok := decoded["input"].([]any)
	if !ok || len(input) != 4 {
		t.Fatalf("unexpected input: %#v", decoded["input"])
	}
	msgItem, ok := input[1].(map[string]any)
	if !ok || msgItem["type"] != "message" || msgItem["id"] != "msg_1" || msgItem["phase"] != "commentary" {
		t.Fatalf("assistant message item did not preserve id/phase: %#v", input[1])
	}
	content, ok := msgItem["content"].([]any)
	if !ok || len(content) != 1 {
		t.Fatalf("unexpected assistant message content: %#v", msgItem["content"])
	}
	contentPart, ok := content[0].(map[string]any)
	if !ok || contentPart["type"] != "output_text" || contentPart["text"] != "I'll inspect it." {
		t.Fatalf("unexpected assistant message content part: %#v", content[0])
	}
	callItem, ok := input[2].(map[string]any)
	if !ok || callItem["type"] != "function_call" || callItem["id"] != "fc_1" || callItem["call_id"] != "call_1" {
		t.Fatalf("function call item did not preserve Responses id: %#v", input[2])
	}
}

func TestResponsesRequest_OmitsProviderItemIDsForDifferentModel(t *testing.T) {
	client := &Client{}

	payload, err := client.buildResponsesRequest(providers.ChatRequest{
		Model: "gpt-next",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "inspect"},
			{
				Role:              "assistant",
				Content:           "I'll inspect it.",
				ProviderItemID:    "msg_1",
				ProviderItemModel: "gpt-test",
				ToolCalls: []providers.ToolCall{{
					ID:                "call_1",
					ProviderItemID:    "fc_1",
					ProviderItemModel: "gpt-test",
					Name:              "read_file",
					Arguments:         `{"path":"README.md"}`,
				}},
			},
			{Role: "tool", ToolCallID: "call_1", Content: "contents"},
		},
	}, false)
	if err != nil {
		t.Fatalf("build request: %v", err)
	}

	body, err := marshalResponsesRequest(payload)
	if err != nil {
		t.Fatalf("marshal request: %v", err)
	}
	var decoded map[string]any
	if err := json.Unmarshal(body, &decoded); err != nil {
		t.Fatalf("decode request: %v", err)
	}
	input := decoded["input"].([]any)
	msgItem := input[1].(map[string]any)
	if _, exists := msgItem["id"]; exists {
		t.Fatalf("assistant message item should omit foreign model id: %#v", msgItem)
	}
	callItem := input[2].(map[string]any)
	if _, exists := callItem["id"]; exists {
		t.Fatalf("function call item should omit foreign model id: %#v", callItem)
	}
}

func TestResponsesChat_RestoresCompactedDiscoveredToolsAsAdditionalTools(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		tools, ok := body["tools"].([]any)
		if !ok || len(tools) != 1 {
			t.Fatalf("expected only tool_search top-level tool, got %#v", body["tools"])
		}
		if tools[0].(map[string]any)["type"] != "tool_search" {
			t.Fatalf("unexpected top-level tools: %#v", tools)
		}

		input, ok := body["input"].([]any)
		if !ok || len(input) != 2 {
			t.Fatalf("unexpected input payload: %#v", body["input"])
		}
		outputItem := input[0].(map[string]any)
		if outputItem["type"] != "additional_tools" || outputItem["role"] != "developer" {
			t.Fatalf("unexpected compact restore item: %#v", outputItem)
		}
		outputTools, ok := outputItem["tools"].([]any)
		if !ok || len(outputTools) != 1 {
			t.Fatalf("unexpected compact restored tools: %#v", outputItem["tools"])
		}
		discovered := outputTools[0].(map[string]any)
		if discovered["name"] != "mcp_docs_search" {
			t.Fatalf("unexpected compact restored tool shape: %#v", discovered)
		}
		if _, exists := discovered["defer_loading"]; exists {
			t.Fatalf("additional_tools should expose callable tools directly: %#v", discovered)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:                       "gpt-test",
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
			{Name: "tool_search", Description: "Search deferred tools", InputSchema: map[string]any{"type": "object"}},
			{Name: "mcp_docs_search", Description: "Search docs through MCP", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesChat_RendersFailedToolSearchAsEmptyNativeOutput(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		input, ok := body["input"].([]any)
		if !ok || len(input) != 3 {
			t.Fatalf("unexpected input payload: %#v", body["input"])
		}
		outputItem := input[2].(map[string]any)
		if outputItem["type"] != "tool_search_output" || outputItem["call_id"] != "search_1" {
			t.Fatalf("unexpected tool_search_output input: %#v", outputItem)
		}
		outputTools, ok := outputItem["tools"].([]any)
		if !ok {
			t.Fatalf("tool_search_output must include tools array, got %#v", outputItem)
		}
		if len(outputTools) != 0 {
			t.Fatalf("expected empty tools array, got %#v", outputTools)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:                       "gpt-test",
		NativeDeferredToolDiscovery: true,
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "find a docs tool"},
			{Role: "assistant", ToolCalls: []providers.ToolCall{{
				ID:        "search_1",
				Name:      "tool_search",
				Kind:      providers.ToolCallKindToolSearch,
				Arguments: `{"query":"docs search"}`,
			}}},
			{
				Role:           "tool",
				Name:           "tool_search",
				ToolCallID:     "search_1",
				ToolResultKind: providers.ToolCallKindToolSearch,
				Content:        `{"error":"tool_search failed"}`,
			},
		},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", Description: "Search deferred tools", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesChat_FiltersUnsupportedProviderOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		for _, key := range []string{"toolStreaming", "thinkingConfig", "reasoningConfig", "modelParams", "gateway", "usage", "chat_template_args", "enable_thinking", "thinking", "temperatureSupported", "temperature_supported", "promptCacheKeySupported"} {
			if _, exists := body[key]; exists {
				t.Fatalf("responses payload should filter %s: %#v", key, body)
			}
		}
		if _, exists := body["include"]; !exists {
			t.Fatalf("responses payload should keep include: %#v", body)
		}
		if body["metadata"] == nil {
			t.Fatalf("responses payload should keep ordinary provider options: %#v", body)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{
			"include":                 []any{"reasoning.encrypted_content"},
			"toolStreaming":           false,
			"thinkingConfig":          map[string]any{"includeThoughts": true},
			"reasoningConfig":         map[string]any{"type": "enabled"},
			"modelParams":             map[string]any{"reasoning_effort": "high"},
			"gateway":                 map[string]any{"caching": "auto"},
			"usage":                   map[string]any{"include": true},
			"chat_template_args":      map[string]any{"enable_thinking": true},
			"enable_thinking":         true,
			"thinking":                map[string]any{"type": "enabled"},
			"promptCacheKeySupported": true,
			"temperatureSupported":    false,
			"temperature_supported":   false,
			"metadata":                map[string]any{"eval": "provider-options"},
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesChat_SendsSamplingProviderOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		if body["temperature"] != float64(1) {
			t.Fatalf("expected temperature=1, got %#v", body["temperature"])
		}
		if body["top_p"] != 0.95 {
			t.Fatalf("expected top_p=0.95, got %#v", body["top_p"])
		}
		if body["top_k"] != float64(20) {
			t.Fatalf("expected top_k=20, got %#v", body["top_k"])
		}
		if _, exists := body["topP"]; exists {
			t.Fatalf("did not expect camel-case topP on wire: %#v", body)
		}
		if _, exists := body["topK"]; exists {
			t.Fatalf("did not expect camel-case topK on wire: %#v", body)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "minimax-m2",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{
			"temperature": 1.0,
			"topP":        0.95,
			"topK":        20,
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesChat_SendsFileContentParts(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}

		input, ok := body["input"].([]any)
		if !ok || len(input) != 1 {
			t.Fatalf("unexpected input payload: %#v", body["input"])
		}
		item, ok := input[0].(map[string]any)
		if !ok {
			t.Fatalf("unexpected input item: %#v", input[0])
		}
		content, ok := item["content"].([]any)
		if !ok || len(content) != 2 {
			t.Fatalf("unexpected content payload: %#v", item["content"])
		}
		filePart, ok := content[1].(map[string]any)
		if !ok || filePart["type"] != "input_file" {
			t.Fatalf("unexpected file part: %#v", content[1])
		}
		if filePart["filename"] != "brief.pdf" || filePart["file_data"] != "data:application/pdf;base64,JVBERi0xLjQ=" {
			t.Fatalf("unexpected file part: %#v", filePart)
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "status": "completed",
  "output": [{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:      "gpt-test",
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
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesChat_SendsProviderOptions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}

		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatalf("decode request body: %v", err)
		}
		reasoning, ok := body["reasoning"].(map[string]any)
		if !ok || reasoning["effort"] != "max" || reasoning["summary"] != "auto" {
			t.Fatalf("unexpected reasoning payload: %#v", body["reasoning"])
		}
		text, ok := body["text"].(map[string]any)
		if !ok || text["verbosity"] != "low" {
			t.Fatalf("unexpected text payload: %#v", body["text"])
		}
		if body["max_output_tokens"] != float64(777) {
			t.Fatalf("expected max_output_tokens=777, got %#v", body["max_output_tokens"])
		}
		if body["service_tier"] != "priority" {
			t.Fatalf("expected service_tier=priority, got %#v", body["service_tier"])
		}

		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "status": "completed",
  "output": [{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		ProviderOptions: map[string]any{
			"reasoningEffort":  "max",
			"reasoningSummary": "auto",
			"textVerbosity":    "low",
			"maxOutputTokens":  777,
			"serviceTier":      "priority",
		},
	})
	if err != nil {
		t.Fatalf("Chat: %v", err)
	}
}

func TestResponsesChat_ParsesMessageContent(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "status": "completed",
  "output": [
    {
      "type": "message",
      "role": "assistant",
      "phase": "final_answer",
      "content": [
        {"type": "output_text", "text": "hello"},
        {"type": "output_text", "text": "world"}
      ]
    }
  ]
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
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
	if resp.Content != "hello\nworld" {
		t.Fatalf("unexpected content: %q", resp.Content)
	}
	if resp.Phase != providers.MessagePhaseFinalAnswer {
		t.Fatalf("unexpected phase: %q", resp.Phase)
	}
}

func TestResponsesChat_ContextLengthExceededClassified(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{
  "status": "failed",
  "error": {
    "code": "context_length_exceeded",
    "message": "Your input exceeds the context window of this model. Please adjust your input and try again."
  }
}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	_, err = client.Chat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if !providers.IsContextOverflow(err) {
		t.Fatalf("expected context overflow error, got %T (%v)", err, err)
	}
}

func TestResponsesStreamChat_SSE(t *testing.T) {
	ssePayload := "event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"status\":\"in_progress\"},\"output_index\":0}\n\n" +
		"event: response.output_text.delta\n" +
		"data: {\"type\":\"response.output_text.delta\",\"delta\":\"Hello\"}\n\n" +
		"event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"status\":\"in_progress\",\"arguments\":\"\",\"call_id\":\"call_1\",\"name\":\"read_file\"},\"output_index\":0}\n\n" +
		"event: response.function_call_arguments.delta\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"{\\\"path\\\":\",\"item_id\":\"fc_1\",\"output_index\":0}\n\n" +
		"event: response.function_call_arguments.delta\n" +
		"data: {\"type\":\"response.function_call_arguments.delta\",\"delta\":\"\\\"README.md\\\"}\",\"item_id\":\"fc_1\",\"output_index\":0}\n\n" +
		"event: response.function_call_arguments.done\n" +
		"data: {\"type\":\"response.function_call_arguments.done\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\",\"item_id\":\"fc_1\",\"output_index\":0}\n\n" +
		"event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"fc_1\",\"type\":\"function_call\",\"status\":\"completed\",\"arguments\":\"{\\\"path\\\":\\\"README.md\\\"}\",\"call_id\":\"call_1\",\"name\":\"read_file\"},\"output_index\":0}\n\n" +
		"event: response.done\n" +
		"data: {\"type\":\"response.done\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		Tools: []providers.ToolDefinition{
			{Name: "read_file", Description: "read file", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var events []providers.StreamEvent
	for ev := range ch {
		events = append(events, ev)
	}

	var content string
	var contentPhase providers.MessagePhase
	var contentProviderItemID string
	var toolStarts, toolEnds int
	var endToolCall *providers.ToolCall
	var done *providers.StreamEvent
	for i := range events {
		ev := events[i]
		switch ev.Type {
		case providers.EventContentDelta:
			content += ev.Content
			if ev.Content != "" {
				contentPhase = ev.Phase
			}
			if ev.ProviderItemID != "" {
				contentProviderItemID = ev.ProviderItemID
			}
		case providers.EventToolUseStart:
			toolStarts++
			if ev.ToolCall == nil || ev.ToolCall.ID != "call_1" || ev.ToolCall.ProviderItemID != "fc_1" || ev.ToolCall.Name != "read_file" {
				t.Fatalf("unexpected tool start: %+v", ev.ToolCall)
			}
		case providers.EventToolUseEnd:
			toolEnds++
			endToolCall = ev.ToolCall
		case providers.EventDone:
			done = &events[i]
		}
	}
	if content != "Hello" {
		t.Fatalf("unexpected content: %q", content)
	}
	if contentPhase != providers.MessagePhaseFinalAnswer {
		t.Fatalf("unexpected content phase: %q", contentPhase)
	}
	if contentProviderItemID != "msg_1" {
		t.Fatalf("unexpected content provider item id: %q", contentProviderItemID)
	}
	if toolStarts != 1 || toolEnds != 1 {
		t.Fatalf("expected one tool start/end, got starts=%d ends=%d events=%+v", toolStarts, toolEnds, events)
	}
	if endToolCall == nil || endToolCall.ProviderItemID != "fc_1" || endToolCall.Arguments != `{"path":"README.md"}` {
		t.Fatalf("unexpected tool end: %+v", endToolCall)
	}
	if done == nil || done.StopReason != "tool_calls" {
		t.Fatalf("unexpected done event: %+v", done)
	}
	if done.Usage == nil || done.Usage.InputTokens != 5 || done.Usage.OutputTokens != 2 {
		t.Fatalf("unexpected usage: %+v", done.Usage)
	}
}

func TestResponsesStreamChat_SSEBoundsTailAfterCompletedFinalAnswer(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = io.WriteString(w,
			"event: response.output_item.added\n"+
				"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"status\":\"in_progress\"},\"output_index\":0}\n\n"+
				"event: response.output_text.delta\n"+
				"data: {\"type\":\"response.output_text.delta\",\"delta\":\"done\",\"item_id\":\"msg_1\",\"output_index\":0}\n\n"+
				"event: response.output_item.done\n"+
				"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"msg_1\",\"type\":\"message\",\"role\":\"assistant\",\"phase\":\"final_answer\",\"status\":\"completed\",\"content\":[{\"type\":\"output_text\",\"text\":\"done\"}]},\"output_index\":0}\n\n")
		w.(http.Flusher).Flush()
		// Reproduce a provider that completed the final output item but leaves the
		// response open without response.completed.
		<-r.Context().Done()
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL:            server.URL,
		WireAPI:            "responses",
		APIKey:             "test-key",
		ResponsesTransport: providers.StreamTransportSSE,
		StreamConfig:       &providers.StreamTransportConfig{IdleTimeout: 100 * time.Millisecond},
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 2*time.Second)
	defer cancel()
	ch, err := client.StreamChat(ctx, providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}
	started := time.Now()
	var events []providers.StreamEvent
	for event := range ch {
		events = append(events, event)
	}
	if elapsed := time.Since(started); elapsed >= time.Second {
		t.Fatalf("final-answer tail took %s, want less than 1s", elapsed)
	}
	if len(events) == 0 {
		t.Fatal("no events received")
	}
	final := events[len(events)-1]
	if final.Type != providers.EventDone || final.FinishReason != providers.FinishReasonStop || final.StopReason != "completed" || final.ProviderEventType != "response.output_item.done" {
		t.Fatalf("final event = %+v, want inferred completed EventDone", final)
	}
}

func TestResponsesFinalAnswerItemDone(t *testing.T) {
	completedFinalAnswer := responsesStreamEvent{
		Type: "response.output_item.done",
		Item: responsesOutputItem{Type: "message", Phase: "final_answer", Status: "completed"},
	}
	tests := []struct {
		name        string
		event       responsesStreamEvent
		sawToolCall bool
		want        bool
	}{
		{name: "completed final answer", event: completedFinalAnswer, want: true},
		{name: "tool call observed", event: completedFinalAnswer, sawToolCall: true},
		{name: "commentary", event: responsesStreamEvent{Type: "response.output_item.done", Item: responsesOutputItem{Type: "message", Phase: "commentary", Status: "completed"}}},
		{name: "incomplete item", event: responsesStreamEvent{Type: "response.output_item.done", Item: responsesOutputItem{Type: "message", Phase: "final_answer", Status: "in_progress"}}},
		{name: "tool item", event: responsesStreamEvent{Type: "response.output_item.done", Item: responsesOutputItem{Type: "function_call", Phase: "final_answer", Status: "completed"}}},
		{name: "item not done", event: responsesStreamEvent{Type: "response.output_item.added", Item: responsesOutputItem{Type: "message", Phase: "final_answer", Status: "completed"}}},
	}
	for _, tt := range tests {
		t.Run(tt.name, func(t *testing.T) {
			if got := responsesFinalAnswerItemDone(tt.event, tt.sawToolCall); got != tt.want {
				t.Fatalf("responsesFinalAnswerItemDone() = %v, want %v", got, tt.want)
			}
		})
	}
}

func TestResponsesStreamChat_ParsesReasoningItem(t *testing.T) {
	ssePayload := "event: response.output_item.added\n" +
		"data: {\"type\":\"response.output_item.added\",\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"status\":\"in_progress\"},\"output_index\":0}\n\n" +
		"event: response.reasoning_summary_text.delta\n" +
		"data: {\"type\":\"response.reasoning_summary_text.delta\",\"delta\":\"inspect first\",\"output_index\":0}\n\n" +
		"event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"id\":\"rs_1\",\"type\":\"reasoning\",\"status\":\"completed\",\"summary\":[{\"type\":\"summary_text\",\"text\":\"inspect first\"}],\"encrypted_content\":\"enc_123\"},\"output_index\":0}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
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

	var thinking string
	var block *providers.ReasoningBlock
	for ev := range ch {
		switch ev.Type {
		case providers.EventThinkingDelta:
			thinking += ev.Content
		case providers.EventThinkingDone:
			block = ev.ReasoningBlock
		}
	}
	if thinking != "inspect first" {
		t.Fatalf("thinking delta = %q", thinking)
	}
	if block == nil || block.Type != "reasoning" || block.Thinking != "inspect first" || !strings.Contains(block.Data, `"encrypted_content":"enc_123"`) {
		t.Fatalf("unexpected reasoning block: %+v", block)
	}
}

func TestResponsesStreamChat_ParsesToolSearchCall(t *testing.T) {
	ssePayload := "event: response.output_item.done\n" +
		"data: {\"type\":\"response.output_item.done\",\"item\":{\"type\":\"tool_search_call\",\"call_id\":\"search_1\",\"execution\":\"client\",\"status\":\"completed\",\"arguments\":{\"query\":\"docs search\",\"limit\":1}},\"output_index\":0}\n\n" +
		"event: response.completed\n" +
		"data: {\"type\":\"response.completed\",\"response\":{\"status\":\"completed\",\"output\":[],\"usage\":{\"input_tokens\":5,\"output_tokens\":2}}}\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	ch, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model:    "gpt-test",
		Messages: []providers.ChatMessage{{Role: "user", Content: "hello"}},
		Tools: []providers.ToolDefinition{
			{Name: "tool_search", Description: "Search deferred tools", InputSchema: map[string]any{"type": "object"}},
		},
	})
	if err != nil {
		t.Fatalf("StreamChat: %v", err)
	}

	var starts, ends int
	var endToolCall *providers.ToolCall
	var done *providers.StreamEvent
	for ev := range ch {
		switch ev.Type {
		case providers.EventToolUseStart:
			starts++
			if ev.ToolCall == nil || ev.ToolCall.ID != "search_1" || ev.ToolCall.Name != "tool_search" || ev.ToolCall.Kind != providers.ToolCallKindToolSearch {
				t.Fatalf("unexpected tool start: %+v", ev.ToolCall)
			}
		case providers.EventToolUseEnd:
			ends++
			endToolCall = ev.ToolCall
		case providers.EventDone:
			done = &ev
		}
	}
	if starts != 1 || ends != 1 {
		t.Fatalf("expected one tool start/end, got starts=%d ends=%d", starts, ends)
	}
	if endToolCall == nil || endToolCall.Kind != providers.ToolCallKindToolSearch || endToolCall.Arguments != `{"query":"docs search","limit":1}` {
		t.Fatalf("unexpected tool end: %+v", endToolCall)
	}
	if done == nil || done.StopReason != "tool_calls" {
		t.Fatalf("unexpected done event: %+v", done)
	}
}

func TestResponsesStreamChat_ContextLengthExceededClassified(t *testing.T) {
	rawError := `{"type":"response.failed","response":{"status":"failed","error":{"code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again."}}}`
	ssePayload := "event: response.failed\n" +
		"data: " + rawError + "\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
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

	var got error
	for ev := range ch {
		if ev.Type == providers.EventError {
			got = ev.Error
			break
		}
	}
	if !providers.IsContextOverflow(got) {
		t.Fatalf("expected context overflow stream error, got %T (%v)", got, got)
	}
}

func TestResponsesStreamChat_TopLevelContextLengthErrorClassified(t *testing.T) {
	rawError := `{"type":"error","error":{"type":"invalid_request_error","code":"context_length_exceeded","message":"Your input exceeds the context window of this model. Please adjust your input and try again.","param":"input"},"sequence_number":2}`
	ssePayload := "event: error\n" +
		"data: " + rawError + "\n\n"

	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/responses" {
			t.Fatalf("unexpected path: %s", r.URL.Path)
		}
		w.Header().Set("Content-Type", "text/event-stream")
		w.WriteHeader(http.StatusOK)
		_, _ = w.Write([]byte(ssePayload))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "test-key"})
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

	var got error
	for ev := range ch {
		if ev.Type == providers.EventError {
			got = ev.Error
			break
		}
	}
	if !providers.IsContextOverflow(got) {
		t.Fatalf("expected top-level context overflow stream error, got %T (%v)", got, got)
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

func TestClampPromptCacheKey(t *testing.T) {
	// Empty / whitespace-only inputs collapse to "".
	if got := clampPromptCacheKey(""); got != "" {
		t.Fatalf("empty = %q", got)
	}
	if got := clampPromptCacheKey("   \t\n"); got != "" {
		t.Fatalf("whitespace-only = %q", got)
	}
	if got := clampPromptCacheKey("  abc  "); got != "abc" {
		t.Fatalf("whitespace-trimmed = %q", got)
	}

	// Under the cap: pass through unchanged.
	short := "thread-abc-123"
	if got := clampPromptCacheKey(short); got != short {
		t.Fatalf("under-cap = %q, want %q", got, short)
	}

	// Exactly at the cap: pass through unchanged.
	atLimit := strings.Repeat("a", openAIPromptCacheKeyMaxLength)
	if got := clampPromptCacheKey(atLimit); got != atLimit {
		t.Fatalf("at-cap = %q", got)
	}

	// Over the cap: truncated to exactly the cap.
	over := strings.Repeat("a", openAIPromptCacheKeyMaxLength+10)
	wantOver := strings.Repeat("a", openAIPromptCacheKeyMaxLength)
	if got := clampPromptCacheKey(over); got != wantOver {
		t.Fatalf("over-cap = %q (len %d), want len %d", got, len(got), openAIPromptCacheKeyMaxLength)
	}

	// Multi-byte: clamp by code point, never split a rune.
	chinese := strings.Repeat("中", openAIPromptCacheKeyMaxLength+6)
	got := clampPromptCacheKey(chinese)
	if n := len([]rune(got)); n != openAIPromptCacheKeyMaxLength {
		t.Fatalf("chinese clamp code points = %d, want %d", n, openAIPromptCacheKeyMaxLength)
	}
}

func TestChat_ResponsesSendsSessionIDAndRequestIDFromCacheHint(t *testing.T) {
	// The Responses wire path must mirror the prompt_cache_key value into
	// session-id and x-client-request-id so the ChatGPT Codex backend can
	// keep sticky routing across the tool loop. OpenAI ignores the headers
	// on the public Responses API.
	t.Helper()

	var seenSession, seenRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSession = r.Header.Get("session-id")
		seenRequest = r.Header.Get("x-client-request-id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: "responses"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
		CacheHint: &providers.CacheHint{PromptCacheKey: "thread-abc-123"},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if seenSession != "thread-abc-123" {
		t.Fatalf("session-id = %q, want thread-abc-123", seenSession)
	}
	if seenRequest != "thread-abc-123" {
		t.Fatalf("x-client-request-id = %q, want thread-abc-123", seenRequest)
	}
}

func TestChat_ResponsesOmitsSessionIDWithoutCacheHint(t *testing.T) {
	// Without a CacheHint (or with an empty PromptCacheKey) the headers must
	// not be sent at all: otherwise OpenAI would log unexplained session-id
	// values, and codex-style backends could incorrectly chain unrelated
	// requests.
	t.Helper()

	var seenSession, seenRequest string
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		seenSession = r.Header.Get("session-id")
		seenRequest = r.Header.Get("x-client-request-id")
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"status":"completed","output":[{"type":"message","role":"assistant","content":[{"type":"output_text","text":"ok"}]}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: "responses"})
	if err != nil {
		t.Fatalf("New: %v", err)
	}

	if _, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "gpt-test",
		Messages: []providers.ChatMessage{
			{Role: "user", Content: "hello"},
		},
	}); err != nil {
		t.Fatalf("Chat: %v", err)
	}
	if seenSession != "" {
		t.Fatalf("session-id = %q, want empty", seenSession)
	}
	if seenRequest != "" {
		t.Fatalf("x-client-request-id = %q, want empty", seenRequest)
	}
}

func TestGPT6SolLunaCatalogResponsesToolRequest(t *testing.T) {
	for _, model := range []string{"gpt-6-sol", "gpt-6-luna", "gpt-6-sol-fast", "gpt-6-luna-fast"} {
		for _, effort := range []string{"none", "max"} {
			t.Run(model+"/"+effort, func(t *testing.T) {
				name, provider := modelcatalog.EnrichProvider("openai", config.ProviderConfig{Type: "openai", Model: model}, model)
				selection := modelvariant.ResolveForProvider(name, provider, model, effort, "")
				server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
					if r.URL.Path != "/responses" {
						t.Errorf("wrong endpoint: %s", r.URL.Path)
					}
					var body map[string]any
					if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
						t.Error(err)
						w.WriteHeader(400)
						return
					}
					if body["model"] != strings.TrimSuffix(model, "-fast") {
						t.Errorf("model = %v", body["model"])
					}
					reasoning, ok := body["reasoning"].(map[string]any)
					if !ok || reasoning["effort"] != effort {
						t.Errorf("reasoning = %#v", body["reasoning"])
					}
					if strings.HasSuffix(model, "-fast") && body["service_tier"] != "priority" {
						t.Errorf("tier = %v", body["service_tier"])
					}
					if _, ok := body["temperature"]; ok {
						t.Error("unexpected temperature")
					}
					if tools, ok := body["tools"].([]any); !ok || len(tools) != 1 {
						t.Error("missing tools")
					}
					w.Header().Set("Content-Type", "application/json")
					_, _ = w.Write([]byte(`{"id":"resp_1","status":"completed","output":[{"type":"function_call","call_id":"call_1","name":"read_file","arguments":"{}"}]}`))
				}))
				defer server.Close()
				client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test-key", WireAPI: provider.WireAPI})
				if err != nil {
					t.Fatal(err)
				}
				resp, err := client.Chat(context.Background(), providers.ChatRequest{
					Model: modelcatalog.APIModel(provider, model), ProviderOptions: selection.ProviderOptions, Temperature: 0.7,
					Messages: []providers.ChatMessage{{Role: "user", Content: "Read the file"}},
					Tools:    []providers.ToolDefinition{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}},
				})
				if err != nil || len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
					t.Fatalf("Chat = %+v, %v", resp, err)
				}
			})
		}
	}
}

func TestResponsesChatGeneratedImagesAndReplay(t *testing.T) {
	for _, format := range []string{"png", "jpeg", "webp"} {
		t.Run(format, func(t *testing.T) {
			var requests []map[string]any
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request map[string]any
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					t.Error(err)
					return
				}
				requests = append(requests, request)
				w.Header().Set("Content-Type", "application/json")
				fmt.Fprintf(w, `{"status":"completed","output":[{"id":"ig_1","type":"image_generation_call","status":"completed","output_format":%q,"result":"aW1hZ2U="}]}`, format)
			}))
			defer server.Close()
			client, err := New(ClientConfig{BaseURL: server.URL, APIKey: "test", WireAPI: "responses"})
			if err != nil {
				t.Fatal(err)
			}
			response, err := client.Chat(context.Background(), providers.ChatRequest{Model: "gpt-test", Messages: []providers.ChatMessage{{Role: "user", Content: "draw"}}})
			if err != nil {
				t.Fatal(err)
			}
			if len(response.Images) != 1 || response.Images[0].MediaType != "image/"+format || response.Images[0].Data != "aW1hZ2U=" {
				t.Fatalf("missing image: %+v", response)
			}
			_, err = client.Chat(context.Background(), providers.ChatRequest{Model: "gpt-test", Messages: []providers.ChatMessage{{Role: "assistant", Images: response.Images}, {Role: "user", Content: "describe"}}})
			if err != nil {
				t.Fatal(err)
			}
			input := requests[1]["input"].([]any)
			prior := input[0].(map[string]any)
			if prior["type"] != "image_generation_call" || prior["id"] != "ig_1" || prior["result"] != "aW1hZ2U=" {
				t.Fatalf("image missing from follow-up: %+v", input)
			}

		})
	}
}
