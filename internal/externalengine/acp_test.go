package externalengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/enginecatalog"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func testEngine(t *testing.T, scenario string) *Engine {
	t.Helper()
	binary, err := os.Executable()
	if err != nil {
		t.Fatal(err)
	}
	return New(enginecatalog.Entry{ID: "test", Name: "Test", Protocol: "acp", Args: []string{"-test.run=^TestACPHelper$", "--", "wuu-acp-helper", scenario}}, binary, t.TempDir())
}

func testBinding() agentengine.ThreadBinding {
	return agentengine.ThreadBinding{PermissionMode: "workspace", ThreadID: "host-thread"}
}
func testInput() agentengine.TurnInput {
	return agentengine.TurnInput{History: []providers.ChatMessage{{Role: "user", Content: "Please inspect the project"}}}
}

func TestACPStreamsAndResumesWithoutReplayingHistory(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	engine := testEngine(t, "stream")
	binding := testBinding()
	var ref string
	binding.PersistRef = func(value string) error { ref = value; return nil }
	session, err := engine.SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	var events []providers.StreamEvent
	result, err := session.RunTurn(ctx, testInput(), func(event providers.StreamEvent) { events = append(events, event) })
	if err != nil {
		t.Fatal(err)
	}
	if ref != "native-session" || result.Result.Content != "Hello world" || len(result.Result.NewMessages) != 1 {
		t.Fatalf("ref=%q result=%+v", ref, result)
	}
	if result.Result.NewMessages[0].ReasoningContent != "Thinking" {
		t.Fatalf("missing reasoning: %+v", result)
	}
	var starts, ends, done int
	for _, event := range events {
		switch event.Type {
		case providers.EventToolUseStart:
			starts++
		case providers.EventToolUseEnd:
			ends++
			if event.ToolResult != "file contents" {
				t.Fatalf("result=%q", event.ToolResult)
			}
		case providers.EventDone:
			done++
		}
	}
	if starts != 1 || ends != 1 || done != 1 {
		t.Fatalf("starts=%d ends=%d done=%d", starts, ends, done)
	}
	binding.ExternalRef = ref
	session, err = engine.SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err = session.RunTurn(ctx, testInput(), nil)
	if err != nil || result.Result.Content != "Hello world" {
		t.Fatalf("replay leaked or resume failed: %+v %v", result, err)
	}
}

func TestACPFailureIsNeverSuccessfulCompletion(t *testing.T) {
	for _, scenario := range []string{"eof", "malformed", "version", "missing-stop", "limit", "load-error", "no-load", "no-images", "unknown-id"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			if scenario == "load-error" || scenario == "no-load" {
				binding.ExternalRef = "old-session"
			}
			input := testInput()
			if scenario == "no-images" {
				input.History[0].Images = []providers.InputImage{{MediaType: "image/png", Data: "aGVsbG8="}}
			}
			session, err := testEngine(t, scenario).SessionForThread(ctx, binding)
			if err != nil {
				t.Fatal(err)
			}
			done := false
			result, err := session.RunTurn(ctx, input, func(event providers.StreamEvent) {
				if event.Type == providers.EventDone {
					done = true
				}
			})
			if err == nil || done || result.Result.FinishReason != providers.FinishReasonError {
				t.Fatalf("false success: %+v %v done=%v", result, err, done)
			}
		})
	}
}

func TestACPPermissionsFailClosedAndDoNotPersistGrants(t *testing.T) {
	for _, decision := range []agentengine.ApprovalDecision{agentengine.ApprovalAccept, agentengine.ApprovalAcceptForSession, agentengine.ApprovalDecline, agentengine.ApprovalCancel, "no-handler"} {
		t.Run(string(decision), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			binding := testBinding()
			if decision != "no-handler" {
				binding.RequestApproval = func(_ context.Context, req agentengine.ApprovalRequest) (agentengine.ApprovalDecision, error) {
					if req.EngineID != "test" || req.Kind != agentengine.ApprovalCommandExecution || req.ItemID != "exec-1" {
						t.Errorf("bad approval: %+v", req)
					}
					return decision, nil
				}
			}
			session, err := testEngine(t, "permission").SessionForThread(ctx, binding)
			if err != nil {
				t.Fatal(err)
			}
			result, err := session.RunTurn(ctx, testInput(), nil)
			if err != nil {
				t.Fatal(err)
			}
			want := "deny"
			if decision == agentengine.ApprovalAccept || decision == agentengine.ApprovalAcceptForSession {
				want = "once"
			}
			if decision == agentengine.ApprovalCancel {
				want = "cancelled"
			}
			if result.Result.Content != want {
				t.Fatalf("got %q want %q", result.Result.Content, want)
			}
		})
	}
}

func TestACPRefPersistenceFailureStopsBeforePrompt(t *testing.T) {
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	binding := testBinding()
	binding.PersistRef = func(string) error { return errors.New("disk unavailable") }
	session, err := testEngine(t, "stream").SessionForThread(ctx, binding)
	if err != nil {
		t.Fatal(err)
	}
	result, err := session.RunTurn(ctx, testInput(), nil)
	if err == nil || !strings.Contains(err.Error(), "disk unavailable") || result.Result.Content != "" {
		t.Fatalf("%+v %v", result, err)
	}
}

