package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func openCodeTestSession(t *testing.T, scenario string, binding agentengine.ThreadBinding) agentengine.Session {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine := New(enginecatalog.Entry{ID: "opencode", Name: "OpenCode", Protocol: "opencode", Args: []string{"-test.run=^TestOpenCodeHelper$", "--", "wuu-opencode-helper", scenario}}, binary, t.TempDir())
	session, err := engine.SessionForThread(context.Background(), binding)
	if err != nil {
		t.Fatal(err)
	}
	return session
}

func TestOpenCodeRejectsEffortSelection(t *testing.T) {
	binding := testBinding()
	binding.Effort = "high"
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	engine := New(enginecatalog.Entry{ID: "opencode", Name: "OpenCode", Protocol: "opencode"}, binary, t.TempDir())
	_, err = engine.SessionForThread(context.Background(), binding)
	if err == nil || !strings.Contains(err.Error(), "reasoning effort") {
		t.Fatalf("effort error = %v", err)
	}
}

func TestOpenCodeStreamingReconciliationAndResume(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	binding := testBinding()
	binding.Model, binding.Instructions = "example/model", "host instructions"
	binding.MCPServers = []agentengine.MCPServer{{Name: "host", URL: "http://127.0.0.1:1234/mcp"}}
	var ref string
	binding.PersistRef = func(value string) error { ref = value; return nil }
	for _, resume := range []bool{false, true} {
		var text strings.Builder
		var ends, approvals, done int
		binding.RequestApproval = func(_ context.Context, request agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
			approvals++
			if request.EngineID != "opencode" || request.Kind != agentengine.ApprovalCommandExecution {
				t.Errorf("request=%+v", request)
			}
			// The fixture waits on this reply before returning its final result.
			// This proves deltas, not just a final snapshot, reached the host.
			if text.String() != "Hello " {
				t.Errorf("streamed text before approval=%q", text.String())
			}
			return agentengine.ApprovalAcceptForSession, nil
		}
		if resume {
			binding.ExternalRef = ref
		}
		session := openCodeTestSession(t, "stream", binding)
		input := testInput()
		input.History[0].Images = []providers.InputImage{{MediaType: "image/png", Data: "aGVsbG8="}}
		result, err := session.RunTurn(ctx, input, func(event providers.StreamEvent) {
			switch event.Type {
			case providers.EventContentDelta:
				text.WriteString(event.Content)
			case providers.EventToolUseEnd:
				ends++
				if event.ToolResult != "file contents" {
					t.Errorf("tool output=%q", event.ToolResult)
				}
			case providers.EventDone:
				done++
			}
		})
		if err != nil {
			t.Fatal(err)
		}
		if ref != "ses_native" || result.Result.Content != "Hello world" || text.String() != "Hello world" || done != 1 || approvals != 1 || ends != 1 {
			t.Fatalf("ref=%s result=%+v text=%q done=%d approvals=%d ends=%d", ref, result, text.String(), done, approvals, ends)
		}
		if result.Result.InputTokens != 10 || result.Result.OutputTokens != 5 || result.Result.CacheReadTokens != 2 || len(result.Result.NewMessages) != 1 || result.Result.NewMessages[0].ReasoningContent != "Thinking" {
			t.Fatalf("missing or duplicated usage/reasoning: %+v", result)
		}
	}
}

func TestOpenCodeFailuresAndCancellationNeverReportSuccess(t *testing.T) {
	// "resume" is absent on purpose: a reference the agent no longer has
	// continues in a fresh session with a notice (see
	// TestOpenCodeUnresumableSessionStartsFreshAndSaysSo).
	for _, scenario := range []string{"version", "persist", "http", "eof", "malformed", "provider", "limit", "missing-finish", "wrong-parent", "unfinished-tool", "question", "cancel"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			if scenario == "persist" {
				binding.PersistRef = func(string) error { return errors.New("disk full") }
			}
			if scenario == "cancel" {
				binding.RequestApproval = func(ctx context.Context, _ agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
					cancel()
					return agentengine.ApprovalCancel, ctx.Err()
				}
			}
			done := false
			result, err := openCodeTestSession(t, scenario, binding).RunTurn(ctx, testInput(), func(event providers.StreamEvent) { done = done || event.Type == providers.EventDone })
			if err == nil || done || result.Result.FinishReason != providers.FinishReasonError {
				t.Fatalf("false success: result=%+v err=%v done=%v", result, err, done)
			}
			if scenario == "cancel" && !errors.Is(err, context.Canceled) {
				t.Fatalf("cancel error=%v", err)
			}
		})
	}
}

