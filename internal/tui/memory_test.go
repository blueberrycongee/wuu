package tui

import (
	"context"
	"path/filepath"
	"testing"

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
		RunPrompt: func(_ctx context.Context, _prompt string) (string, error) {
			return "", nil
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
