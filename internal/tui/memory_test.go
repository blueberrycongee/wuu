package tui

import (
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestAppendAndLoadMemoryEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	if err := appendMemoryEntry(path, transcriptEntry{Role: "USER", Content: "hello"}); err != nil {
		t.Fatalf("append user entry: %v", err)
	}
	if err := appendMemoryEntry(path, transcriptEntry{Role: "ASSISTANT", Content: "world"}); err != nil {
		t.Fatalf("append assistant entry: %v", err)
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("load entries: %v", err)
	}
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries, got %d", len(entries))
	}
	if entries[0].Role != "USER" || entries[0].Content != "hello" {
		t.Fatalf("unexpected first entry: %#v", entries[0])
	}
	if entries[1].Role != "ASSISTANT" || entries[1].Content != "world" {
		t.Fatalf("unexpected second entry: %#v", entries[1])
	}
}

func TestResumeSlashLoadsMemory(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	if err := appendMemoryEntry(path, transcriptEntry{Role: "USER", Content: "resume me"}); err != nil {
		t.Fatalf("seed memory: %v", err)
	}

	m := NewModel(Config{
		Provider:   "test",
		Model:      "test-model",
		ConfigPath: filepath.Join(dir, ".wuu.json"),
		MemoryPath: path,
		StreamRunner: &agent.StreamRunner{
			Client: &echoStreamClient{answer: func(_ []providers.ChatMessage) string { return "" }},
			Model:  "test-model",
		},
	})
	m.entries = nil

	msg, handled := m.handleSlash("/resume")
	if !handled {
		t.Fatal("expected /resume to be handled")
	}
	if msg == "" {
		t.Fatal("expected resume response message")
	}
	if len(m.entries) != 1 {
		t.Fatalf("expected one entry after resume, got %d", len(m.entries))
	}
}

func TestAppendAndLoadChatHistory_WithToolCalls(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")

	// Assistant message with tool calls.
	assistantMsg := providers.ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "get_weather", Arguments: `{"city":"Tokyo"}`},
			{ID: "call_2", Name: "get_time", Arguments: `{"tz":"Asia/Tokyo"}`},
		},
	}
	if err := appendChatMessage(path, assistantMsg); err != nil {
		t.Fatalf("append assistant msg: %v", err)
	}

	// Tool result message.
	toolMsg := providers.ChatMessage{
		Role:       "tool",
		Content:    `{"temp":22}`,
		ToolCallID: "call_1",
		Name:       "get_weather",
	}
	if err := appendChatMessage(path, toolMsg); err != nil {
		t.Fatalf("append tool msg: %v", err)
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("load chat history: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// Verify assistant message tool calls.
	got := msgs[0]
	if got.Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", got.Role)
	}
	if len(got.ToolCalls) != 2 {
		t.Fatalf("expected 2 tool calls, got %d", len(got.ToolCalls))
	}
	if got.ToolCalls[0].ID != "call_1" || got.ToolCalls[0].Name != "get_weather" {
		t.Fatalf("unexpected first tool call: %+v", got.ToolCalls[0])
	}
	if got.ToolCalls[0].Arguments != `{"city":"Tokyo"}` {
		t.Fatalf("unexpected arguments: %s", got.ToolCalls[0].Arguments)
	}
	if got.ToolCalls[1].ID != "call_2" || got.ToolCalls[1].Name != "get_time" {
		t.Fatalf("unexpected second tool call: %+v", got.ToolCalls[1])
	}

	// Verify tool result message.
	got2 := msgs[1]
	if got2.Role != "tool" {
		t.Fatalf("expected role tool, got %q", got2.Role)
	}
	if got2.ToolCallID != "call_1" {
		t.Fatalf("expected tool_call_id call_1, got %q", got2.ToolCallID)
	}
	if got2.Name != "get_weather" {
		t.Fatalf("expected name get_weather, got %q", got2.Name)
	}
	if got2.Content != `{"temp":22}` {
		t.Fatalf("unexpected content: %s", got2.Content)
	}
}

func TestAppendAndLoadChatHistory_WithReasoningContent(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "chat.jsonl")

	assistantMsg := providers.ChatMessage{
		Role:             "assistant",
		ReasoningContent: "inspect repo before tool use",
		ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "list_files", Arguments: `{}`},
		},
	}
	if err := appendChatMessage(path, assistantMsg); err != nil {
		t.Fatalf("append assistant msg: %v", err)
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("load chat history: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if msgs[0].ReasoningContent != "inspect repo before tool use" {
		t.Fatalf("unexpected reasoning content: %q", msgs[0].ReasoningContent)
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("load memory entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 entry, got %d", len(entries))
	}
	if entries[0].ThinkingContent != "inspect repo before tool use" || !entries[0].ThinkingDone {
		t.Fatalf("unexpected transcript thinking state: %#v", entries[0])
	}
}

