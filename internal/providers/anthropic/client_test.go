package anthropic

import (
	"context"
	"encoding/json"
	"net/http"
	"net/http/httptest"
	"testing"

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
