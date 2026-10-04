package appserver

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

const responseSelectionFixture = `[{"type":"response_selection","text":"Quoted assistant response (JSON):\n{\"text\":\"Alpha 🌊\",\"comment\":\"Explain this.\"}\n","selection":{"id":"selection-1","text":"Alpha 🌊","comment":"Explain this.","source":{"thread_id":"source-thread","turn_id":"source-turn","item_id":"source-item","start_offset":4,"end_offset":12}}},{"type":"text","text":"Compare it."}]`

func assertContentPartsWire(t *testing.T, parts []providers.MessageContentPart, want string) {
	t.Helper()
	raw, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	var gotValue, wantValue any
	if err := json.Unmarshal(raw, &gotValue); err != nil {
		t.Fatal(err)
	}
	if err := json.Unmarshal([]byte(want), &wantValue); err != nil {
		t.Fatal(err)
	}
	if !reflect.DeepEqual(gotValue, wantValue) {
		t.Fatalf("content parts wire = %s, want %s", raw, want)
	}
}

func TestResponseSelectionValidation(t *testing.T) {
	for _, tc := range []struct{ name, old, replacement string }{
		{"valid", "", ""},
		{"missing_id", `"id":"selection-1"`, `"id":""`},
		{"empty_text", `"text":"Alpha 🌊"`, `"text":""`},
		{"missing_thread", `"thread_id":"source-thread"`, `"thread_id":""`},
		{"missing_turn", `"turn_id":"source-turn"`, `"turn_id":""`},
		{"missing_item", `"item_id":"source-item"`, `"item_id":""`},
		{"negative_start", `"start_offset":4`, `"start_offset":-1`},
		{"empty_range", `"end_offset":12`, `"end_offset":4`},
		{"reversed_range", `"end_offset":12`, `"end_offset":3`},
		{"wrong_utf16_span", `"end_offset":12`, `"end_offset":11`},
		{"mismatched_text", `"text":"Alpha 🌊"`, `"text":"Other text"`},
		{"mismatched_comment", `"comment":"Explain this."`, `"comment":"Hidden comment"`},
		{"invalid_json", `\"text\":`, `\"text\"!`},
		{"trailing_payload", `}\n","selection"`, `}\nHidden text\n","selection"`},
		{"missing_selection", `"selection":{`, `"ignored_selection":{`},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := responseSelectionFixture
			if tc.old != "" {
				wire = strings.Replace(wire, tc.old, tc.replacement, 1)
			}
			var parts []providers.MessageContentPart
			if err := json.Unmarshal([]byte(wire), &parts); err != nil {
				t.Fatal(err)
			}
			prompt := parts[0].Text + parts[1].Text
			msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt {
				t.Fatal("canonical provider text changed")
			}
			if tc.name == "valid" {
				assertContentPartsWire(t, msg.ContentParts, wire)
				return
			}
			want := []providers.MessageContentPart{{Type: "text", Text: parts[0].Text}, parts[1]}
			if !reflect.DeepEqual(msg.ContentParts, want) {
				t.Fatalf("invalid selection must retain plain text fallback: %+v", msg.ContentParts)
			}
		})
	}
}

func responseSelectionRangeWire(t *testing.T) string {
	t.Helper()
	var parts []map[string]any
	if err := json.Unmarshal([]byte(responseSelectionFixture), &parts); err != nil {
		t.Fatal(err)
	}
	selection := parts[0]["selection"].(map[string]any)
	selection["text"] = "Alpha\n🌊"
	payload, err := json.Marshal(struct {
		Text    string `json:"text"`
		Comment string `json:"comment"`
	}{Text: "Alpha\n🌊", Comment: "Explain this."})
	if err != nil {
		t.Fatal(err)
	}
	parts[0]["text"] = "Quoted assistant response (JSON):\n" + string(payload) + "\n"
	source := selection["source"].(map[string]any)
	source["end_offset"] = 11
	source["range_text"] = "Alpha🌊"
	wire, err := json.Marshal(parts)
	if err != nil {
		t.Fatal(err)
	}
	return string(wire)
}

