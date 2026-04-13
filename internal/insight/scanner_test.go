package insight

import (
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"
)

func TestScanSessionsAndFormatTranscriptHandleLargeToolRecords(t *testing.T) {
	dir := t.TempDir()
	sessionID := "20260413-101416-cd82"
	path := filepath.Join(dir, sessionID+".jsonl")
	largeToolResult := strings.Repeat("x", 2100*1024)
	start := time.Date(2026, time.April, 13, 10, 14, 16, 0, time.UTC)

	writeInsightSessionRecords(t, path, []memoryRecord{
		{Role: "user", Content: "restore this session", At: start},
		{
			Role:    "assistant",
			Content: "",
			At:      start.Add(10 * time.Second),
			ToolCalls: []toolCallRec{
				{ID: "call_big", Name: "write_file", Arguments: `{"file_path":"main.go"}`},
			},
		},
		{
			Role:       "tool",
			Content:    largeToolResult,
			ToolCallID: "call_big",
			Name:       "write_file",
			At:         start.Add(20 * time.Second),
		},
		{Role: "user", Content: "and keep it visible", At: start.Add(2 * time.Minute)},
		{Role: "assistant", Content: "done", At: start.Add(3 * time.Minute)},
	})

	metas, err := ScanSessions(dir, 0)
	if err != nil {
		t.Fatalf("ScanSessions: %v", err)
	}
	if len(metas) != 1 {
		t.Fatalf("expected 1 session meta, got %d", len(metas))
	}
	meta := metas[0]
	if meta.ID != sessionID {
		t.Fatalf("unexpected session id: %q", meta.ID)
	}
	if meta.UserMessages != 2 {
		t.Fatalf("expected 2 user messages, got %d", meta.UserMessages)
	}
	if meta.AssistantMsgs != 2 {
		t.Fatalf("expected 2 assistant messages, got %d", meta.AssistantMsgs)
	}
	if meta.ToolCounts["write_file"] != 1 {
		t.Fatalf("expected write_file count 1, got %d", meta.ToolCounts["write_file"])
	}
	if meta.FilesModified != 1 {
		t.Fatalf("expected 1 modified file, got %d", meta.FilesModified)
	}

	transcript, err := FormatTranscript(dir, sessionID)
	if err != nil {
		t.Fatalf("FormatTranscript: %v", err)
	}
	if !strings.Contains(transcript, "[User]: restore this session") {
		t.Fatalf("expected first user message in transcript, got %q", transcript)
	}
	if !strings.Contains(transcript, "[User]: and keep it visible") {
		t.Fatalf("expected follow-up user message in transcript, got %q", transcript)
	}
	if !strings.Contains(transcript, "[Tool: write_file]") {
		t.Fatalf("expected tool call marker in transcript, got %q", transcript)
	}
	if strings.Contains(transcript, largeToolResult[:256]) {
		t.Fatal("expected large tool payload to stay out of transcript output")
	}
}

func writeInsightSessionRecords(t *testing.T, path string, records []memoryRecord) {
	t.Helper()

	file, err := os.Create(path)
	if err != nil {
		t.Fatalf("create session file: %v", err)
	}
	defer file.Close()

	enc := json.NewEncoder(file)
	for _, rec := range records {
		if err := enc.Encode(rec); err != nil {
			t.Fatalf("encode session record: %v", err)
		}
	}
}
