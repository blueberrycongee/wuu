package main

import (
	"bytes"
	"context"
	"encoding/json"
	"reflect"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Failure cases: full-fixture buffering, reset sequence numbers between batches,
// rewriting supplied sidebar timestamps, and partial append after domain error.
func TestFixtureAppendsBatchesWithoutChangingMetadata(t *testing.T) {
	for _, provider := range []string{persistence.SQLiteModule, persistence.JSONLModule} {
		t.Run(provider, func(t *testing.T) {
			home, workdir, _ := fixtureHome(t, provider)
			stamp := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
			metadata := session.Session{ID: "batched", Title: "large history", Entries: 4, CreatedAt: stamp, UpdatedAt: stamp.Add(time.Hour), CWD: workdir}
			var seeded struct {
				IDs []string `json:"ids"`
			}
			fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "op": "seed", "sessions": []fixtureSession{{Session: metadata}}}, &seeded)
			var expected []session.HistoryRecord
			for batch := 0; batch < 2; batch++ {
				history := []session.HistoryRecord{{Role: "user", Content: "input", At: stamp}, {Role: "assistant", Content: "output", Phase: "final_answer", At: stamp}}
				var result struct {
					ID       string `json:"id"`
					FirstSeq int    `json:"first_seq"`
					LastSeq  int    `json:"last_seq"`
					Entries  int    `json:"entries"`
				}
				fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "op": "append", "id": metadata.ID, "history": history}, &result)
				if result.ID != metadata.ID || result.FirstSeq != batch*2+1 || result.LastSeq != batch*2+2 || result.Entries != result.LastSeq {
					t.Fatalf("noncontiguous batch: %+v", result)
				}
				for i := range history {
					history[i].Seq = batch*2 + i + 1
				}
				expected = append(expected, history...)
			}
			// The second row cannot project a nonexistent tool invocation. The first
			// row in this same batch must roll back with it.
			rejected := []session.HistoryRecord{{Role: "user", Content: "rolled back"}, {Role: "tool", ToolInvocationID: "missing-invocation", Content: "invalid"}}
			request := map[string]any{"home": home, "workdir": workdir, "op": "append", "id": metadata.ID, "history": rejected}
			raw, _ := json.Marshal(request)
			var output bytes.Buffer
			if err := run(context.Background(), bytes.NewReader(raw), &output); err == nil || output.Len() != 0 {
				t.Fatalf("failed batch acknowledged: err=%v output=%q", err, output.String())
			}
			var read struct {
				Sessions []fixtureSession `json:"sessions"`
			}
			fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "op": "read", "ids": []string{metadata.ID}}, &read)
			if len(read.Sessions) != 1 || !reflect.DeepEqual(read.Sessions[0].Session, metadata) || !reflect.DeepEqual(read.Sessions[0].History, expected) {
				t.Fatalf("batch changed metadata or history: %+v", read)
			}
		})
	}
}