func TestResponseSelectionRangeTextValidation(t *testing.T) {
	for _, tc := range []struct {
		name, replacement string
		valid             bool
	}{
		{"raw_dom_range", `"range_text":"Alpha🌊"`, true},
		{"wrong_raw_range", `"range_text":"Alpha"`, false},
		{"empty_raw_uses_readable", `"range_text":""`, false},
	} {
		t.Run(tc.name, func(t *testing.T) {
			wire := strings.Replace(responseSelectionRangeWire(t), `"range_text":"Alpha🌊"`, tc.replacement, 1)
			var parts []providers.MessageContentPart
			if err := json.Unmarshal([]byte(wire), &parts); err != nil {
				t.Fatal(err)
			}
			prompt := parts[0].Text + parts[1].Text
			msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt {
				t.Fatal("readable provider text changed")
			}
			if tc.valid {
				assertContentPartsWire(t, msg.ContentParts, wire)
				return
			}
			if len(msg.ContentParts) != 2 || msg.ContentParts[0].Type != "text" || msg.ContentParts[0].Selection != nil {
				t.Fatal("invalid range did not fall back to text")
			}
		})
	}
}

func TestUserMessageFromPromptPreservesValidContentParts(t *testing.T) {
	prompt := "pasted body\nregular text"
	parts := []providers.MessageContentPart{
		{Type: "pasted_text", Text: "pasted body\n"},
		{Type: "text", Text: "regular text"},
	}

	msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	if msg.Content != prompt {
		t.Fatalf("Content = %q, want %q", msg.Content, prompt)
	}
	if len(msg.ContentParts) != 2 || msg.ContentParts[0].Type != "pasted_text" {
		t.Fatalf("ContentParts = %#v, want structured pasted and text parts", msg.ContentParts)
	}
}

func TestResponseSelectionSnapshotIsolation(t *testing.T) {
	var parts []providers.MessageContentPart
	if err := json.Unmarshal([]byte(responseSelectionFixture), &parts); err != nil {
		t.Fatal(err)
	}
	msg, err := userMessageFromPrompt(parts[0].Text+parts[1].Text, nil, nil, parts)
	if err != nil {
		t.Fatal(err)
	}
	parts[0].Selection.Text = "caller mutation"
	assertContentPartsWire(t, msg.ContentParts, responseSelectionFixture)
	item := ThreadItem{ContentParts: msg.ContentParts}
	snapshot := cloneThreadItem(item)
	snapshot.ContentParts[0].Selection.Source.ItemID = "reader mutation"
	assertContentPartsWire(t, item.ContentParts, responseSelectionFixture)
	summary := heldUserMessageSummary("thread", queuedTurn{msg: msg})
	summary.ContentParts[0].Selection.Comment = "summary mutation"
	assertContentPartsWire(t, msg.ContentParts, responseSelectionFixture)
}

