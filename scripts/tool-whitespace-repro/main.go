// Command tool-whitespace-repro exercises the real Responses adapter and stream
// guard with deterministic wire frames. It never executes generated tool calls.
package main

import (
	"context"
	"encoding/json"
	"flag"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/providers/openai"
	"github.com/coder/websocket"
)

type fixture struct {
	name, kind, initial string
	deltas              []string
	wantBlocked         bool
}

func main() {
	logDir := flag.String("log-dir", "", "directory for metadata diagnostics")
	flag.Parse()
	if *logDir != "" {
		providers.InitDebugLog(*logDir)
	}
	spaces := strings.Repeat(" ", 17*1024)
	fixtures := []fixture{
		{"function-formatting-runaway", "function_call", "", []string{"{", spaces, "}"}, true},
		{"function-string", "function_call", "", []string{`{"input":"`, spaces, `"}`}, false},
		{"function-initial-string-prefix", "function_call", `{"input":"`, []string{spaces, `"}`}, false},
		{"custom-string", "custom_tool_call", "", []string{spaces}, false},
		{"custom-initial-prefix", "custom_tool_call", "// prefix", []string{spaces}, false},
		{"custom-empty-first-delta", "custom_tool_call", "", []string{"", spaces}, false},
		{"custom-escaped-source", "custom_tool_call", "", []string{"\"\\\n", spaces, "\""}, false},
	}
	failed := false
	for _, transport := range []providers.StreamTransportMode{providers.StreamTransportSSE, providers.StreamTransportWebSocket} {
		for _, f := range fixtures {
			if !run(f, transport) {
				failed = true
			}
		}
	}
	if failed {
		os.Exit(1)
	}
}

func run(f fixture, transport providers.StreamTransportMode) bool {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var conn *websocket.Conn
		if transport == providers.StreamTransportWebSocket {
			var err error
			conn, err = websocket.Accept(w, r, nil)
			if err != nil {
				return
			}
			defer conn.CloseNow()
			if _, _, err = conn.Read(r.Context()); err != nil {
				return
			}
		} else {
			w.Header().Set("Content-Type", "text/event-stream")
		}
		write := func(event any) {
			data, _ := json.Marshal(event)
			if conn != nil {
				_ = conn.Write(ctx, websocket.MessageText, data)
			} else {
				fmt.Fprintf(w, "data: %s\n\n", data)
			}
		}
		field, deltaType := "arguments", "response.function_call_arguments.delta"
		if f.kind == "custom_tool_call" {
			field, deltaType = "input", "response.custom_tool_call_input.delta"
		}
		item := map[string]any{"type": f.kind, "id": "item_fixture", "call_id": "call_fixture", "name": "probe", field: f.initial}
		write(map[string]any{"type": "response.output_item.added", "output_index": 0, "item": item})
		for _, delta := range f.deltas {
			write(map[string]any{"type": deltaType, "item_id": "item_fixture", "delta": delta})
		}
		item[field] = f.initial + strings.Join(f.deltas, "")
		write(map[string]any{"type": "response.output_item.done", "output_index": 0, "item": item})
		write(map[string]any{"type": "response.completed", "response": map[string]any{"id": "resp_fixture", "status": "completed", "output": []any{item}}})
	}))
	defer server.Close()
	client, err := openai.New(openai.ClientConfig{BaseURL: server.URL, WireAPI: "responses", APIKey: "fixture", ResponsesTransport: transport})
	if err != nil {
		panic(err)
	}
	guarded := providers.NewReliableStreamClient(client, nil)
	req := providers.ChatRequest{Model: "fixture", CacheHint: &providers.CacheHint{PromptCacheKey: f.name}, Messages: []providers.ChatMessage{{Role: "user", Content: "probe"}}, Tools: []providers.ToolDefinition{{Name: "probe", Freeform: f.kind == "custom_tool_call", InputSchema: map[string]any{"type": "object"}}}}
	ch, err := guarded.StreamChat(ctx, req)
	if err != nil {
		panic(err)
	}
	var streamError string
	var finalArgs string
	starts, deltas, initialBytes := 0, 0, 0
	for event := range ch {
		if event.Error != nil {
			streamError = event.Error.Error()
		}
		if event.Type == providers.EventToolUseStart {
			starts++
			initialBytes += len(event.ToolCall.Arguments)
		}
		if event.Type == providers.EventToolUseDelta {
			deltas++
		}
		if event.Type == providers.EventToolUseEnd {
			finalArgs = event.ToolCall.Arguments
		}
	}
	blocked := strings.Contains(streamError, "consecutive JSON whitespace")
	expectedArgs := f.initial + strings.Join(f.deltas, "")
	if f.kind == "custom_tool_call" {
		encoded, _ := json.Marshal(map[string]string{"input": expectedArgs})
		expectedArgs = string(encoded)
	}
	passed := blocked == f.wantBlocked && (blocked || (streamError == "" && json.Valid([]byte(finalArgs)) && finalArgs == expectedArgs))
	_ = json.NewEncoder(os.Stdout).Encode(map[string]any{"case": f.name, "transport": transport, "passed": passed, "expected_blocked": f.wantBlocked, "blocked": blocked, "error": streamError, "wire_initial_bytes": len(f.initial), "normalized_initial_bytes": initialBytes, "starts": starts, "forwarded_deltas": deltas, "final_bytes": len(finalArgs)})
	return passed
}
