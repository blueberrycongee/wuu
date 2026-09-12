package host

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/appserver"
)

func TestMobileActivityBoundsToolsAcrossSnapshotsPagesAndEvents(t *testing.T) {
	tool := appserver.ThreadItem{ID: "tool", Type: appserver.ThreadItemToolCall, Name: "read_file",
		Status: appserver.ThreadItemStatusCompleted, Arguments: `{"path":"fixture.txt"}`, Result: strings.Repeat("工具🌱\n", 30_000)}
	rawTool, _ := json.Marshal(tool)
	turn := map[string]any{"id": "turn", "items": []any{tool, map[string]string{"id": "reason", "type": "reasoning", "text": "private reasoning"}}}
	thread := map[string]any{"id": "thread", "turns": []any{turn}}
	for name, line := range map[string]any{
		"snapshot":     map[string]any{"id": "resume", "result": map[string]any{"thread": thread}},
		"page":         map[string]any{"id": "page", "result": map[string]any{"thread_id": "thread", "turns": []any{turn}, "history_cursor": "next"}},
		"thread event": map[string]any{"method": "thread/resumed", "params": map[string]any{"thread": thread}},
		"turn event":   map[string]any{"method": "turn/completed", "params": map[string]any{"thread_id": "thread", "turn": turn}},
		"item event":   map[string]any{"method": "item/completed", "params": map[string]any{"thread_id": "thread", "turn_id": "turn", "item": tool}},
	} {
		t.Run(name, func(t *testing.T) {
			raw, _ := json.Marshal(line)
			out, keep := (mobileChatFilter{tools: true}).line(raw)
			if !keep || len(out) > 64*1024 || strings.Contains(string(out), "private reasoning") {
				t.Fatalf("unbounded or missing activity: keep=%v bytes=%d", keep, len(out))
			}
			// All paths must produce the same address, so a later explicit read
			// recovers the original item rather than a hash of an earlier preview.
			digest := sha256.Sum256(rawTool)
			address, _ := json.Marshal([]string{"thread", "turn", "tool", fmt.Sprintf("%x", digest)})
			ref := "content:" + base64.RawURLEncoding.EncodeToString(address)
			if !strings.Contains(string(out), ref) || !strings.Contains(string(out), `"status":"completed"`) {
				t.Fatalf("tool lost identity or status: %s", out)
			}
			if name == "page" && !strings.Contains(string(out), `"history_cursor":"next"`) {
				t.Fatal("tool filtering lost page cursor")
			}
			again, _ := (mobileChatFilter{tools: true}).line(out)
			if string(again) != string(out) {
				t.Fatal("reprojection changed content address")
			}
		})
	}
	if _, keep := (mobileChatFilter{tools: true}).line([]byte(`{"method":"item/toolCall/outputDelta","params":{"delta":"heavy"}}`)); keep {
		t.Fatal("activity profile must not send unbounded tool deltas")
	}
}

func TestFilterMobileChatLineKeepsTextStreamingWithoutToolOutput(t *testing.T) {
	line := []byte(`{"method":"item/toolCall/outputDelta","params":{"thread_id":"t","turn_id":"turn","item_id":"i","delta":"hidden"}}`)
	if got, keep := filterMobileChatLine(line); keep || got != nil {
		t.Fatalf("hidden delta should be dropped, got keep=%v line=%s", keep, got)
	}
	visible := []byte(`{"method":"item/agentMessage/delta","params":{"thread_id":"t","turn_id":"turn","item_id":"i","delta":"hello"}}`)
	got, keep := filterMobileChatLine(visible)
	if !keep || !json.Valid(got) {
		t.Fatalf("phone lost streaming text: keep=%v line=%s", keep, got)
	}
}

func TestFilterMobileChatLineKeepsServerRequests(t *testing.T) {
	line := []byte(`{"id":"server-1","method":"tool/approval/request","params":{"tool":"bash"}}`)
	got, keep := filterMobileChatLine(line)
	if !keep || string(got) != string(line) {
		t.Fatalf("server request should pass through unchanged, keep=%v got=%s", keep, got)
	}
}

func TestMobileChatHistoryPageKeepsCursorAndErrorWithoutHeavyItems(t *testing.T) {
	line := []byte(`{"id":"page","result":{"thread_id":"t","cursor":"before","history_cursor":"next","turns":[{"id":"turn","error":{"message":"failed"},"items":[{"id":"u","type":"user_message","text":"hello"},{"id":"tool","type":"tool_call","result":"heavy"}]}]}}`)
	got, keep := filterMobileChatLine(line)
	var response struct {
		Result struct {
			HistoryCursor string           `json:"history_cursor"`
			Turns         []appserver.Turn `json:"turns"`
		} `json:"result"`
	}
	if err := json.Unmarshal(got, &response); err != nil {
		t.Fatal(err)
	}
	if !keep || response.Result.HistoryCursor != "next" || len(response.Result.Turns) != 1 {
		t.Fatalf("lost history navigation: %s", got)
	}
	turn := response.Result.Turns[0]
	if len(turn.Items) != 1 || turn.Items[0].Text != "hello" || turn.Error == nil || turn.Error.Message != "failed" {
		t.Fatalf("incorrect phone page: %s", got)
	}
}

