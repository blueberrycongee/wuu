// Command fakecodex is a minimal codex app-server stub speaking the same
// JSON-RPC/NDJSON protocol over stdio. It exists only for codexengine tests.
package main

import (
	"bufio"
	"encoding/json"
	"fmt"
	"os"
	"strings"
	"time"
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

func record(method string) {
	if path := os.Getenv("WUU_TEST_CODEX_LIFECYCLE"); path != "" {
		f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
		if err != nil {
			panic(err)
		}
		defer f.Close()
		_ = json.NewEncoder(f).Encode(map[string]any{"method": method, "pid": os.Getpid()})
	}
}

func waitForGate(name string) {
	if gate := os.Getenv(name); gate != "" {
		for {
			if _, err := os.Stat(gate); err == nil {
				return
			}
			time.Sleep(5 * time.Millisecond)
		}
	}
}

func main() {
	record("started")
	// A missing/null turn effort retains the native setting. Model discovery
	// uses reasoningEffort, but turn/start accepts effort.
	effectiveEffort := os.Getenv("WUU_TEST_CODEX_DEFAULT_EFFORT")
	if effectiveEffort == "" {
		effectiveEffort = "high"
	}
	defer func() {
		record("exiting")
		waitForGate("WUU_TEST_CODEX_EXIT_GATE")
		record("exited")
	}()
	threadCount, turnCount := 0, 0
	turns := make(map[string]string)
	modes := make(map[string]string)
	workspace := newWorkspaceProbe()
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
		if path := os.Getenv("WUU_TEST_CODEX_REQUESTS"); path != "" {
			f, err := os.OpenFile(path, os.O_CREATE|os.O_APPEND|os.O_WRONLY, 0600)
			if err != nil {
				panic(err)
			}
			fmt.Fprintln(f, line)
			f.Close()
		}
		if handleHistory(req) || (workspace != nil && workspace.handle(req)) {
			continue
		}
		switch req.Method {
		case "initialize":
			record("initialize")
			waitForGate("WUU_TEST_CODEX_INITIALIZE_GATE")
			if marker := os.Getenv("WUU_TEST_CODEX_FAIL_INITIALIZE_ONCE"); marker != "" {
				if f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
					f.Close()
					respondError(req.ID, -32603, "initialization temporarily unavailable")
					continue
				}
			}
			respond(req.ID, map[string]any{
				"userAgent":      "codex-cli/0.100.0 (test)",
				"codexHome":      "/tmp/fake-codex-home",
				"platformFamily": "test",
				"platformOs":     "test",
			})
		case "model/list":
			respond(req.ID, map[string]any{"data": []any{map[string]any{"id": "gpt-6-astra", "model": "gpt-6-astra", "displayName": "GPT-6 Astra", "isDefault": true, "defaultReasoningEffort": "high", "supportedReasoningEfforts": []any{map[string]any{"reasoningEffort": "low"}, map[string]any{"reasoningEffort": "high"}}, "serviceTiers": []any{map[string]any{"id": "fast"}}, "defaultServiceTier": os.Getenv("WUU_TEST_CODEX_DEFAULT_TIER")}}})
		case "config/read":
			var effort any
			if configured := os.Getenv("WUU_TEST_CODEX_DEFAULT_EFFORT"); configured != "" {
				effort = configured
			}
			respond(req.ID, map[string]any{"config": map[string]any{"service_tier": os.Getenv("WUU_TEST_CODEX_DEFAULT_TIER"), "model_reasoning_effort": effort}})
		case "thread/start":
			threadCount++
			threadID := fmt.Sprintf("codex-thread-%d", threadCount)
			respond(req.ID, map[string]any{
				"thread": map[string]any{"id": threadID, "status": map[string]any{"type": "idle"}},
				"model":  "gpt-5",
				"cwd":    ".",
			})
			notify("thread/started", map[string]any{
				"thread": map[string]any{"id": threadID},
			})
		case "thread/resume":
			respond(req.ID, map[string]any{
				"thread": map[string]any{"id": "codex-thread-1"},
				"model":  "gpt-5",
				"cwd":    ".",
			})
		case "turn/start":
			if marker := os.Getenv("WUU_TEST_CODEX_FAIL_TURN_ONCE"); marker != "" {
				if f, err := os.OpenFile(marker, os.O_CREATE|os.O_EXCL|os.O_WRONLY, 0600); err == nil {
					f.Close()
					respondError(req.ID, -32602, "turn rejected before admission")
					continue
				}
			}
			if path := os.Getenv("WUU_TEST_CODEX_INPUT"); path != "" {
				if err := os.WriteFile(path, req.Params, 0600); err != nil {
					panic(err)
				}
			}
			var params struct {
				ThreadID string  `json:"threadId"`
				Effort   *string `json:"effort"`
				Input    []struct {
					Text string `json:"text"`
				} `json:"input"`
			}
			_ = json.Unmarshal(req.Params, &params)
			if params.Effort != nil {
				effectiveEffort = *params.Effort
			}
			text := "Hello from codex."
			if os.Getenv("WUU_TEST_CODEX_REPORT_EFFORT") == "1" {
				text = "Reasoning effort: " + effectiveEffort
			}
			turnCount++
			turnID := fmt.Sprintf("turn-%d", turnCount)
			turns[params.ThreadID] = turnID
			mode := ""
			if len(params.Input) > 0 && strings.HasPrefix(params.Input[0].Text, "e2e:") {
				mode = strings.TrimPrefix(params.Input[0].Text, "e2e:")
			}
			modes[params.ThreadID] = mode
			split := len("Hello from ")
			if os.Getenv("WUU_TEST_CODEX_REPORT_EFFORT") == "1" {
				split = len(text) / 2
			}
			if mode != "cancel-start-confirmed" && mode != "start-response-invalid" {
				respond(req.ID, map[string]any{
					"turn": map[string]any{"id": turnID},
				})
			}
			notify("turn/started", map[string]any{
				"threadId": params.ThreadID,
				"turn":     map[string]any{"id": turnID},
			})
			notify("item/started", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   turnID,
				"item":     map[string]any{"id": "item-1", "type": "agentMessage"},
			})
			notify("item/agentMessage/delta", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   turnID,
				"itemId":   "item-1",
				"delta":    text[:split],
			})
			switch mode {
			case "crash":
				os.Exit(1)
			case "invalid-json":
				fmt.Fprintln(os.Stdout, "invalid JSON")
				continue
			case "start-response-invalid":
				respond(req.ID, map[string]any{"turn": "invalid turn envelope"})
				continue
			case "error-unconfirmed":
				notify("error", map[string]any{
					"threadId": params.ThreadID, "turnId": turnID,
					"willRetry": false, "error": map[string]any{"message": "native stream failed"},
				})
				continue
			case "pending", "overflow-completed", "overflow-unconfirmed", "cancel-unconfirmed", "cancel-start-confirmed", "invalid-terminal", "invalid-terminal-status":
				continue
			}
			notify("item/reasoning/textDelta", map[string]any{
				"threadId":     params.ThreadID,
				"turnId":       turnID,
				"itemId":       "item-2",
				"delta":        "thinking...",
				"contentIndex": 0,
			})
			notify("item/agentMessage/delta", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   turnID,
				"itemId":   "item-1",
				"delta":    text[split:],
			})
			notify("thread/tokenUsage/updated", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   turnID,
				"tokenUsage": map[string]any{
					"total": map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 120},
					"last":  map[string]any{"inputTokens": 100, "cachedInputTokens": 40, "outputTokens": 20, "reasoningOutputTokens": 5, "totalTokens": 120},
				},
			})
			notify("item/completed", map[string]any{
				"threadId": params.ThreadID,
				"turnId":   turnID,
				"item":     map[string]any{"id": "item-1", "type": "agentMessage", "text": text, "phase": "final_answer"},
			})
			notify("turn/completed", map[string]any{
				"threadId": params.ThreadID,
				"turn":     map[string]any{"id": turnID, "status": "completed"},
			})
			if mode == "completed-exit" {
				return
			}
		case "turn/interrupt":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(req.Params, &params)
			respond(req.ID, map[string]any{})
			if strings.HasSuffix(modes[params.ThreadID], "-unconfirmed") {
				continue
			}
			notify("turn/completed", map[string]any{
				"threadId": params.ThreadID,
				"turn":     map[string]any{"id": turns[params.ThreadID], "status": "interrupted"},
			})
		case "test/emitInvalidTerminal":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(req.Params, &params)
			var status any = 42
			if modes[params.ThreadID] == "invalid-terminal-status" {
				status = "inProgress"
			}
			notify("turn/completed", map[string]any{
				"threadId": params.ThreadID, "turn": map[string]any{"id": turns[params.ThreadID], "status": status},
			})
			respond(req.ID, map[string]any{})
		case "test/emitBurst":
			var params struct {
				ThreadID string `json:"threadId"`
			}
			_ = json.Unmarshal(req.Params, &params)
			for i := 0; i < 1024; i++ {
				notify("item/agentMessage/delta", map[string]any{
					"threadId": params.ThreadID, "turnId": turns[params.ThreadID], "itemId": "item-1", "delta": "burst ",
				})
			}
			if modes[params.ThreadID] == "overflow-completed" {
				notify("turn/completed", map[string]any{
					"threadId": params.ThreadID, "turn": map[string]any{"id": turns[params.ThreadID], "status": "completed"},
				})
			}
			respond(req.ID, map[string]any{})
		case "thread/unsubscribe":
			respond(req.ID, map[string]any{})
		default:
			respondError(req.ID, -32601, "method not found: "+req.Method)
		}
	}
}
