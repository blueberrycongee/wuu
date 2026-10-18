package session

import (
	"context"
	"errors"
	"fmt"
	"testing"
	"time"
)

func TestHistoryRecoveryReadsAndSearchesPhysicalTranscript(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "thread-history-recovery", t.TempDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	records := []HistoryRecord{
		{Role: "user", Content: "first request"},
		{Role: "assistant", Content: "checking source", ToolCalls: []byte(`[{"name":"grep","arguments":"{\"pattern\":\"needle\"}"}]`)},
		{Role: "tool", Content: "", ToolResult: []byte(`{"content":[{"type":"text","text":"exact tool fact"}]}`)},
		{Role: "meta", Content: "token_usage"},
		{Role: "assistant", Content: "finished"},
	}
	start, end, err := AppendHistoryRecordsReturningRange(dir, "thread-history-recovery", records)
	if err != nil {
		t.Fatalf("append records: %v", err)
	}
	if start != 1 || end != 5 {
		t.Fatalf("range = %d..%d, want 1..5", start, end)
	}

	page, err := ReadHistoryPage(context.Background(), dir, "thread-history-recovery", 2, 2)
	if err != nil {
		t.Fatalf("read history: %v", err)
	}
	if page.HeadSeq != 5 || page.SnapshotSeq != 5 || !page.HasMore || len(page.Records) != 2 {
		t.Fatalf("page = %+v", page)
	}
	if page.Records[0].Seq != 2 || page.Records[1].Seq != 3 {
		t.Fatalf("read seqs = %d, %d", page.Records[0].Seq, page.Records[1].Seq)
	}

	search, err := SearchHistoryPage(context.Background(), dir, "thread-history-recovery", "exact tool fact", 0, 10)
	if err != nil {
		t.Fatalf("search tool result: %v", err)
	}
	if len(search.Records) != 1 || search.Records[0].Seq != 3 {
		t.Fatalf("search = %+v", search)
	}
	search, err = SearchHistoryPage(context.Background(), dir, "thread-history-recovery", "needle", 0, 10)
	if err != nil {
		t.Fatalf("search tool arguments: %v", err)
	}
	if len(search.Records) != 1 || search.Records[0].Seq != 2 {
		t.Fatalf("tool argument search = %+v", search)
	}
}

func TestHistoryReadSnapshotIgnoresLaterAppends(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "source", t.TempDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := AppendHistoryRecords(dir, "source", []HistoryRecord{
		{Role: "user", Content: "first"},
		{Role: "assistant", Content: "second"},
	}); err != nil {
		t.Fatalf("append first page: %v", err)
	}
	page, err := ReadHistoryQuery(context.Background(), dir, HistoryReadQuery{SessionID: "source", StartSeq: 1, Limit: 1})
	if err != nil {
		t.Fatalf("read first page: %v", err)
	}
	if page.SnapshotSeq != 2 || page.Next == nil || page.Next.Seq != 2 {
		t.Fatalf("first page = %+v", page)
	}
	if err := AppendHistoryRecord(dir, "source", HistoryRecord{Role: "user", Content: "later append"}); err != nil {
		t.Fatalf("append later: %v", err)
	}
	continued, err := ReadHistoryQuery(context.Background(), dir, HistoryReadQuery{
		SessionID: "source", StartSeq: page.Next.Seq, SnapshotSeq: page.SnapshotSeq, Limit: 10, Cursor: page.Next,
	})
	if err != nil {
		t.Fatalf("continue snapshot: %v", err)
	}
	if continued.SnapshotSeq != 2 || len(continued.Records) != 1 || continued.Records[0].Content != "second" {
		t.Fatalf("continued = %+v", continued)
	}
}

func TestHistorySearchSnapshotDoesNotSeeLaterMatches(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "source", t.TempDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := AppendHistoryRecord(dir, "source", HistoryRecord{Role: "user", Content: "alpha one"}); err != nil {
		t.Fatalf("append first: %v", err)
	}
	page, err := SearchHistoryQuery(context.Background(), dir, HistorySearchQuery{SessionID: "source", Query: "alpha", Limit: 10})
	if err != nil {
		t.Fatalf("search first: %v", err)
	}
	if page.SnapshotSeq != 1 || len(page.Records) != 1 {
		t.Fatalf("first search = %+v", page)
	}
	if err := AppendHistoryRecord(dir, "source", HistoryRecord{Role: "user", Content: "alpha two"}); err != nil {
		t.Fatalf("append later: %v", err)
	}
	frozen, err := SearchHistoryQuery(context.Background(), dir, HistorySearchQuery{
		SessionID: "source", Query: "alpha", SnapshotSeq: page.SnapshotSeq, Limit: 10,
	})
	if err != nil {
		t.Fatalf("search frozen: %v", err)
	}
	if frozen.SnapshotSeq != 1 || len(frozen.Records) != 1 || frozen.Records[0].Content != "alpha one" {
		t.Fatalf("frozen search = %+v", frozen)
	}
}