func TestLoadMemoryEntries_ToolMerge(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	// Write assistant with tool_calls, then tool result, then assistant with content.
	assistantMsg := providers.ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []providers.ToolCall{
			{ID: "call_123", Name: "grep", Arguments: `{"pattern":"foo"}`},
		},
	}
	if err := appendChatMessage(path, assistantMsg); err != nil {
		t.Fatalf("append assistant: %v", err)
	}
	toolMsg := providers.ChatMessage{
		Role:       "tool",
		Content:    `{"matches":null,"total":0}`,
		ToolCallID: "call_123",
		Name:       "grep",
	}
	if err := appendChatMessage(path, toolMsg); err != nil {
		t.Fatalf("append tool: %v", err)
	}
	finalMsg := providers.ChatMessage{
		Role:    "assistant",
		Content: "No matches found.",
	}
	if err := appendChatMessage(path, finalMsg); err != nil {
		t.Fatalf("append final assistant: %v", err)
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("load entries: %v", err)
	}

	// Should have 2 entries: assistant (with merged tool call) + assistant (with content).
	// Tool entry should NOT appear as a separate entry.
	if len(entries) != 2 {
		t.Fatalf("expected 2 entries (tool merged), got %d", len(entries))
	}

	// First assistant should have the tool call with result merged in.
	if len(entries[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 tool call, got %d", len(entries[0].ToolCalls))
	}
	tc := entries[0].ToolCalls[0]
	if tc.Name != "grep" {
		t.Fatalf("expected tool name 'grep', got %q", tc.Name)
	}
	if tc.Args != `{"pattern":"foo"}` {
		t.Fatalf("expected args, got %q", tc.Args)
	}
	if tc.Result != `{"matches":null,"total":0}` {
		t.Fatalf("expected result, got %q", tc.Result)
	}
	if tc.Status != ToolCallDone {
		t.Fatalf("expected status done, got %q", tc.Status)
	}

	// Second assistant should have content.
	if entries[1].Content != "No matches found." {
		t.Fatalf("expected content, got %q", entries[1].Content)
	}

	// No entry should have role TOOL.
	for _, e := range entries {
		if e.Role == "TOOL" {
			t.Fatal("TOOL entry should not exist as separate entry")
		}
	}
}

func TestLoadChatHistory_BackwardCompatible(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "compat.jsonl")

	// Write old-format entries via appendMemoryEntry.
	if err := appendMemoryEntry(path, transcriptEntry{Role: "USER", Content: "hello"}); err != nil {
		t.Fatalf("append old user: %v", err)
	}
	if err := appendMemoryEntry(path, transcriptEntry{Role: "ASSISTANT", Content: "hi there"}); err != nil {
		t.Fatalf("append old assistant: %v", err)
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("load chat history: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 messages, got %d", len(msgs))
	}

	// Roles should be lowercase.
	if msgs[0].Role != "user" {
		t.Fatalf("expected role user, got %q", msgs[0].Role)
	}
	if msgs[0].Content != "hello" {
		t.Fatalf("unexpected content: %q", msgs[0].Content)
	}
	if msgs[1].Role != "assistant" {
		t.Fatalf("expected role assistant, got %q", msgs[1].Role)
	}
	if msgs[1].Content != "hi there" {
		t.Fatalf("unexpected content: %q", msgs[1].Content)
	}

	// No tool call fields should be set.
	if len(msgs[0].ToolCalls) != 0 {
		t.Fatalf("expected no tool calls, got %d", len(msgs[0].ToolCalls))
	}
	if msgs[0].ToolCallID != "" {
		t.Fatalf("expected empty tool_call_id, got %q", msgs[0].ToolCallID)
	}
}

func TestLoadChatHistory_SkipsNonChatRoles(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	if err := appendMemoryEntry(path, transcriptEntry{Role: "SYSTEM", Content: "ui-only notice"}); err != nil {
		t.Fatalf("append system entry: %v", err)
	}
	if err := appendMemoryEntry(path, transcriptEntry{Role: "META", Content: "token_usage"}); err != nil {
		t.Fatalf("append meta entry: %v", err)
	}
	userMsg := providers.ChatMessage{Role: "user", Content: "hello"}
	if err := appendChatMessage(path, userMsg); err != nil {
		t.Fatalf("append user msg: %v", err)
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("load chat history: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected only chat messages, got %d", len(msgs))
	}
	if msgs[0].Role != "user" || msgs[0].Content != "hello" {
		t.Fatalf("unexpected restored chat message: %#v", msgs[0])
	}
}

