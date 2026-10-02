package appserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	sessionstore "github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

func TestChatHistoryRoundTripKeepsRichToolResult(t *testing.T) {
	sessDir := t.TempDir()
	sess, err := sessionstore.CreateWithMetadata(sessDir, "rich-tool-result", t.TempDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	detail := toolresult.Result{
		Content: []toolresult.ContentPart{
			{Type: toolresult.ContentTypeText, Text: "screenshot captured"},
			{Type: toolresult.ContentTypeImage, Data: "aW1hZ2U=", MIMEType: "image/png", Name: "screen.png"},
		},
		StructuredContent: json.RawMessage(`{"caption":"result"}`),
		Meta:              json.RawMessage(`{"source":"mcp"}`),
	}
	history := []providers.ChatMessage{
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "call-rich", Name: "mcp_rich"}}},
		{Role: "tool", Name: "mcp_rich", ToolCallID: "call-rich", Content: detail.TextProjection(), ToolResult: &detail},
	}
	if err := rewriteChatHistory(sessDir, sess.ID, history); err != nil {
		t.Fatalf("rewrite history: %v", err)
	}
	loaded, err := loadChatMessages(sessDir, sess.ID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(loaded) != 2 || loaded[1].ToolResult == nil || !reflect.DeepEqual(*loaded[1].ToolResult, detail) {
		t.Fatalf("rich tool result did not survive restart: %+v", loaded)
	}
	prepared, err := providers.PrepareMessagesForModelRequest("gpt-5", loaded)
	if err != nil {
		t.Fatalf("prepare resumed history: %v", err)
	}
	if len(prepared) != 3 || len(prepared[2].Images) != 1 || prepared[2].Images[0].Data != "aW1hZ2U=" {
		t.Fatalf("resumed history lost native media observation: %+v", prepared)
	}
}