func TestResponseSelectionQueuedHeldResumeAndSteer(t *testing.T) {
	client := newBlockingStreamClient("done")
	rt := newTestRuntime(t, &fakeClient{})
	rt.StreamRunner.Client = client
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	rpc := func(id, method string, params any) any {
		t.Helper()
		raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
		if err != nil {
			t.Fatal(err)
		}
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		response := responseByID(t, parseOutput(t, out.String()), id)
		if response["error"] != nil {
			t.Fatalf("%s: %+v", method, response["error"])
		}
		return response["result"]
	}
	threadID := remarshal[ThreadStartResult](t, rpc("start", MethodThreadStart, nil)).Thread.ID
	started := remarshal[TurnStartResult](t, rpc("turn", MethodTurnStart, TurnStartParams{ThreadID: threadID, Prompt: "first"}))
	<-client.started
	var parts []providers.MessageContentPart
	if err := json.Unmarshal([]byte(responseSelectionFixture), &parts); err != nil {
		t.Fatal(err)
	}
	prompt := parts[0].Text + parts[1].Text
	params := map[string]any{"thread_id": threadID, "prompt": prompt, "content_parts": parts, "client_id": "queue-1"}
	rpc("queue", MethodTurnQueue, params)
	delete(params, "client_id")
	params["queue_id"] = "queue-1"
	rpc("update", MethodTurnUpdateQueued, params)
	pending := srv.pendingUserMessageSummaries(threadID)
	if len(pending) != 1 {
		t.Fatalf("pending = %+v", pending)
	}
	assertContentPartsWire(t, pending[0].ContentParts, responseSelectionFixture)
	delete(params, "queue_id")
	params["client_id"] = "steer-1"
	params["expected_turn_id"] = started.Turn.ID
	rpc("steer", MethodTurnSteer, params)
	rpc("interrupt", MethodTurnInterrupt, map[string]any{"thread_id": threadID})
	waitForMethod(t, out, NotificationTurnError)
	held, err := srv.loadHeldUserTurns(threadID)
	if err != nil {
		t.Fatal(err)
	}
	if len(held) != 2 {
		t.Fatalf("held = %+v", held)
	}
	for _, entry := range held {
		assertContentPartsWire(t, entry.msg.ContentParts, responseSelectionFixture)
	}
	srv.Close()
	out = &lockedBuffer{}
	srv = New(rt, out)
	t.Cleanup(srv.Close)
	resumed := remarshal[ThreadResumeResult](t, rpc("resume", MethodThreadResume, ThreadResumeParams{SessionID: threadID}))
	if len(resumed.HeldUserMessages) != 2 {
		t.Fatalf("resumed held = %+v", resumed.HeldUserMessages)
	}
	for _, entry := range resumed.HeldUserMessages {
		assertContentPartsWire(t, entry.ContentParts, responseSelectionFixture)
	}
	delete(params, "client_id")
	params["queue_id"] = "queue-1"
	delete(params, "expected_turn_id")
	rpc("held-update", MethodTurnUpdateQueued, params)
	// Releasing by stable ID must use the durable selection even without resending metadata.
	rpc("release", MethodTurnSteer, map[string]any{"thread_id": threadID, "client_id": "queue-1", "prompt": prompt})
	close(client.release)
	waitForTurnCompletedForThread(t, out, threadID)
	history, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	for _, msg := range history {
		if msg.ClientID == "queue-1" {
			if msg.Content != prompt {
				t.Fatalf("released provider text = %q", msg.Content)
			}
			assertContentPartsWire(t, msg.ContentParts, responseSelectionFixture)
			return
		}
	}
	t.Fatal("released selection absent from durable history")
}

func TestUserMessageFromPromptRejectsMismatchedContentParts(t *testing.T) {
	msg, err := userMessageFromPrompt(
		"canonical prompt",
		nil,
		nil,
		[]providers.MessageContentPart{{Type: "pasted_text", Text: "different text"}},
	)
	if err != nil {
		t.Fatal(err)
	}
	if len(msg.ContentParts) != 0 {
		t.Fatalf("ContentParts = %#v, want no presentation metadata for mismatched content", msg.ContentParts)
	}
}

func fileSelectionPartForTest(intent string) providers.MessageContentPart {
	part := providers.MessageContentPart{
		Type: "file_selection", ID: "selection-1", Intent: intent,
		Source: &providers.FileSelectionSource{
			Workspace: "/workspace", Path: "src/main.go",
			StartLine: 3, StartColumn: 2, EndLine: 4, EndColumn: 1,
			Quote: "return value\n", Revision: "sha256:captured-revision",
		},
	}
	if intent != "quote" {
		part.Comment = "Explain or improve this return."
	}
	// The client supplies the serialization; the server must preserve it verbatim.
	source, _ := json.Marshal(part.Source)
	part.Text = fmt.Sprintf("<file_selection id=%q intent=%q>\n%s\n%s\n</file_selection>", part.ID, part.Intent, source, part.Comment)
	return part
}

func TestFileSelectionContentPartsPreserveCanonicalPrompt(t *testing.T) {
	for _, intent := range []string{"comment", "edit", "quote"} {
		t.Run(intent, func(t *testing.T) {
			part := fileSelectionPartForTest(intent)
			parts := []providers.MessageContentPart{
				{Type: "text", Text: "  Review this:\n"}, part,
				{Type: "pasted_text", Text: "\nAdditional reference\n", Title: "Reference"},
			}
			prompt := strings.TrimSpace(parts[0].Text + part.Text + parts[2].Text)
			msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt || !reflect.DeepEqual(msg.ContentParts, parts) {
				t.Fatalf("normalized message lost content or metadata: %+v", msg)
			}
			parts[1].Text = strings.Replace(part.Text, "return value", "return other", 1)
			msg, err = userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt || len(msg.ContentParts) != 0 {
				t.Fatalf("mismatched metadata changed canonical prompt or survived: %+v", msg)
			}
		})
	}
}