func TestOpenCodeUnresumableSessionStartsFreshAndSaysSo(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	binding := testBinding()
	binding.ExternalRef = "ses_missing"
	var persisted string
	binding.PersistRef = func(value string) error { persisted = value; return nil }
	done := false
	result, err := openCodeTestSession(t, "resume", binding).RunTurn(ctx, testInput(), func(event providers.StreamEvent) {
		done = done || event.Type == providers.EventDone
	})
	if err != nil || !done {
		t.Fatalf("turn did not recover: %+v %v done=%v", result, err, done)
	}
	content := result.Result.Content
	if !strings.Contains(content, "could not resume") || !strings.HasSuffix(content, "Hello world") {
		t.Fatalf("fallback was not disclosed ahead of the answer: %q", content)
	}
	if persisted != "ses_native" {
		t.Fatalf("persisted ref = %q", persisted)
	}
}

func TestOpenCodePermissionsAreOneShotAndFailClosed(t *testing.T) {
	for _, test := range []struct {
		name, mode string
		decision   agentengine.ApprovalDecision
		handler    bool
		expected   string
	}{
		{"accept", "workspace", agentengine.ApprovalAccept, true, "once"},
		{"session", "workspace", agentengine.ApprovalAcceptForSession, true, "once"},
		{"deny", "workspace", agentengine.ApprovalDecline, true, "reject"},
		{"no-handler", "workspace", "", false, "reject"},
		{"unconfined", "unconfined", "", false, "once"},
	} {
		t.Run(test.name, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			binding.PermissionMode = test.mode
			if test.handler {
				binding.RequestApproval = func(context.Context, agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
					return test.decision, nil
				}
			}
			_, err := openCodeTestSession(t, "permission-"+test.expected, binding).RunTurn(ctx, testInput(), nil)
			if err != nil {
				t.Fatal(err)
			}
		})
	}
}

