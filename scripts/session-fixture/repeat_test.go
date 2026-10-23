package main

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Failure cases: reopening the authority per batch, materializing all repeats,
// changed pattern bytes/order/metadata, and replaying a commit with unknown outcome.
func TestFixtureRepeatsPatternWithOneCapturedState(t *testing.T) {
	for _, provider := range []string{persistence.SQLiteModule, persistence.JSONLModule} {
		t.Run(provider, func(t *testing.T) {
			home, workdir, cfg := fixtureHome(t, provider)
			t.Setenv("WUU_HOME", home)
			reader, err := runtime.OpenState(context.Background(), runtime.StateOptions{RootDir: workdir, HomeDir: home, ConfigPath: filepath.Join(home, "config.json"), ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg})
			if err != nil {
				t.Fatal(err)
			}
			t.Cleanup(func() {
				if err := reader.Close(); err != nil {
					t.Error(err)
				}
			})
			pattern := repeatedFixturePattern()
			const repeat = 32
			metadata := session.Session{ID: "repeated", Title: "repeated fixture", Entries: len(pattern) * repeat, CreatedAt: pattern[0].At, UpdatedAt: pattern[0].At.Add(time.Hour), CWD: workdir}
			if _, err := reader.Sessions.CreateInitialized(metadata, nil); err != nil {
				t.Fatal(err)
			}
			query := persistence.Query{Namespace: "wuu.inference_journal_runtimes.v1", Limit: 100}
			before, err := reader.Documents.Read(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			var result struct {
				ID       string `json:"id"`
				FirstSeq int    `json:"first_seq"`
				LastSeq  int    `json:"last_seq"`
				Entries  int    `json:"entries"`
			}
			fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "op": "append", "id": metadata.ID, "history": pattern, "repeat": repeat}, &result)
			if result.ID != metadata.ID || result.FirstSeq != 1 || result.LastSeq != metadata.Entries || result.Entries != metadata.Entries {
				t.Fatalf("wrong repeated range: %+v", result)
			}
			after, err := reader.Documents.Read(context.Background(), query)
			if err != nil {
				t.Fatal(err)
			}
			// Each actual OpenState creates one durable runtime lease, which remains
			// after Close. Count that behavior instead of instrumenting source text.
			if len(after.Documents)-len(before.Documents) != 1 {
				t.Fatalf("repeat reopened state: runtime leases %d -> %d", len(before.Documents), len(after.Documents))
			}
			history, err := reader.Sessions.LoadHistoryRecords(metadata.ID, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(history) != metadata.Entries {
				t.Fatalf("history length %d, want %d", len(history), metadata.Entries)
			}
			for i, record := range history {
				expected := pattern[i%len(pattern)]
				expected.Seq = i + 1
				if !reflect.DeepEqual(record, expected) {
					t.Fatalf("pattern changed at seq %d", i+1)
				}
			}
			stored, found, err := reader.Sessions.Find(metadata.ID)
			if err != nil || !found || !reflect.DeepEqual(stored, metadata) {
				t.Fatalf("metadata changed: %+v found=%v err=%v", stored, found, err)
			}
			// Both explicit single append and a repeated request that fits one batch
			// must preserve the domain's all-or-nothing failure behavior.
			for _, count := range []int{1, 2} {
				request := map[string]any{"home": home, "workdir": workdir, "op": "append", "id": metadata.ID, "history": []session.HistoryRecord{{Role: "user", Content: "must roll back"}, {Role: "tool", ToolInvocationID: "unsettled"}}, "repeat": count}
				raw, _ := json.Marshal(request)
				var output bytes.Buffer
				if err := run(context.Background(), bytes.NewReader(raw), &output); err == nil || output.Len() != 0 {
					t.Fatalf("failed repeated batch acknowledged: %v %q", err, output.String())
				}
			}
			final, err := reader.Sessions.LoadHistoryRecords(metadata.ID, true)
			if err != nil || len(final) != len(history) {
				t.Fatalf("failed repeated batch partially persisted: len=%d err=%v", len(final), err)
			}
		})
	}
}