func TestACPCancelAndCloseStopQuietAgent(t *testing.T) {
	for _, closeSession := range []bool{false, true} {
		t.Run(fmt.Sprint(closeSession), func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
			defer cancel()
			session, err := testEngine(t, "quiet").SessionForThread(ctx, testBinding())
			if err != nil {
				t.Fatal(err)
			}
			started := make(chan struct{})
			finished := make(chan error, 1)
			go func() {
				_, err := session.RunTurn(ctx, testInput(), func(event providers.StreamEvent) {
					if event.Type == providers.EventContentDelta {
						close(started)
					}
				})
				finished <- err
			}()
			select {
			case <-started:
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
			if closeSession {
				err = session.Close(ctx)
			} else {
				err = session.Interrupt(ctx, "user")
			}
			if err != nil {
				t.Fatal(err)
			}
			select {
			case err := <-finished:
				if !errors.Is(err, context.Canceled) {
					t.Fatal(err)
				}
			case <-ctx.Done():
				t.Fatal("cancellation hung")
			}
		})
	}
}

// TestACPHelper is an isolated fake process, not a model connection. Requests
// drive it deterministically; it never reads user settings or credentials.
func TestACPHelper(t *testing.T) {
	args := os.Args
	if len(args) < 2 || args[len(args)-2] != "wuu-acp-helper" {
		return
	}
	scenario := args[len(args)-1]
	scanner := bufio.NewScanner(os.Stdin)
	write := func(value any) { _ = json.NewEncoder(os.Stdout).Encode(value) }
	update := func(kind string, fields map[string]any) {
		fields["sessionUpdate"] = kind
		write(map[string]any{"jsonrpc": "2.0", "method": "session/update", "params": map[string]any{"sessionId": "native-session", "update": fields}})
	}
	text := func(value string) {
		update("agent_message_chunk", map[string]any{"content": map[string]string{"type": "text", "text": value}})
	}
	var promptID json.RawMessage
	for scanner.Scan() {
		var msg rpcMessage
		if json.Unmarshal(scanner.Bytes(), &msg) != nil {
			os.Exit(2)
		}
		result := any(map[string]any{})
		switch msg.Method {
		case "initialize":
			version := 1
			if scenario == "version" {
				version = 2
			}
			result = map[string]any{"protocolVersion": version, "agentCapabilities": map[string]any{"loadSession": scenario != "no-load"}}
		case "session/new":
			result = map[string]any{"sessionId": "native-session"}
		case "session/load":
			if scenario == "load-error" {
				write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "error": map[string]any{"code": -32000, "message": "session not found"}})
				continue
			}
			text("old replay")
		case "session/prompt":
			promptID = msg.ID
			if scenario == "eof" {
				os.Exit(0)
			}
			if scenario == "malformed" {
				fmt.Println("not JSON")
				continue
			}
			if scenario == "unknown-id" {
				write(map[string]any{"jsonrpc": "2.0", "id": "unrelated", "result": map[string]string{"stopReason": "end_turn"}})
				continue
			}
			if scenario == "permission" {
				write(map[string]any{"jsonrpc": "2.0", "id": 99, "method": "session/request_permission", "params": map[string]any{"sessionId": "native-session", "toolCall": map[string]string{"toolCallId": "exec-1", "title": "Run command", "kind": "execute"}, "options": []any{map[string]string{"optionId": "forever", "kind": "allow_always"}, map[string]string{"optionId": "once", "kind": "allow_once"}, map[string]string{"optionId": "deny", "kind": "reject_once"}}}})
				continue
			}
			text("Hello ")
			if scenario == "quiet" {
				continue
			}
			update("agent_thought_chunk", map[string]any{"content": map[string]string{"type": "text", "text": "Thinking"}})
			update("tool_call", map[string]any{"toolCallId": "read-1", "title": "Read file", "status": "in_progress", "rawInput": map[string]string{"path": "README.md"}})
			update("tool_call_update", map[string]any{"toolCallId": "read-1", "status": "completed", "content": []any{map[string]any{"type": "content", "content": map[string]string{"type": "text", "text": "file contents"}}}})
			text("world")
			stop := "end_turn"
			if scenario == "limit" {
				stop = "max_tokens"
			}
			if scenario == "missing-stop" {
				stop = ""
			}
			result = map[string]string{"stopReason": stop}
		case "session/cancel":
			os.Exit(0)
		case "":
			var reply struct {
				Outcome struct {
					Outcome  string `json:"outcome"`
					OptionID string `json:"optionId"`
				} `json:"outcome"`
			}
			if json.Unmarshal(msg.Result, &reply) != nil {
				os.Exit(3)
			}
			answer := reply.Outcome.OptionID
			if reply.Outcome.Outcome == "cancelled" {
				answer = "cancelled"
			}
			text(answer)
			write(map[string]any{"jsonrpc": "2.0", "id": promptID, "result": map[string]string{"stopReason": "end_turn"}})
			continue
		}
		write(map[string]any{"jsonrpc": "2.0", "id": msg.ID, "result": result})
	}
	os.Exit(0)
}