func TestFileSelectionContentPartsDiscardInvalidMetadata(t *testing.T) {
	tests := map[string]func(*providers.MessageContentPart){
		"missing source":        func(p *providers.MessageContentPart) { p.Source = nil },
		"blank workspace":       func(p *providers.MessageContentPart) { p.Source.Workspace = " \t" },
		"blank path":            func(p *providers.MessageContentPart) { p.Source.Path = " " },
		"blank revision":        func(p *providers.MessageContentPart) { p.Source.Revision = " " },
		"empty quote":           func(p *providers.MessageContentPart) { p.Source.Quote = "" },
		"blank id":              func(p *providers.MessageContentPart) { p.ID = " " },
		"missing intent":        func(p *providers.MessageContentPart) { p.Intent = "" },
		"unknown intent":        func(p *providers.MessageContentPart) { p.Intent = "delete" },
		"zero start line":       func(p *providers.MessageContentPart) { p.Source.StartLine = 0 },
		"negative start column": func(p *providers.MessageContentPart) { p.Source.StartColumn = -1 },
		"zero end line":         func(p *providers.MessageContentPart) { p.Source.EndLine = 0 },
		"zero end column":       func(p *providers.MessageContentPart) { p.Source.EndColumn = 0 },
		"reversed lines":        func(p *providers.MessageContentPart) { p.Source.EndLine = p.Source.StartLine - 1 },
		"reversed columns": func(p *providers.MessageContentPart) {
			p.Source.EndLine = p.Source.StartLine
			p.Source.EndColumn = p.Source.StartColumn - 1
		},
	}
	for name, mutate := range tests {
		t.Run(name, func(t *testing.T) {
			part := fileSelectionPartForTest("edit")
			mutate(&part)
			prompt := "Review:\n" + part.Text
			msg, err := userMessageFromPrompt(prompt, nil, nil, []providers.MessageContentPart{
				{Type: "text", Text: "Review:\n"}, part,
			})
			if err != nil {
				t.Fatal(err)
			}
			if msg.Content != prompt || len(msg.ContentParts) != 0 {
				t.Fatalf("invalid metadata survived or changed model content: %+v", msg)
			}
		})
	}
}

func TestFileSelectionContentPartsAcceptRangeBoundaries(t *testing.T) {
	for _, end := range []struct{ line, column int }{{3, 2}, {3, 8}, {4, 1}} {
		t.Run(fmt.Sprintf("%d:%d", end.line, end.column), func(t *testing.T) {
			part := fileSelectionPartForTest("quote")
			part.Source.EndLine, part.Source.EndColumn = end.line, end.column
			part.Source.Quote = " \t"
			parts := normalizeMessageContentParts([]providers.MessageContentPart{part})
			if len(parts) != 1 || !reflect.DeepEqual(parts[0], part) {
				t.Fatalf("valid range or whitespace selection lost: %+v", parts)
			}
		})
	}
}