func TestLoadChatHistory_IncludesConversationSummarySystemMessage(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	summary := providers.ChatMessage{
		Role:    "system",
		Content: "[Conversation summary]\nOlder turns were compacted.",
	}
	if err := appendChatMessage(path, summary); err != nil {
		t.Fatalf("append summary msg: %v", err)
	}
	if err := appendChatMessage(path, providers.ChatMessage{Role: "user", Content: "hello"}); err != nil {
		t.Fatalf("append user msg: %v", err)
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("load chat history: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected summary + user, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[0].Content != summary.Content {
		t.Fatalf("unexpected summary message: %#v", msgs[0])
	}
}

func TestRewriteChatHistory_PreservesMetaEntries(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	if err := appendChatMessage(path, providers.ChatMessage{Role: "user", Content: "old"}); err != nil {
		t.Fatalf("append old msg: %v", err)
	}
	if err := appendTokenUsage(path, 10, 5); err != nil {
		t.Fatalf("append token usage: %v", err)
	}

	replacement := []providers.ChatMessage{
		{Role: "system", Content: "[Conversation summary]\nOlder turns were compacted."},
		{Role: "user", Content: "new prompt"},
		{Role: "assistant", Content: "new answer"},
	}
	if err := rewriteChatHistory(path, replacement); err != nil {
		t.Fatalf("rewrite chat history: %v", err)
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("load rewritten history: %v", err)
	}
	if len(msgs) != 3 {
		t.Fatalf("expected 3 rewritten chat messages, got %d", len(msgs))
	}
	if msgs[0].Role != "system" || msgs[2].Content != "new answer" {
		t.Fatalf("unexpected rewritten history: %#v", msgs)
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("load memory entries: %v", err)
	}
	foundMeta := false
	for _, entry := range entries {
		if entry.Role == "META" {
			foundMeta = true
		}
	}
	if foundMeta {
		t.Fatal("meta entries should not be materialized into transcript entries")
	}

	meta, err := loadMetaEntries(path)
	if err != nil {
		t.Fatalf("load meta entries: %v", err)
	}
	if len(meta) != 1 || meta[0].Content != "token_usage" {
		t.Fatalf("expected preserved token usage meta, got %#v", meta)
	}
}

func TestLoadMemoryEntries_SkipsMetaRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	if err := appendMemoryEntry(path, transcriptEntry{Role: "META", Content: "token_usage"}); err != nil {
		t.Fatalf("append meta entry: %v", err)
	}
	if err := appendMemoryEntry(path, transcriptEntry{Role: "SYSTEM", Content: "ready"}); err != nil {
		t.Fatalf("append system entry: %v", err)
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("load entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected meta to be skipped, got %d entries", len(entries))
	}
	if entries[0].Role != "SYSTEM" || entries[0].Content != "ready" {
		t.Fatalf("unexpected restored entry: %#v", entries[0])
	}
}

func TestChatHistory_WithUserImages(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "images.jsonl")

	userMsg := providers.ChatMessage{
		Role:    "user",
		Content: "check this",
		Images: []providers.InputImage{
			{MediaType: "image/png", Data: "AAA"},
			{MediaType: "image/jpeg", Data: "BBB"},
		},
	}
	if err := appendChatMessage(path, userMsg); err != nil {
		t.Fatalf("append user msg: %v", err)
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("load chat history: %v", err)
	}
	if len(msgs) != 1 {
		t.Fatalf("expected 1 message, got %d", len(msgs))
	}
	if len(msgs[0].Images) != 2 {
		t.Fatalf("expected 2 images, got %d", len(msgs[0].Images))
	}
	if msgs[0].Images[0].MediaType != "image/png" || msgs[0].Images[0].Data != "AAA" {
		t.Fatalf("unexpected first image: %+v", msgs[0].Images[0])
	}
	if msgs[0].Images[1].MediaType != "image/jpeg" || msgs[0].Images[1].Data != "BBB" {
		t.Fatalf("unexpected second image: %+v", msgs[0].Images[1])
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("load memory entries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 transcript entry, got %d", len(entries))
	}
	if entries[0].Content != "check this\n[Image #1]\n[Image #2]" {
		t.Fatalf("unexpected transcript content: %q", entries[0].Content)
	}
}

