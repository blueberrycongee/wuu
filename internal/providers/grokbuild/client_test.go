package grokbuild

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestChatUsesGrokBuildHeadersAndChatCompletions(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" {
			t.Fatalf("path = %q", r.URL.Path)
		}
		if got := r.Header.Get("Authorization"); got != "Bearer session-token" {
			t.Fatalf("Authorization = %q", got)
		}
		if got := r.Header.Get("X-XAI-Token-Auth"); got != "xai-grok-cli" {
			t.Fatalf("X-XAI-Token-Auth = %q", got)
		}
		if got := r.Header.Get("x-grok-model-override"); got != "grok-4.6" {
			t.Fatalf("x-grok-model-override = %q", got)
		}
		for header, want := range map[string]string{
			"x-grok-client-version":    "1.0.13",
			"x-grok-client-identifier": "wuu",
			"x-grok-client-mode":       "interactive",
			"x-authenticateresponse":   "authenticate-response",
		} {
			if got := r.Header.Get(header); got != want {
				t.Fatalf("%s = %q, want %q", header, got, want)
			}
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["model"] != "grok-4.6" || body["reasoning_effort"] != "xhigh" {
			t.Fatalf("body = %#v", body)
		}
		tools, _ := body["tools"].([]any)
		if len(tools) != 1 {
			t.Fatalf("tools = %#v", body["tools"])
		}
		w.Header().Set("Content-Type", "application/json")
		_, _ = w.Write([]byte(`{"choices":[{"message":{"content":"","tool_calls":[{"id":"call_1","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"README.md\"}"}}]}}]}`))
	}))
	defer server.Close()

	client, err := New(ClientConfig{
		BaseURL: server.URL,
		APIKey:  "session-token",
		Headers: map[string]string{
			"authorization":         "Bearer wrong",
			"x-xai-token-auth":      "wrong",
			"X-Grok-Model-Override": "wrong",
			"X-Grok-Client-Version": "0.0.0",
			"X-Grok-Client-Mode":    "wrong",
		},
	})
	if err != nil {
		t.Fatal(err)
	}
	resp, err := client.Chat(context.Background(), providers.ChatRequest{
		Model:           "grok-4.6",
		Messages:        []providers.ChatMessage{{Role: "user", Content: "read"}},
		Tools:           []providers.ToolDefinition{{Name: "read_file", InputSchema: map[string]any{"type": "object"}}},
		ProviderOptions: map[string]any{"reasoningEffort": "xhigh"},
	})
	if err != nil {
		t.Fatal(err)
	}
	if len(resp.ToolCalls) != 1 || resp.ToolCalls[0].Name != "read_file" {
		t.Fatalf("tool calls = %#v", resp.ToolCalls)
	}
}

func TestChatExplainsRejectedGrokLogin(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		http.Error(w, "expired", http.StatusUnauthorized)
	}))
	defer server.Close()
	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "expired"})
	_, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "grok-4.5", Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil || !strings.Contains(err.Error(), "grok login") {
		t.Fatalf("err = %v", err)
	}
	if providers.IsRetryable(err) {
		t.Fatal("diagnostic 401 must not be retryable")
	}
}

func TestChatGenericProxyAuthIsRetryable(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		w.Header().Set("Content-Type", "application/json")
		w.WriteHeader(http.StatusUnauthorized)
		_, _ = w.Write([]byte(`{"error":"Authentication required"}`))
	}))
	defer server.Close()
	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "session-token"})
	_, err := client.Chat(context.Background(), providers.ChatRequest{
		Model: "grok-4.6", Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err == nil {
		t.Fatal("expected error")
	}
	if strings.Contains(err.Error(), "grok login") {
		t.Fatalf("err = %q, must not ask to grok login", err)
	}
	plan := providers.PlanRecovery(providers.NormalizeFailure(err))
	if plan.Action != providers.RecoveryReplaySame {
		t.Fatalf("plan = %+v, want replay_same_payload", plan)
	}
	if !providers.IsRetryable(err) {
		t.Fatal("generic proxy 401 must be retryable")
	}
}

func TestStreamChatUsesGrokBuildRoute(t *testing.T) {
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		if r.URL.Path != "/chat/completions" || r.Header.Get("x-grok-model-override") != "grok-4.5" {
			t.Fatalf("request = %s, override = %q", r.URL.Path, r.Header.Get("x-grok-model-override"))
		}
		var body map[string]any
		if err := json.NewDecoder(r.Body).Decode(&body); err != nil {
			t.Fatal(err)
		}
		if body["stream"] != true {
			t.Fatalf("stream = %#v", body["stream"])
		}
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"ok\"}}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	client, _ := New(ClientConfig{BaseURL: server.URL, APIKey: "session-token"})
	events, err := client.StreamChat(context.Background(), providers.ChatRequest{
		Model: "grok-4.5", Messages: []providers.ChatMessage{{Role: "user", Content: "hi"}},
	})
	if err != nil {
		t.Fatal(err)
	}
	var content string
	for event := range events {
		if event.Error != nil {
			t.Fatal(event.Error)
		}
		if event.Type == providers.EventContentDelta {
			content += event.Content
		}
	}
	if content != "ok" {
		t.Fatalf("content = %q", content)
	}
}
