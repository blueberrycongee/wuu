package appserver

import (
	"context"
	"encoding/json"
	"fmt"
	"strings"
	"testing"
	"time"
)

func TestThreadListSummaryPreservesMetadataAndFullHistory(t *testing.T) {
	for _, method := range []string{"thread/list", "thread/listAll", "thread/listArchived"} {
		t.Run(method, func(t *testing.T) {
			rt := newTestRuntime(t, &fakeClient{})
			out := &lockedBuffer{}
			srv := New(rt, out)
			th := newThreadState("summary-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now())
			th.Turns = []Turn{{ID: "retained-turn"}}
			th.Title = "Conversation title"
			th.running = true
			if method == "thread/listArchived" {
				now := time.Now()
				th.ArchivedAt = &now
			}
			srv.threads[th.ID] = th
			for i, summary := range []bool{true, false} {
				id := string(rune('a' + i))
				raw, _ := json.Marshal(map[string]any{"id": id, "method": method, "params": ThreadListParams{SummaryOnly: summary}})
				if err := srv.handleLine(context.Background(), raw); err != nil {
					t.Fatal(err)
				}
				result := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), id)["result"])
				if len(result.Threads) != 1 {
					t.Fatalf("missing thread: %+v", result)
				}
				got := result.Threads[0]
				if got.ID != th.ID || got.Title != th.Title || got.Status != ThreadStatusInProgress || got.CWD != rt.RootDir {
					t.Fatal("summary lost metadata", got)
				}
				if summary {
					if got.Turns == nil || len(got.Turns) != 0 {
						t.Fatal("summary must contain an empty turns array")
					}
				} else if len(got.Turns) != 1 || got.Turns[0].ID != "retained-turn" {
					t.Fatal("summary changed retained history", got)
				}
			}
		})
	}
}

func TestResumeResponseOnlyRetainsHistoryWithoutBroadcast(t *testing.T) {
	for _, responseOnly := range []bool{false, true} {
		rt := newTestRuntime(t, &fakeClient{})
		out := &lockedBuffer{}
		srv := New(rt, out)
		th := newThreadState("resume-thread", nil, rt.ProviderName, rt.Model, rt.RootDir, false, time.Now())
		th.Turns = []Turn{{ID: "retained-turn"}}
		srv.threads[th.ID] = th
		raw, _ := json.Marshal(map[string]any{"id": "resume", "method": "thread/resume", "params": ThreadResumeParams{SessionID: th.ID, ResponseOnly: responseOnly}})
		if err := srv.handleLine(context.Background(), raw); err != nil {
			t.Fatal(err)
		}
		rows := parseOutput(t, out.String())
		result := remarshal[ThreadResumeResult](t, responseByID(t, rows, "resume")["result"])
		if len(result.Thread.Turns) != 1 {
			t.Fatal("missing retained history")
		}
		broadcasts := 0
		for _, row := range rows {
			if row["method"] == NotificationThreadResumed {
				broadcasts++
			}
		}
		if (broadcasts == 0) != responseOnly {
			t.Fatalf("responseOnly=%v broadcasts=%d", responseOnly, broadcasts)
		}
	}
}

// Report both lock-held snapshot allocations and wire bytes for a loaded
// conversation; a summary must not pay for history it never sends.
func BenchmarkThreadListSnapshot(b *testing.B) {
	th := newThreadState("large-thread", nil, "provider", "model", "/workspace", false, time.Now())
	for turn := range 100 {
		items := make([]ThreadItem, 20)
		for index := range items {
			items[index] = ThreadItem{
				ID: fmt.Sprintf("item-%d-%d", turn, index), Type: ThreadItemAgentMessage,
				Text: strings.Repeat("output ", 1024),
			}
		}
		th.Turns = append(th.Turns, Turn{ID: fmt.Sprintf("turn-%d", turn), Items: items, Status: TurnStatusCompleted})
	}
	for _, summary := range []bool{false, true} {
		b.Run(fmt.Sprintf("summary=%v", summary), func(b *testing.B) {
			raw, err := json.Marshal(th.listSnapshotLocked(summary))
			if err != nil {
				b.Fatal(err)
			}
			b.ReportAllocs()
			b.ResetTimer()
			for range b.N {
				th.mu.Lock()
				_ = th.listSnapshotLocked(summary)
				th.mu.Unlock()
			}
			b.ReportMetric(float64(len(raw)), "wire-B")
		})
	}
}