func TestLoadTokenUsageTotals(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	if err := appendTokenUsage(path, 10, 5); err != nil {
		t.Fatalf("append first token usage: %v", err)
	}
	if err := appendTokenUsage(path, 7, 3); err != nil {
		t.Fatalf("append second token usage: %v", err)
	}
	if err := appendMemoryEntry(path, transcriptEntry{Role: "SYSTEM", Content: "note"}); err != nil {
		t.Fatalf("append system entry: %v", err)
	}

	inputTokens, outputTokens, err := loadTokenUsageTotals(path)
	if err != nil {
		t.Fatalf("loadTokenUsageTotals: %v", err)
	}
	if inputTokens != 17 || outputTokens != 8 {
		t.Fatalf("unexpected token totals: in=%d out=%d", inputTokens, outputTokens)
	}
}

func TestAppendTokenUsage_PersistsPerTurnDeltasAcrossTurns(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")

	turns := []struct {
		input  int
		output int
	}{
		{input: 10, output: 5},
		{input: 7, output: 3},
		{input: 4, output: 2},
	}

	for _, turn := range turns {
		if err := appendTokenUsage(path, turn.input, turn.output); err != nil {
			t.Fatalf("append token usage: %v", err)
		}
	}

	meta, err := loadMetaEntries(path)
	if err != nil {
		t.Fatalf("loadMetaEntries: %v", err)
	}
	if len(meta) != len(turns) {
		t.Fatalf("expected %d meta entries, got %d", len(turns), len(meta))
	}
	for i, rec := range meta {
		if rec.Content != "token_usage" {
			t.Fatalf("expected token_usage meta content, got %#v", rec)
		}
		if rec.InputTokens != turns[i].input || rec.OutputTokens != turns[i].output {
			t.Fatalf("expected turn %d delta in=%d out=%d, got in=%d out=%d", i, turns[i].input, turns[i].output, rec.InputTokens, rec.OutputTokens)
		}
	}
}

func TestSessionReaders_HandleLargeJSONLRecords(t *testing.T) {
	dir := t.TempDir()
	path := filepath.Join(dir, "session.jsonl")
	largeResult := strings.Repeat("x", 2100*1024)

	assistantMsg := providers.ChatMessage{
		Role:    "assistant",
		Content: "",
		ToolCalls: []providers.ToolCall{
			{ID: "call_big", Name: "grep", Arguments: `{"pattern":"message_start"}`},
		},
	}
	if err := appendChatMessage(path, assistantMsg); err != nil {
		t.Fatalf("append assistant msg: %v", err)
	}
	toolMsg := providers.ChatMessage{
		Role:       "tool",
		Content:    largeResult,
		ToolCallID: "call_big",
		Name:       "grep",
	}
	if err := appendChatMessage(path, toolMsg); err != nil {
		t.Fatalf("append tool msg: %v", err)
	}
	if err := appendTokenUsage(path, 10, 5); err != nil {
		t.Fatalf("append token usage: %v", err)
	}

	entries, err := loadMemoryEntries(path)
	if err != nil {
		t.Fatalf("loadMemoryEntries: %v", err)
	}
	if len(entries) != 1 {
		t.Fatalf("expected 1 transcript entry, got %d", len(entries))
	}
	if len(entries[0].ToolCalls) != 1 {
		t.Fatalf("expected 1 merged tool call, got %d", len(entries[0].ToolCalls))
	}
	if entries[0].ToolCalls[0].Result != largeResult {
		t.Fatalf("unexpected merged tool result size: got %d want %d", len(entries[0].ToolCalls[0].Result), len(largeResult))
	}

	msgs, err := loadChatHistory(path)
	if err != nil {
		t.Fatalf("loadChatHistory: %v", err)
	}
	if len(msgs) != 2 {
		t.Fatalf("expected 2 chat messages, got %d", len(msgs))
	}
	if msgs[1].Role != "tool" || msgs[1].Content != largeResult {
		t.Fatalf("unexpected restored tool message: role=%q len=%d", msgs[1].Role, len(msgs[1].Content))
	}

	replacement := []providers.ChatMessage{
		{Role: "user", Content: "after compact"},
		{Role: "assistant", Content: "still here"},
	}
	if err := rewriteChatHistory(path, replacement); err != nil {
		t.Fatalf("rewriteChatHistory: %v", err)
	}

	meta, err := loadMetaEntries(path)
	if err != nil {
		t.Fatalf("loadMetaEntries: %v", err)
	}
	if len(meta) != 1 || meta[0].Content != "token_usage" {
		t.Fatalf("expected preserved token usage meta, got %#v", meta)
	}
}
