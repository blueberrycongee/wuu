// Command fakecodex is a minimal codex app-server stub speaking the same
// JSON-RPC/NDJSON protocol over stdio. It exists only for codexengine tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
)

type request struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
}

func send(msg map[string]any) {
	line, _ := json.Marshal(msg)
	fmt.Fprintln(os.Stdout, string(line))
}

func respond(id json.RawMessage, result any) {
	send(map[string]any{"id": id, "result": result})
}

func respondError(id json.RawMessage, code int, message string) {
	send(map[string]any{"id": id, "error": map[string]any{"code": code, "message": message}})
}

func notify(method string, params any) {
	send(map[string]any{"method": method, "params": params})
}

func main() {
	scanner := bufio.NewScanner(os.Stdin)
	scanner.Buffer(make([]byte, 1024*1024), 16*1024*1024)
	for scanner.Scan() {
		line := strings.TrimSpace(scanner.Text())
		if line == "" {
			continue
		}
		var req request
		if err := json.Unmarshal([]byte(line), &req); err != nil {
			continue
		}
		switch req.Method {
		case "initialize":
			respond(req.ID, map[string]any{
				"userAgent":      "codex-cli/0.100.0 (test)",
				"codexHome":      "/tmp/fake-codex-home",
				"platformFamily": "test",
				"platformOs":     "test",
			})
		case "thread/start":
			respond(req.ID, map[string]any{
				"thread": map[string]any{"id": "codex-thread-1", "status": map[string]any{"type": "idle"}},
				"model":  "gpt-5",
				"cwd":    ".",
			})
			notify("thread/started", map[string]any{
				"thread": map[string]any{"id": "codex-thread-1"},
			})
		case "thread/resume":
			respond(req.ID, map[string]any{
				"thread": map[string]any{"id": "codex-thread-1"},
				"model":  "gpt-5",
				"cwd":    ".",
			})
		case "turn/start":
			if path := os.Getenv("WUU_TEST_CODEX_INPUT"); path != "" {
				if err := os.WriteFile(path, req.Params, 0600); err != nil {
					panic(err)
				}
			}
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(req.Params, &params)
			respond(req.ID, map[string]any{
				"turn": map[string]any{"id": "turn-1"},
			})
			notify("turn/started", map[string]any{
				"threadId": params.ThreadID,
				"turn":     map[string]any{"id": "turn-1"},
			})
			notify("item/started", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   "turn-1",
				"item":     map[string]any{"id": "item-1", "type": "agentMessage"},
			})
			notify("item/agentMessage/delta", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   "turn-1",
				"itemId":   "item-1",
				"delta":    "Hello from ",
			})
			notify("item/reasoning/textDelta", map[string]any{
				"threadId":     params.ThreadID,
				"turnId":       "turn-1",
				"itemId":       "item-2",
				"delta":        "thinking...",
				"contentIndex": 0,
			})
			notify("item/agentMessage/delta", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   "turn-1",
				"itemId":   "item-1",
				"delta":    "codex.",
			})
			notify("thread/tokenUsage/updated", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   "turn-1",
				"tokenUsage": map[string]any{
					"total": map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 165},
					"last":  map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 165},
				},
			})
			notify("item/completed", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   "turn-1",
				"item":     map[string]any{"id": "item-1", "type": "agentMessage", "text": "Hello from codex.", "phase": "final_answer"},
			})
			notify("turn/completed", map[string]any{
				"threadId": params.ThreadID,
				"turn":     map[string]any{"id": "turn-1", "status": "completed"},
			})
		case "turn/interrupt":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(req.Params, &params)
			respond(req.ID, map[string]any{})
			notify("turn/completed", map[string]any{
				"threadId": params.ThreadID,
				"turn":     map[string]any{"id": "turn-1", "status": "interrupted"},
			})
		case "thread/unsubscribe":
			respond(req.ID, map[string]any{})
		default:
			respondError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}