func TestFileSelectionContentPartsHistoryRoundTrip(t *testing.T) {
	for _, rewrite := range []bool{false, true} {
		t.Run(fmt.Sprintf("rewrite=%t", rewrite), func(t *testing.T) {
			dir := t.TempDir()
			sess, err := session.CreateWithMetadata(dir, "selection-history", t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			parts := []providers.MessageContentPart{
				{Type: "text", Text: "Review:\n"}, fileSelectionPartForTest("comment"),
				{Type: "pasted_text", Text: "\nReference", Title: "Notes"},
			}
			prompt := parts[0].Text + parts[1].Text + parts[2].Text
			msg, err := userMessageFromPrompt(prompt, nil, nil, parts)
			if err != nil {
				t.Fatal(err)
			}
			if rewrite {
				err = rewriteChatHistory(dir, sess.ID, []providers.ChatMessage{msg})
			} else {
				_, err = appendChatMessage(dir, sess.ID, msg)
			}
			if err != nil {
				t.Fatal(err)
			}
			loaded, err := loadChatMessages(dir, sess.ID)
			if err != nil {
				t.Fatal(err)
			}
			if len(loaded) != 1 || loaded[0].Content != prompt || !reflect.DeepEqual(loaded[0].ContentParts, parts) {
				t.Fatalf("provider replay lost content or metadata: %+v", loaded)
			}
			records, err := loadPersistedMessages(dir, sess.ID, false)
			if err != nil || len(records) != 1 {
				t.Fatalf("load display history: records=%+v err=%v", records, err)
			}
			item := chatMessageItem("item-1", chatMessageFromPersistedMessage(records[0]))
			if item.Text != prompt || !reflect.DeepEqual(item.ContentParts, parts) {
				t.Fatalf("history item lost content or metadata: %+v", item)
			}
			prepared, err := providers.PrepareMessagesForModelRequest("gpt-5", loaded)
			if err != nil || len(prepared) != 1 || prepared[0].Content != prompt {
				t.Fatalf("model request changed canonical content: %+v, err=%v", prepared, err)
			}
		})
	}
}

func TestFileSelectionContentPartsQueueRoundTrip(t *testing.T) {
	dir := t.TempDir()
	sess, err := session.CreateWithMetadata(dir, "selection-queue", t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	part := fileSelectionPartForTest("edit")
	msg, err := userMessageFromPrompt(part.Text, nil, nil, []providers.MessageContentPart{part})
	if err != nil {
		t.Fatal(err)
	}
	msg.ClientID = "queue-1"
	srv := &Server{rt: &runtime.Session{SessionDir: dir}}
	srv.enqueueQueuedUserTurn(sess.ID, queuedTurn{id: msg.ClientID, msg: msg})
	entry, ok := srv.takeNextQueuedUserTurn(sess.ID)
	if !ok {
		t.Fatal("queued message missing")
	}
	if _, err := srv.appendHeldUserTurns(sess.ID, []queuedTurn{entry}); err != nil {
		t.Fatal(err)
	}
	restarted := &Server{rt: &runtime.Session{SessionDir: dir}}
	held, err := restarted.loadHeldUserTurns(sess.ID)
	if err != nil || len(held) != 1 {
		t.Fatalf("load held messages: %+v, err=%v", held, err)
	}
	if held[0].msg.Content != msg.Content || !reflect.DeepEqual(held[0].msg.ContentParts, msg.ContentParts) {
		t.Fatalf("held message lost content or metadata: %+v", held[0].msg)
	}
	// Exercise the protocol response as well as the stored Go representation.
	raw, err := json.Marshal(heldUserMessageSummary(sess.ID, held[0]))
	if err != nil {
		t.Fatal(err)
	}
	var summary HeldUserMessage
	if err := json.Unmarshal(raw, &summary); err != nil {
		t.Fatal(err)
	}
	if summary.Prompt != msg.Content || !reflect.DeepEqual(summary.ContentParts, msg.ContentParts) {
		t.Fatalf("queue response lost content or metadata: %+v", summary)
	}
	restarted.enqueueQueuedUserTurn(sess.ID, held[0])
	replayed, ok := restarted.takeNextQueuedUserTurn(sess.ID)
	if !ok || replayed.msg.Content != msg.Content || !reflect.DeepEqual(replayed.msg.ContentParts, msg.ContentParts) {
		t.Fatalf("replayed queue lost content or metadata: %+v", replayed)
	}
}
func TestServerContentPartsSurviveResumeAndFork(t *testing.T) {
	pasted := "  " + strings.Repeat("source line\n", 80)
	// Selection presentation must survive storage/replay while the canonical
	// text, including the user's comment, still reaches the provider.
	var selected []providers.MessageContentPart
	if err := json.Unmarshal([]byte(responseSelectionFixture), &selected); err != nil {
		t.Fatal(err)
	}
	assertContentPartsWire(t, selected, responseSelectionFixture)
	var rangeSelected []providers.MessageContentPart
	rangeWire := responseSelectionRangeWire(t)
	if err := json.Unmarshal([]byte(rangeWire), &rangeSelected); err != nil {
		t.Fatal(err)
	}
	assertContentPartsWire(t, rangeSelected, rangeWire)
	var oversized []providers.MessageContentPart
	oversizedWire := strings.Replace(strings.ReplaceAll(responseSelectionFixture, "Alpha 🌊", strings.Repeat("Alpha 🌊", 12000)), `"end_offset":12`, `"end_offset":96004`, 1)
	if err := json.Unmarshal([]byte(oversizedWire), &oversized); err != nil {
		t.Fatal(err)
	}
	parts := []providers.MessageContentPart{
		{Type: "pasted_text", Text: pasted, Title: "Source notes"},
		{Type: "text", Text: "Follow-up question.  "},
	}
	for _, tc := range []struct {
		name        string
		prompt      string
		input, want []providers.MessageContentPart
	}{
		{name: "pasted_and_text", prompt: pasted + "Follow-up question.  ", input: parts, want: parts},
		{name: "plain_text", prompt: "Ordinary question."},
		{name: "response_selection", prompt: selected[0].Text + selected[1].Text, input: selected, want: selected},
		{name: "response_selection_range_text", prompt: rangeSelected[0].Text + rangeSelected[1].Text, input: rangeSelected, want: rangeSelected},
		{name: "oversized_selection", prompt: oversized[0].Text + oversized[1].Text, input: oversized, want: oversized},
		{name: "mismatched_parts", prompt: "Canonical question.", input: parts},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &fakeClient{response: providersResponse("answer")}
			rt := newTestRuntime(t, client)
			out := &lockedBuffer{}
			srv := New(rt, out)
			t.Cleanup(srv.Close)
			rpc := func(id, method string, params any) any {
				t.Helper()
				raw, err := json.Marshal(map[string]any{"id": id, "method": method, "params": params})
				if err != nil {
					t.Fatal(err)
				}
				if err := srv.handleLine(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				response := responseByID(t, parseOutput(t, out.String()), id)
				if response["error"] != nil {
					t.Fatalf("%s: %+v", method, response["error"])
				}
				return response["result"]
			}
			prompt := strings.TrimSpace(tc.prompt)
			checkParts := func(stage string, parts []providers.MessageContentPart) {
				t.Helper()
				snapshots := providers.CloneMessageContentParts(parts)
				for index := range snapshots {
					part := &snapshots[index]
					if part.Type == "pasted_text" {
						data, err := os.ReadFile(part.LocalPath)
						if err != nil || string(data) != part.Text {
							t.Errorf("%s working file lost its exact snapshot: %v", stage, err)
						}
					}
					part.LocalPath = ""
				}
				if !reflect.DeepEqual(snapshots, tc.want) {
					t.Errorf("%s content snapshots = %#v, want %#v", stage, snapshots, tc.want)
				}
			}
			checkItem := func(stage string, turns []Turn) {
				t.Helper()
				for _, turn := range turns {
					for _, item := range turn.Items {
						if item.Type != ThreadItemUserMessage {
							continue
						}
						if item.RemoteContentRef != "" {
							refBytes, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(item.RemoteContentRef, "content:"))
							if err != nil {
								t.Fatal(err)
							}
							var ref []string
							if err := json.Unmarshal(refBytes, &ref); err != nil {
								t.Fatal(err)
							}
							var encoded strings.Builder
							for offset, total := 0, 1; offset < total; {
								result := remarshal[struct {
									Data  string
									Total int
								}](t, rpc(fmt.Sprintf("hydrate-%s-%d", stage, offset), "thread/content/read", map[string]any{"thread_id": ref[0], "turn_id": ref[1], "item_id": ref[2], "sha256": ref[3], "offset": offset}))
								if result.Data == "" {
									t.Fatal("missing content chunk")
								}
								encoded.WriteString(result.Data)
								offset += len(result.Data)
								total = result.Total
							}
							decoded, err := base64.StdEncoding.DecodeString(encoded.String())
							if err != nil {
								t.Fatal(err)
							}
							if err := json.Unmarshal(decoded, &item); err != nil {
								t.Fatal(err)
							}
							t.Logf("%s: hydrated full selection", stage)
						}
						if item.Text != prompt {
							t.Errorf("%s text = %q, want %q", stage, item.Text, prompt)
						}
						checkParts(stage, item.ContentParts)
						t.Logf("%s: body intact=%v, parts=%d", stage, item.Text == prompt, len(item.ContentParts))
						return
					}
				}
				t.Fatalf("%s: no user message", stage)
			}
			checkStored := func(stage, threadID string) {
				t.Helper()
				records, err := session.LoadHistoryRecords(rt.SessionDir, threadID, false)
				if err != nil {
					t.Fatal(err)
				}
				for _, record := range records {
					if record.Role != "user" {
						continue
					}
					var stored []providers.MessageContentPart
					if len(record.ContentParts) > 0 {
						if err := json.Unmarshal(record.ContentParts, &stored); err != nil {
							t.Fatal(err)
						}
					}
					display := record.DisplayContent
					if display == "" {
						display = record.Content
					}
					if display != prompt {
						t.Errorf("%s stored authored text = %q, want %q", stage, display, prompt)
					}
					checkParts(stage, stored)
					t.Logf("%s: body intact=%v, parts=%d", stage, display == prompt, len(stored))
					return
				}
				t.Fatalf("%s: no stored user message", stage)
			}
			started := remarshal[ThreadStartResult](t, rpc("start", MethodThreadStart, nil))
			threadID := started.Thread.ID
			rpc("turn", MethodTurnStart, TurnStartParams{ThreadID: threadID, Prompt: tc.prompt, ContentParts: tc.input})
			messages := waitForTurnCompletedForThread(t, out, threadID)
			completed := notificationsByMethod(messages, NotificationTurnCompleted)
			turn := remarshal[TurnCompletedNotification](t, completed[len(completed)-1]["params"]).Turn
			if turn.Status != TurnStatusCompleted {
				t.Fatalf("turn status = %s", turn.Status)
			}
			checkItem("live", []Turn{turn})
			client.mu.Lock()
			requests := append([]providers.ChatRequest(nil), client.requests...)
			client.mu.Unlock()
			found := false
			for _, request := range requests {
				for _, msg := range request.Messages {
					if msg.Role == "user" && msg.Content == prompt {
						found = true
					}
					if msg.Role == "user" && len(tc.want) > 0 && tc.want[0].Type == "pasted_text" {
						found = len(msg.ContentParts) == len(tc.want) &&
							!strings.Contains(msg.Content, strings.TrimSpace(pasted)) &&
							strings.Contains(msg.Content, msg.ContentParts[0].LocalPath) &&
							strings.Contains(msg.Content, strings.TrimSpace(tc.want[1].Text))
					}
				}
			}
			if !found {
				t.Fatal("provider did not receive instructions and the expected attachment representation")
			}
			t.Log("provider: instructions and attachment representation received")
			checkStored("SQLite", threadID)
			srv.Close()
			out = &lockedBuffer{}
			srv = New(rt, out)
			t.Cleanup(srv.Close)
			resumed := remarshal[ThreadResumeResult](t, rpc("resume", MethodThreadResume, ThreadResumeParams{SessionID: threadID, HistoryPage: true}))
			checkItem("resume", resumed.Thread.Turns)
			restoredTurn := resumed.Thread.Turns[0]
			lastItem := restoredTurn.Items[len(restoredTurn.Items)-1]
			fork := remarshal[ThreadForkResult](t, rpc("fork", MethodThreadFork, ThreadForkParams{
				ThreadID: threadID, TurnID: restoredTurn.ID, ItemID: lastItem.ID,
			}))
			checkItem("fork", fork.Thread.Turns)
			checkStored("fork SQLite", fork.Thread.ID)
			srv.Close()
			out = &lockedBuffer{}
			srv = New(rt, out)
			t.Cleanup(srv.Close)
			forkResume := remarshal[ThreadResumeResult](t, rpc("fork-resume", MethodThreadResume, ThreadResumeParams{SessionID: fork.Thread.ID}))
			checkItem("fork resume", forkResume.Thread.Turns)
		})
	}
}