func TestRewriteChatHistoryKeepsCompactSummary(t *testing.T) {
	sessDir := t.TempDir()
	sess, err := sessionstore.CreateWithMetadata(sessDir, "compact-summary", t.TempDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	history := []providers.ChatMessage{
		{Role: "system", Content: compact.BuildSummaryContent("Recovered task state")},
		{Role: "assistant", Content: "continued"},
	}
	if err := rewriteChatHistory(sessDir, sess.ID, history); err != nil {
		t.Fatalf("rewrite history: %v", err)
	}
	loaded, err := loadChatMessages(sessDir, sess.ID)
	if err != nil {
		t.Fatalf("load history: %v", err)
	}
	if len(loaded) != 2 {
		t.Fatalf("expected summary and assistant to persist, got %+v", loaded)
	}
	if loaded[0].Role != "system" || !compact.IsConversationSummaryContent(loaded[0].Content) || !strings.Contains(loaded[0].Content, "Recovered task state") {
		t.Fatalf("expected persisted summary system message, got %+v", loaded[0])
	}
}

func TestReplaceBaseSystemPromptPreservesDurableContext(t *testing.T) {
	for _, boundary := range []providers.ChatMessage{
		{Role: "system", Content: compact.BuildSummaryContent("saved state"), Hidden: true},
		{Role: "system", Content: "synthetic recovery address", Hidden: true, Origin: "internal", Cause: "fresh_context", Seq: 42},
	} {
		history := []providers.ChatMessage{boundary, {Role: "user", Content: "continue"}}
		original := cloneHistory(history)
		withBase := replaceBaseSystemPrompt(history, "old runtime prompt")
		updated := replaceBaseSystemPrompt(withBase, "current runtime prompt")
		want := append([]providers.ChatMessage{{Role: "system", Content: "current runtime prompt"}}, original...)
		if !reflect.DeepEqual(updated, want) || !reflect.DeepEqual(history, original) {
			t.Fatalf("runtime prompt replacement altered durable context: got %+v, original %+v", updated, history)
		}
	}
}

func TestPersistFreshContextKeepsReleasedOriginalsAddressable(t *testing.T) {
	sessDir := t.TempDir()
	sess, err := sessionstore.CreateWithMetadata(sessDir, "fresh-context-history", t.TempDir())
	if err != nil {
		t.Fatalf("create session: %v", err)
	}
	if _, err := appendChatMessage(sessDir, sess.ID, providers.ChatMessage{Role: "user", Content: "original request"}); err != nil {
		t.Fatalf("append user: %v", err)
	}
	_, archivedHead, err := appendChatMessagesReturningRange(sessDir, sess.ID, []providers.ChatMessage{
		{Role: "assistant", Content: "pre-switch reasoning"},
		{Role: "assistant", ToolCalls: []providers.ToolCall{{ID: "switch", Name: "new_context", Arguments: `{}`}}},
		{Role: "tool", ToolCallID: "switch", Name: "new_context", Content: `{"requested":true}`},
	})
	if err != nil {
		t.Fatalf("archive pre-switch messages: %v", err)
	}
	replacement := []providers.ChatMessage{
		{Role: "system", Content: compact.BuildSummaryContent("continue from note"), Hidden: true},
		{Role: "assistant", Content: "post-switch answer"},
	}
	server := &Server{rt: &runtime.Session{SessionDir: sessDir}}
	thread := &threadState{ID: sess.ID, PersistHistory: true, History: cloneHistory(replacement)}
	result := agent.LoopResult{
		NewMessages:            cloneHistory(replacement),
		DurableNewMessages:     []providers.ChatMessage{{Role: "assistant", Content: "post-switch answer"}},
		DurableMessagesTracked: true,
		HistoryArchiveHeadSeq:  archivedHead,
		HistoryRewritten:       true,
	}
	if err := server.persistTurnResultLocked(thread, result, true, "", "", 1); err != nil {
		t.Fatalf("persist fresh context: %v", err)
	}

	originals, err := sessionstore.LoadHistoryRecords(sessDir, sess.ID, false)
	if err != nil {
		t.Fatalf("load physical transcript: %v", err)
	}
	postSwitchAnswers := 0
	for _, record := range originals {
		if record.Content == "post-switch answer" {
			postSwitchAnswers++
		}
	}
	if len(originals) != 6 || originals[1].Content != "pre-switch reasoning" || originals[4].Content != "post-switch answer" || postSwitchAnswers != 1 {
		t.Fatalf("physical transcript = %+v", originals)
	}
	active, err := loadChatMessages(sessDir, sess.ID)
	if err != nil {
		t.Fatalf("load active history: %v", err)
	}
	if len(active) != 2 || !compact.IsConversationSummaryContent(active[0].Content) || active[1].Content != "post-switch answer" {
		t.Fatalf("active history = %+v", active)
	}
}

// Loading must retain usage from the active physical transcript even when a
// provider checkpoint omits it, and must not repair storage owned by a live turn.
func TestPersistedThreadLoadPreservesActiveTranscript(t *testing.T) {
	for _, scenario := range []string{"plain", "checkpoint", "empty_checkpoint", "edited_branch", "execution_owned"} {
		t.Run(scenario, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			const id = "resume-fork"
			if _, err := sessionstore.CreateForkWithMetadata(rt.SessionDir, id, rt.RootDir, sessionstore.ForkMetadata{
				ForkedFromID: "source-thread", ForkedFromTurnID: "source-turn", ForkedFromItemID: "source-item",
			}); err != nil {
				t.Fatal(err)
			}
			if _, err := sessionstore.SetRuntimeSelection(rt.SessionDir, id, sessionstore.RuntimeSelection{
				Provider: "pinned-provider", Model: "pinned-model", Variant: "high", PermissionMode: "read_only",
			}); err != nil {
				t.Fatal(err)
			}
			at := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			records := []sessionstore.HistoryRecord{
				{Role: "user", Content: "first prompt", At: at},
				{Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"call-1","name":"read_file","arguments":"{}"}]`), At: at},
				{Role: "tool", ToolCallID: "call-1", Name: "read_file", Content: "rich result", ToolResult: json.RawMessage(`{"content":[{"type":"text","text":"rich result"},{"type":"image","data":"aW1hZ2U=","mime_type":"image/png"}]}`), At: at},
				{Role: "assistant", Content: "first answer", Phase: string(providers.MessagePhaseFinalAnswer), At: at},
				{Role: "meta", Content: "token_usage", InputTokens: 12, OutputTokens: 3, ContextTokens: 18, Model: "first-usage-model", At: at},
				{Role: "meta", Content: "turn_terminal", ClientID: id + "-turn-0001", StopReason: "completed", At: at},
				{Role: "user", Content: "second prompt", At: at.Add(time.Second)},
				{Role: "assistant", Content: "second answer", Phase: string(providers.MessagePhaseFinalAnswer), At: at.Add(time.Second)},
				{Role: "meta", Content: "token_usage", InputTokens: 22, OutputTokens: 4, ContextTokens: 30, Model: "second-usage-model", At: at.Add(time.Second)},
				{Role: "meta", Content: "turn_terminal", ClientID: id + "-turn-0002", StopReason: "completed", At: at.Add(time.Second)},
			}
			if err := sessionstore.AppendHistoryRecords(rt.SessionDir, id, records); err != nil {
				t.Fatal(err)
			}
			raw, err := sessionstore.LoadHistoryRecords(rt.SessionDir, id, true)
			if err != nil {
				t.Fatal(err)
			}
			switch scenario {
			case "checkpoint":
				// Keeping seq 1 makes the display projection use this replacement
				// directly. Its missing usage still belongs to the durable turns.
				if err := sessionstore.RewriteHistoryRecordsAtBaseline(rt.SessionDir, id, []sessionstore.HistoryRecord{raw[0], raw[3], raw[6], raw[7]}, 10); err != nil {
					t.Fatal(err)
				}
			case "empty_checkpoint":
				if err := sessionstore.RewriteHistoryRecordsAtBaseline(rt.SessionDir, id, nil, 10); err != nil {
					t.Fatal(err)
				}
			case "edited_branch":
				if err := sessionstore.RewriteHistoryRecordsForEdit(rt.SessionDir, id, raw[:6], 7, 10); err != nil {
					t.Fatal(err)
				}
			case "execution_owned":
				if err := sessionstore.AppendHistoryRecord(rt.SessionDir, id, sessionstore.HistoryRecord{
					Role: "assistant", ToolCalls: json.RawMessage(`[{"id":"unfinished","name":"read_file","arguments":"{}"}]`), At: at,
				}); err != nil {
					t.Fatal(err)
				}
				lease, acquired, err := sessionstore.TryAcquireThreadExecutionLease(rt.SessionDir, id)
				if err != nil || !acquired {
					t.Fatalf("acquire execution ownership: acquired=%v err=%v", acquired, err)
				}
				t.Cleanup(func() { _ = lease.Release() })
			}
			before, err := sessionstore.LoadHistoryRecords(rt.SessionDir, id, true)
			if err != nil {
				t.Fatal(err)
			}
			providerBefore, err := sessionstore.LoadProviderHistorySnapshot(rt.SessionDir, id)
			if err != nil {
				t.Fatal(err)
			}
			srv := New(rt, &lockedBuffer{})
			t.Cleanup(srv.Close)
			loaded, err := srv.loadPersistedThreadSnapshot(id)
			if err != nil {
				t.Fatal(err)
			}
			metas, err := loadMetaMessages(rt.SessionDir, id)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(loaded.tokenMetas, metas) {
				t.Fatal("loaded usage differs from the active physical transcript")
			}
			// Reconstruct the prior load path independently of its optimized
			// constructor and meta selection, then compare the complete state.
			want := newThreadState(id, loaded.history, rt.ProviderName, rt.Model, firstNonEmpty(loaded.metadata.CWD, rt.RootDir), true, at)
			want.historyHeadSeq = loaded.baselineSeq
			want.Turns = turnsFromPersistedHistory(id, loaded.displayHistory, at, srv.resolveParticipantSummary)
			srv.restorePluginToolLabels(want.Turns)
			want.Turns = applyTokenUsageMetasToTurns(want.Turns, metas)
			want.WorkspaceKind = workspaceKindForCWD(rt.WuuHome, want.CWD)
			applySessionMetadata(want, loaded.metadata)
			want.SessionControl, err = srv.readThreadSessionControl(id)
			if err != nil {
				t.Fatal(err)
			}
			got, err := srv.loadPersistedThreadState(id, at)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(got, want) {
				t.Fatalf("loaded state changed: got=%+v want=%+v", got.snapshotLocked(), want.snapshotLocked())
			}
			gotJSON, _ := json.Marshal(got.snapshotLocked())
			wantJSON, _ := json.Marshal(want.snapshotLocked())
			if string(gotJSON) != string(wantJSON) {
				t.Fatal("full renderer snapshot changed")
			}
			if len(got.Turns) > 0 && len(got.Turns[0].Items) > 0 {
				view := got.snapshotLocked()
				view.Turns[0].Items[0].Text = "changed by reader"
				if got.Turns[0].Items[0].Text == "changed by reader" {
					t.Fatal("snapshot aliases retained turn items")
				}
			}
			after, err := sessionstore.LoadHistoryRecords(rt.SessionDir, id, true)
			if err != nil || !reflect.DeepEqual(after, before) {
				t.Fatalf("loading modified physical history: %v", err)
			}
			providerAfter, err := sessionstore.LoadProviderHistorySnapshot(rt.SessionDir, id)
			if err != nil || !reflect.DeepEqual(providerAfter, providerBefore) {
				t.Fatalf("loading modified the provider checkpoint: %v", err)
			}
		})
	}
}