func TestMobileChatCanObserveAndResolveConversationQuestions(t *testing.T) {
	// Engine approvals use the question broker, not reverse JSON-RPC requests.
	for _, line := range []string{
		`{"method":"user-question/requested","params":{"type":"requested","request":{"request_id":"q1","thread_id":"t","questions":[{"id":"approval","question":"Allow command?","options":[{"label":"Allow"},{"label":"Decline"}]}]}}}`,
		`{"method":"user-question/resolved","params":{"type":"resolved","request_id":"q1","thread_id":"t","outcome":"answered"}}`,
	} {
		got, keep := filterMobileChatLine([]byte(line))
		if !keep {
			t.Fatalf("phone lost question lifecycle event: %s", line)
		}
		var before, after map[string]any
		if err := json.Unmarshal([]byte(line), &before); err != nil {
			t.Fatal(err)
		}
		if err := json.Unmarshal(got, &after); err != nil {
			t.Fatal(err)
		}
		want, _ := json.Marshal(before)
		actual, _ := json.Marshal(after)
		if string(want) != string(actual) {
			t.Fatalf("question payload changed: %s", got)
		}
	}
}

func TestFilterMobileChatLineSlimsThreadListResponse(t *testing.T) {
	line := []byte(`{
		"id":"list-1",
		"result":{
			"threads":[
				{
					"id":"non-project",
					"source":"automation",
					"turns":[{
						"id":"turn-1",
						"items":[
							{"id":"u","type":"user_message","text":"hi"},
							{"id":"a","type":"agent_message","text":"working"},
							{"id":"tool","type":"tool_call","name":"bash"}
						],
						"items_view":"full",
						"status":"completed"
					}],
					"child_agents":[{"id":"agent-1"}],
					"browser_state":{"current_url":"https://example.com"}
				},
				{"id":"project","workspace_kind":"project","turns":[{"id":"p","items":[]}]}
			]
		}
	}`)
	got, keep := filterMobileChatLine(line)
	if !keep {
		t.Fatal("thread list response should be kept")
	}
	var env struct {
		Result struct {
			Threads []struct {
				ID          string           `json:"id"`
				ChildAgents json.RawMessage  `json:"child_agents"`
				Browser     json.RawMessage  `json:"browser_state"`
				Turns       []appserver.Turn `json:"turns"`
			} `json:"threads"`
		} `json:"result"`
	}
	if err := json.Unmarshal(got, &env); err != nil {
		t.Fatalf("decode filtered line: %v\n%s", err, got)
	}
	if len(env.Result.Threads) != 2 {
		t.Fatalf("phone lost visible conversations: %+v", env.Result.Threads)
	}
	first := env.Result.Threads[0]
	if len(first.ChildAgents) != 0 || len(first.Browser) != 0 || len(first.Turns) != 1 || itemTypes(first.Turns[0].Items) != "user_message,agent_message" {
		t.Fatalf("phone received heavy conversation content: %+v", first)
	}
}

func TestFilterMobileChatLineKeepsOnlyVisibleItemNotifications(t *testing.T) {
	hidden := []byte(`{"method":"item/completed","params":{"thread_id":"t","turn_id":"turn","item":{"id":"tool","type":"tool_call","name":"bash"}}}`)
	if got, keep := filterMobileChatLine(hidden); keep || got != nil {
		t.Fatalf("tool item notification should be dropped, keep=%v got=%s", keep, got)
	}
	visible := []byte(`{"method":"item/completed","params":{"thread_id":"t","turn_id":"turn","item":{"id":"msg","type":"agent_message","text":"done"}}}`)
	got, keep := filterMobileChatLine(visible)
	if !keep {
		t.Fatal("agent message notification should be kept")
	}
	var env struct {
		Method string `json:"method"`
	}
	if err := json.Unmarshal(got, &env); err != nil || env.Method != appserver.NotificationItemCompleted {
		t.Fatalf("visible notification corrupted: method=%q err=%v line=%s", env.Method, err, got)
	}
}

func itemTypes(items []appserver.ThreadItem) string {
	out := ""
	for i, item := range items {
		if i > 0 {
			out += ","
		}
		out += string(item.Type)
	}
	return out
}