func repeatedFixturePattern() []session.HistoryRecord {
	stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	var pattern []session.HistoryRecord
	for _, provider := range []string{"subscription-grok", "subscription-codex"} {
		pattern = append(pattern, session.HistoryRecord{Role: "user", Content: "historical input", At: stamp}, session.HistoryRecord{Role: "assistant", Content: strings.Repeat("Synthetic historical content. ", 2300), Phase: "final_answer", At: stamp}, session.HistoryRecord{Role: "meta", Content: "token_usage", Provider: provider, Model: "historical", InputTokens: 7, At: stamp}, session.HistoryRecord{Role: "meta", Content: "turn_terminal", Provider: provider, Model: "historical", InputTokens: 7, At: stamp})
	}
	return pattern
}

func TestRepeatedAppendBoundsBatchesAndDoesNotReplayUnknownCommit(t *testing.T) {
	ctx := context.Background()
	documents, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := documents.Close(); err != nil {
			t.Error(err)
		}
	})
	reader := session.NewStore(ctx, documents)
	if _, err := reader.CreateInitialized(session.Session{ID: "unknown-batch"}, nil); err != nil {
		t.Fatal(err)
	}
	unknown := errors.New("response lost after commit")
	observed := &repeatDocuments{Documents: documents, t: t, unknown: unknown}
	store := session.NewStore(ctx, observed)
	if _, _, err := appendRepeatedHistory(store, "unknown-batch", repeatedFixturePattern(), 32); !errors.Is(err, unknown) {
		t.Fatalf("unknown outcome hidden: %v", err)
	}
	if observed.historyCommits != 2 || observed.records != 256 {
		t.Fatalf("replayed or truncated batches: commits=%d records=%d", observed.historyCommits, observed.records)
	}
	persisted, err := reader.LoadHistoryRecords("unknown-batch", true)
	if err != nil || len(persisted) != 256 {
		t.Fatalf("unknown committed rows: len=%d err=%v", len(persisted), err)
	}
	// A single indivisible record over the repeat bound must fail before append.
	oversized := []session.HistoryRecord{{Role: "assistant", Content: strings.Repeat("x", 4<<20)}}
	if _, _, err := appendRepeatedHistory(store, "unknown-batch", oversized, 2); err == nil {
		t.Fatal("oversized repeated record accepted")
	}
	if observed.historyCommits != 2 {
		t.Fatal("invalid pattern wrote data")
	}
}

type repeatDocuments struct {
	persistence.Documents
	t                       *testing.T
	historyCommits, records int
	unknown                 error
}

func (d *repeatDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	var records []session.HistoryRecord
	for _, write := range tx.Writes {
		if write.Namespace != "wuu.session_messages.v1" {
			continue
		}
		var record session.HistoryRecord
		if err := json.Unmarshal(write.Value, &record); err != nil {
			d.t.Fatal(err)
		}
		record.Seq = 0
		records = append(records, record)
	}
	encoded, err := json.Marshal(records)
	if err != nil {
		d.t.Fatal(err)
	}
	if len(records) == 0 || len(encoded) > 4<<20 {
		d.t.Fatalf("invalid history batch: records=%d bytes=%d", len(records), len(encoded))
	}
	d.historyCommits++
	d.records += len(records)
	receipt, err := d.Documents.Commit(ctx, tx)
	if err == nil && d.historyCommits == 2 {
		err = d.unknown
	}
	return receipt, err
}

func TestFixtureRejectsInvalidRepeatBeforeOpeningState(t *testing.T) {
	for _, repeat := range []int{0, -1, int(^uint(0) >> 1)} {
		raw, _ := json.Marshal(map[string]any{"home": "/unused", "workdir": "/unused", "op": "append", "id": "invalid", "history": []session.HistoryRecord{{Role: "user"}, {Role: "assistant"}}, "repeat": repeat})
		var output bytes.Buffer
		err := run(context.Background(), bytes.NewReader(raw), &output)
		if err == nil || !strings.Contains(err.Error(), "repeat") || output.Len() != 0 {
			t.Fatalf("invalid repeat %d: err=%v output=%q", repeat, err, output.String())
		}
	}
}