func TestHistoryCursorRoundTrip(t *testing.T) {
	cursor := HistoryCursor{SessionID: "source", SnapshotSeq: 9, Seq: 4, Field: HistoryFieldToolResult, Offset: 12}
	decoded, err := DecodeHistoryCursor(EncodeHistoryCursor(cursor))
	if err != nil {
		t.Fatalf("decode: %v", err)
	}
	if decoded != cursor {
		t.Fatalf("decoded = %+v, want %+v", decoded, cursor)
	}
}

func TestHistoryReadMissingSessionIsUnavailable(t *testing.T) {
	_, err := ReadHistoryQuery(context.Background(), t.TempDir(), HistoryReadQuery{SessionID: "missing", StartSeq: 1, Limit: 10})
	if !errors.Is(err, ErrSessionNotFound) {
		t.Fatalf("error = %v, want session not found", err)
	}
}

func TestUTF8SafeSliceDoesNotSplitRunes(t *testing.T) {
	part, next, clipped := utf8SafeSlice("汉字测试", 1, 2)
	if part != "字测" || next != 3 || !clipped {
		t.Fatalf("slice = %q %d %v", part, next, clipped)
	}
}

func TestHistorySearchRespectsExplicitTimeWindowAndKeepsOriginalSequence(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "timed", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	base := time.Date(2026, 10, 18, 10, 0, 0, 0, time.UTC)
	for i, at := range []time.Time{base.Add(-time.Minute), base, base.Add(time.Minute)} {
		if err := AppendHistoryRecord(dir, "timed", HistoryRecord{Role: "user", Content: fmt.Sprintf("Ada travel %d", i), At: at}); err != nil {
			t.Fatal(err)
		}
	}
	page, err := SearchHistoryQuery(context.Background(), dir, HistorySearchQuery{SessionID: "timed", Query: "Ada travel", TimeMin: base, TimeMax: base.Add(time.Minute), Limit: 10})
	if err != nil || len(page.Records) != 1 || page.Records[0].Seq != 2 || !page.Records[0].At.Equal(base) {
		t.Fatalf("wrong time window: %+v %v", page, err)
	}
	if _, err := SearchHistoryQuery(context.Background(), dir, HistorySearchQuery{SessionID: "timed", Query: "Ada", TimeMin: base, TimeMax: base.Add(-time.Second), Limit: 10}); err == nil {
		t.Fatal("reversed window accepted")
	}
	legacy, err := SearchHistoryPage(context.Background(), dir, "timed", "Ada", 0, 10)
	if err != nil || len(legacy.Records) != 3 {
		t.Fatalf("unfiltered Coding search changed: %+v %v", legacy, err)
	}
}

func TestConversationHistorySearchProjectsBeforePagination(t *testing.T) {
	dir := t.TempDir()
	if _, err := CreateWithMetadata(dir, "visible", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	records := []HistoryRecord{
		{Role: "user", Content: "visible Ada question"},
		{Role: "assistant", Content: "visible Ada answer"},
		{Role: "assistant", Content: "ordinary answer", ReasoningContent: "secret Ada reasoning"},
		{Role: "tool", Content: "secret Ada tool result"},
		{Role: "user", Origin: "user", Content: "host Ada path", DisplayContent: "file caption", Files: []byte(`[{"filename":"record.txt"}]`)},
	}
	if _, _, err := AppendHistoryRecordsReturningRange(dir, "visible", records); err != nil {
		t.Fatal(err)
	}
	page, err := SearchHistoryQuery(context.Background(), dir, HistorySearchQuery{SessionID: "visible", Query: "Ada", View: "conversation", Limit: 1})
	if err != nil || len(page.Records) != 1 || page.Records[0].Seq != 2 || !page.HasMore {
		t.Fatalf("hidden match consumed page: %+v %v", page, err)
	}
	next, err := SearchHistoryQuery(context.Background(), dir, HistorySearchQuery{SessionID: "visible", Query: "Ada", View: "conversation", Limit: 1, Cursor: page.Next})
	if err != nil || len(next.Records) != 1 || next.Records[0].Seq != 1 || next.HasMore {
		t.Fatalf("visible cursor lost source: %+v %v", next, err)
	}
	all, err := SearchHistoryQuery(context.Background(), dir, HistorySearchQuery{SessionID: "visible", Query: "Ada", Limit: 10})
	if err != nil || len(all.Records) != 5 {
		t.Fatalf("default trace search changed: %+v %v", all, err)
	}
}