// This subprocess implements the native HTTP contract, not the adapter's private
// methods. No installed CLI, provider credentials, or real model is required.
func TestOpenCodeHelper(t *testing.T) {
	marker := -1
	for i, arg := range os.Args {
		if arg == "wuu-opencode-helper" {
			marker = i
			break
		}
	}
	if marker < 0 {
		return
	}
	scenario := os.Args[marker+1]
	if strings.Join(os.Args[marker+2:], " ") != "serve --hostname 127.0.0.1 --port 0" {
		os.Exit(31)
	}
	var config map[string]any
	if json.Unmarshal([]byte(os.Getenv("OPENCODE_CONFIG_CONTENT")), &config) != nil || config["autoupdate"] != false || config["share"] != "disabled" {
		os.Exit(32)
	}
	if scenario == "stream" {
		mcp, _ := config["mcp"].(map[string]any)
		host, _ := mcp["host"].(map[string]any)
		if host["url"] != "http://127.0.0.1:1234/mcp" || host["oauth"] != false {
			os.Exit(33)
		}
	}
	password := os.Getenv("OPENCODE_SERVER_PASSWORD")
	if len(password) < 32 || os.Getenv("OPENCODE_SERVER_USERNAME") != "wuu" {
		os.Exit(34)
	}
	events := make(chan string, 64)
	replies := make(chan string, 1)
	var mu sync.Mutex
	var final map[string]any
	var past map[string]any
	writeJSON := func(w http.ResponseWriter, value any) {
		w.Header().Set("Content-Type", "application/json")
		_ = json.NewEncoder(w).Encode(value)
	}
	emit := func(kind string, value any) {
		data, _ := json.Marshal(map[string]any{"type": kind, "properties": value})
		events <- string(data)
	}
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		username, pass, ok := r.BasicAuth()
		if !ok || username != "wuu" || pass != password || r.URL.Query().Get("directory") == "" {
			http.Error(w, "unauthorized", 401)
			return
		}
		switch {
		case r.URL.Path == "/global/health":
			version := "1.18.31"
			if scenario == "version" {
				version = "2.0.0"
			}
			writeJSON(w, map[string]any{"healthy": true, "version": version})
		case r.URL.Path == "/session" || r.Method == "PATCH":
			// "resume" is a reference the agent no longer has: the resume PATCH
			// 404s while creating a session still works, which is the fallback.
			if scenario == "resume" && r.Method == "PATCH" {
				http.Error(w, "missing session", 404)
				return
			}
			var input struct {
				Permission []struct{ Permission, Pattern, Action string } `json:"permission"`
			}
			if json.NewDecoder(r.Body).Decode(&input) != nil || len(input.Permission) != 1 || input.Permission[0].Action != "ask" || input.Permission[0].Permission != "*" || input.Permission[0].Pattern != "*" {
				http.Error(w, "unsafe permissions", 400)
				return
			}
			writeJSON(w, map[string]string{"id": "ses_native"})
		case r.URL.Path == "/event":
			w.Header().Set("Content-Type", "text/event-stream")
			fmt.Fprint(w, "data: {\"type\":\"server.connected\",\"properties\":{}}\n\n")
			w.(http.Flusher).Flush()
			for {
				select {
				case data := <-events:
					if data == "EOF" {
						return
					}
					fmt.Fprintf(w, "data: %s\n\n", data)
					w.(http.Flusher).Flush()
				case <-r.Context().Done():
					return
				}
			}
		case strings.HasSuffix(r.URL.Path, "/abort"):
			writeJSON(w, true)
		case strings.HasPrefix(r.URL.Path, "/question/"):
			writeJSON(w, true)
		case strings.HasPrefix(r.URL.Path, "/permission/"):
			var reply struct {
				Reply string `json:"reply"`
			}
			_ = json.NewDecoder(r.Body).Decode(&reply)
			replies <- reply.Reply
			writeJSON(w, true)
		case r.URL.Path == "/session/ses_native/message" && r.Method == "GET":
			mu.Lock()
			defer mu.Unlock()
			writeJSON(w, []any{past, final})
		case r.URL.Path == "/session/ses_native/message" && r.Method == "POST":
			if scenario == "http" {
				http.Error(w, "provider unavailable", 503)
				return
			}
			var prompt struct {
				MessageID, System string
				Model             map[string]string
				Parts             []map[string]string
			}
			if json.NewDecoder(r.Body).Decode(&prompt) != nil || !strings.HasPrefix(prompt.MessageID, "msg_") {
				http.Error(w, "invalid prompt", 400)
				return
			}
			if scenario == "stream" && (prompt.System != "host instructions" || prompt.Model["providerID"] != "example" || len(prompt.Parts) != 2 || prompt.Parts[1]["url"] != "data:image/png;base64,aGVsbG8=") {
				http.Error(w, "lost prompt input", 400)
				return
			}
			info := map[string]any{"id": "msg_answer", "sessionID": "ses_native", "role": "assistant", "parentID": prompt.MessageID, "finish": "stop", "time": map[string]int64{"completed": 123}, "tokens": map[string]any{"input": 10, "output": 5, "cache": map[string]int{"read": 2}}}
			part := func(id, kind, text string) map[string]any {
				return map[string]any{"id": id, "sessionID": "ses_native", "messageID": "msg_answer", "type": kind, "text": text}
			}
			tool := part("prt_tool", "tool", "")
			tool["callID"], tool["tool"], tool["state"] = "tool_read", "read", map[string]any{"status": "completed", "input": map[string]string{"path": "README.md"}, "output": "file contents"}
			if scenario == "unfinished-tool" {
				tool["state"] = map[string]string{"status": "running"}
			}
			mu.Lock()
			final = map[string]any{"info": info, "parts": []any{part("prt_reason", "reasoning", "Thinking"), part("prt_text", "text", "Hello world"), tool}}
			past = map[string]any{"info": map[string]any{"id": "old", "sessionID": "ses_native", "role": "assistant", "parentID": "old_user"}, "parts": []any{part("old_part", "text", "Must not replay")}}
			mu.Unlock()
			emit("session.idle", map[string]string{"sessionID": "ses_native"})
			switch scenario {
			case "eof":
				events <- "EOF"
			case "malformed":
				events <- "{broken"
			case "provider":
				emit("session.error", map[string]any{"sessionID": "ses_native", "error": map[string]any{"name": "APIError", "data": map[string]string{"message": "provider failed"}}})
			case "question":
				emit("question.asked", map[string]string{"id": "que_one", "sessionID": "ses_native"})
			}
			if scenario == "eof" || scenario == "malformed" || scenario == "provider" || scenario == "question" {
				<-r.Context().Done()
				return
			}
			if scenario == "stream" {
				emit("message.updated", map[string]any{"info": info})
				emit("message.part.updated", map[string]any{"part": part("prt_reason", "reasoning", "Thinking")})
				emit("message.part.updated", map[string]any{"part": part("prt_text", "text", "")})
				emit("message.part.delta", map[string]string{"sessionID": "ses_native", "messageID": "msg_answer", "partID": "prt_text", "field": "text", "delta": "Hello "})
				// A stale snapshot must not duplicate content or suppress later deltas.
				emit("message.part.updated", map[string]any{"part": part("prt_text", "text", "Hello")})
			}
			if scenario == "stream" || scenario == "cancel" || strings.HasPrefix(scenario, "permission-") {
				emit("permission.asked", map[string]any{"id": "per_one", "sessionID": "ses_native", "permission": "bash", "patterns": []string{"pwd"}, "metadata": map[string]string{"command": "pwd"}})
				select {
				case reply := <-replies:
					want := "once"
					if strings.HasPrefix(scenario, "permission-") {
						want = strings.TrimPrefix(scenario, "permission-")
					}
					if reply != want {
						http.Error(w, "wrong approval scope", 400)
						return
					}
				case <-r.Context().Done():
					return
				}
			}
			mu.Lock()
			defer mu.Unlock()
			if scenario == "limit" {
				info["finish"] = "length"
			}
			if scenario == "missing-finish" {
				delete(info, "finish")
			}
			if scenario == "wrong-parent" {
				info["parentID"] = "other-user"
			}
			writeJSON(w, final)
		default:
			http.Error(w, "unexpected request", 404)
		}
	}))
	fmt.Println("opencode server listening on " + server.URL)
	select {}
}
