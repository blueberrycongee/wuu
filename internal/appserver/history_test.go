package appserver

import (
	"encoding/json"
	"reflect"
	"strings"
	"testing"

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
