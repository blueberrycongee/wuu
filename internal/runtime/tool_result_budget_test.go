package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
	"net/http"
	"net/http/httptest"
	"path/filepath"
	"reflect"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

type budgetToolClient struct {
	mu    sync.Mutex
	texts []string
	calls []string
}

func (*budgetToolClient) ID() string { return "budget-test" }
func (c *budgetToolClient) Status() pluginhost.Status {
	return pluginhost.Status{ID: c.ID(), State: pluginhost.StateActive}
}
func (*budgetToolClient) Close(context.Context) error { return nil }
func (*budgetToolClient) Tools() []pluginhost.ToolRegistration {
	return []pluginhost.ToolRegistration{{ID: "records", Description: "Synthetic budget records",
		InputSchema: map[string]any{"type": "object", "properties": map[string]any{"index": map[string]any{"type": "integer"}}},
		Activity:    &pluginhost.ToolActivityMetadata{ReadOnly: true, ConcurrencySafe: false, Risk: "low"},
	}}
}
func (c *budgetToolClient) ExecuteTool(_ context.Context, input pluginhost.ToolExecuteParams) (pluginhost.ToolExecuteResult, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	var args struct {
		Index int `json:"index"`
	}
	if err := json.Unmarshal(input.Arguments, &args); err != nil {
		return pluginhost.ToolExecuteResult{}, err
	}
	if args.Index < 0 || args.Index >= len(c.texts) || input.ExecutionID == "" || input.TurnID == "" {
		return pluginhost.ToolExecuteResult{}, fmt.Errorf("invalid scoped tool request")
	}
	c.calls = append(c.calls, input.CallID)
	return pluginhost.ToolExecuteResult{Result: toolresult.FromText(c.texts[args.Index])}, nil
}

func TestNativeToolResultBudgetHTTP(t *testing.T) {
	for _, tc := range []struct {
		name  string
		sizes []int
	}{
		{"at_limit", []int{200000}},
		{"one_over", []int{200001}},
		{"asymmetric", []int{170000, 50001}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			home, root := t.TempDir(), t.TempDir()
			t.Setenv("HOME", home)
			t.Setenv("WUU_HOME", filepath.Join(home, "state"))
			t.Setenv("WUU_CODE_MODE_HOST", "")
			plugin := &budgetToolClient{}
			var wantIDs []string
			for i, size := range tc.sizes {
				plugin.texts = append(plugin.texts, strings.Repeat(string(rune('a'+i)), size-4)+"TAIL")
				wantIDs = append(wantIDs, fmt.Sprintf("budget_%d", i))
			}
			requests := make(chan []byte, 8)
			var toolName string
			server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					t.Error(err)
					http.Error(w, "read error", 400)
					return
				}
				select {
				case requests <- body:
				default:
					t.Error("unexpected extra request")
					http.Error(w, "extra request", 400)
					return
				}
				if r.URL.Path != "/chat/completions" {
					t.Errorf("route = %s", r.URL.Path)
				}
				var req struct {
					Messages []struct {
						Role string `json:"role"`
					} `json:"messages"`
				}
				if err := json.Unmarshal(body, &req); err != nil {
					t.Error(err)
				}
				hasResult := false
				for _, msg := range req.Messages {
					hasResult = hasResult || msg.Role == "tool"
				}
				delta := map[string]any{"content": "done"}
				finish := "stop"
				if !hasResult {
					var calls []any
					for i := range tc.sizes {
						calls = append(calls, map[string]any{"index": i, "id": wantIDs[i], "type": "function", "function": map[string]any{"name": toolName, "arguments": fmt.Sprintf(`{"index":%d}`, i)}})
					}
					delta = map[string]any{"tool_calls": calls}
					finish = "tool_calls"
				}
				// Omit usage so the normal runtime estimator remains active.
				frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": delta, "finish_reason": finish}}})
				w.Header().Set("Content-Type", "text/event-stream")
				fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
			}))
			defer server.Close()
			rt, err := NewSession(Options{RootDir: root, HomeDir: home, SafeMode: true, Config: config.Config{
				DefaultProvider: "synthetic", Providers: map[string]config.ProviderConfig{
					"synthetic": {Type: "openai-compatible", WireAPI: "chat", Model: "gpt-4o", BaseURL: server.URL, APIKey: "synthetic-key", AuthToken: "synthetic-token"},
				},
			}})
			if err != nil {
				t.Fatal(err)
			}
			defer func() {
				if _, err := rt.Cleanup(); err != nil {
					t.Error(err)
				}
			}()
			// Keep NewSession's real Host, execution tracking and materializer.
			rt.PluginHost.Add(plugin)
			for _, def := range rt.PluginHost.ToolDefinitions() {
				if def.Description == "Synthetic budget records" {
					toolName = def.Name
				}
			}
			if toolName == "" {
				t.Fatal("tool was not registered")
			}
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			engine, err := rt.WuuEngine().Open(ctx, agentengine.OpenRequest{ThreadID: "budget-thread", RootDir: root})
			if err != nil {
				t.Fatal(err)
			}
			defer engine.Close(context.Background())
			runner := engine.(*wuuEngineSession).runner
			if runner.StreamingToolExecution || runner.DisableAutoCompact {
				t.Fatal("test requires default completed-batch path and compaction")
			}
			ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "budget-thread", ExecutionID: "budget-turn"})
			result, err := engine.RunTurn(ctx, agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "Read the synthetic records."}}}, nil)
			if err != nil {
				t.Fatal(err)
			}
			if len(requests) != 2 || result.Result.HistoryRewritten {
				t.Fatalf("requests=%d rewritten=%v", len(requests), result.Result.HistoryRewritten)
			}
			<-requests
			var wire struct {
				Messages []struct {
					Role    string          `json:"role"`
					Content json.RawMessage `json:"content"`
					CallID  string          `json:"tool_call_id"`
				} `json:"messages"`
			}
			if err := json.Unmarshal(<-requests, &wire); err != nil {
				t.Fatal(err)
			}
			var returned []providers.ChatMessage
			for _, msg := range result.Result.NewMessages {
				if msg.Role == "tool" {
					returned = append(returned, msg)
				}
			}
			var ids []string
			total := 0
			for _, msg := range wire.Messages {
				if msg.Role != "tool" {
					continue
				}
				index := len(ids)
				ids = append(ids, msg.CallID)
				var text string
				if err := json.Unmarshal(msg.Content, &text); err != nil {
					t.Fatal(err)
				}
				if index >= len(returned) {
					t.Fatal("extra wire tool result")
				}
				if returned[index].ToolResult == nil || returned[index].ToolResult.IsError {
					t.Fatal("Host did not accept the result")
				}
				if text != returned[index].Content {
					t.Errorf("result %d: HTTP=%d returned=%d", index, len(text), len(returned[index].Content))
				}
				if (tc.name == "at_limit" || index == 1) && text != plugin.texts[index] {
					t.Error("untrimmed text changed")
				}
				total += len(text)
			}
			plugin.mu.Lock()
			calls := append([]string(nil), plugin.calls...)
			plugin.mu.Unlock()
			if !reflect.DeepEqual(ids, wantIDs) || !reflect.DeepEqual(calls, wantIDs) {
				t.Fatalf("wire IDs=%v execution IDs=%v want=%v", ids, calls, wantIDs)
			}
			t.Logf("produced=%v wire_tool_bytes=%d requests=2 history_rewritten=false", tc.sizes, total)
			if total > 200000 {
				t.Errorf("HTTP tool text exceeds batch budget: %d", total)
			}
		})
	}
}
