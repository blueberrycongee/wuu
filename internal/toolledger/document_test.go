package toolledger

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

type ledgerTestDocuments struct {
	*persistence.Store
	dir, backend string
}

func forEachLedgerStore(t *testing.T, run func(*testing.T, *ledgerTestDocuments)) {
	t.Helper()
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			dir := t.TempDir()
			store, err := persistence.Open(context.Background(), dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			documents := &ledgerTestDocuments{Store: store, dir: dir, backend: backend}
			t.Cleanup(func() {
				if err := documents.Close(); err != nil {
					t.Error(err)
				}
			})
			run(t, documents)
		})
	}
}

func restartLedgerStore(t *testing.T, documents *ledgerTestDocuments) {
	t.Helper()
	if err := documents.Close(); err != nil {
		t.Fatal(err)
	}
	store, err := persistence.Open(context.Background(), documents.dir, documents.backend)
	if err != nil {
		t.Fatal(err)
	}
	documents.Store = store
}

func readLedgerDocument[T any](t *testing.T, documents persistence.Documents, namespace, id string) T {
	t.Helper()
	view, err := documents.Read(context.Background(), persistence.Query{Namespace: namespace, Keys: []string{id}})
	if err != nil {
		t.Fatal(err)
	}
	if len(view.Documents) != 1 {
		t.Fatalf("missing %s/%s", namespace, id)
	}
	var value T
	if err := json.Unmarshal(view.Documents[0].Value, &value); err != nil {
		t.Fatal(err)
	}
	return value
}

// Projection must commit together with history, preserve a settled result when
// admission fails, and reject mutation from a different execution owner.
func TestLedgerHistoryProjectionOwnershipAndAtomicity(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, documents *ledgerTestDocuments) {
		ctx := context.Background()
		sessions := session.NewStore(ctx, documents)
		if _, err := sessions.CreateInitialized(session.Session{ID: "owner"}, nil); err != nil {
			t.Fatal(err)
		}
		owner, err := NewDocuments(documents, "owner")
		if err != nil {
			t.Fatal(err)
		}
		other, err := NewDocuments(documents, "other")
		if err != nil {
			t.Fatal(err)
		}
		batch, err := owner.BeginBatch(ctx, "operation", 1)
		if err != nil {
			t.Fatal(err)
		}
		invocation, err := owner.Prepare(ctx, batch, providers.ToolCall{ID: "call", Name: "write", Arguments: `{}`}, ReplayAtMostOnce)
		if err != nil {
			t.Fatal(err)
		}
		if err := owner.FinalizeBatch(ctx, batch); err != nil {
			t.Fatal(err)
		}
		if err := owner.Start(ctx, invocation.ID); err != nil {
			t.Fatal(err)
		}
		if err := other.Settle(ctx, invocation.ID, toolresult.FromText("forged")); err == nil {
			t.Fatal("another owner settled invocation")
		}
		if _, err := other.DecideReplay(ctx, batch); err == nil {
			t.Fatal("another owner read replay state")
		}
		if err := owner.Settle(ctx, invocation.ID, toolresult.FromText("written")); err != nil {
			t.Fatal(err)
		}
		if err := other.MarkProjected(ctx, []string{invocation.ID}); err == nil {
			t.Fatal("another owner projected invocation")
		}
		record := session.HistoryRecord{Role: "tool", Content: "written", ToolInvocationID: invocation.ID}
		if err := sessions.AppendHistoryRecord("missing", record); err == nil {
			t.Fatal("missing session accepted result")
		}
		if pending, err := owner.PendingProjection(ctx); err != nil || len(pending) != 1 {
			t.Fatalf("failed admission lost result: %+v, %v", pending, err)
		}
		if err := sessions.AppendHistoryRecord("owner", record); err != nil {
			t.Fatal(err)
		}
		restartLedgerStore(t, documents)
		if pending, err := owner.PendingProjection(ctx); err != nil || len(pending) != 0 {
			t.Fatalf("committed projection replayed: %+v, %v", pending, err)
		}
		history, err := sessions.LoadHistoryRecords("owner", true)
		if err != nil || len(history) != 1 || history[0].Content != "written" {
			t.Fatalf("history: %+v, %v", history, err)
		}
		stored := readLedgerDocument[toolstate.Invocation](t, documents, toolstate.Invocations, invocation.ID)
		if stored.Result != nil || stored.ProjectedAt == 0 {
			t.Fatalf("result not disposed atomically: %+v", stored)
		}
		if decision, err := owner.DecideReplay(ctx, batch); err != nil || decision.Action != ReplayBlock {
			t.Fatalf("projection lost replay fence: %+v, %v", decision, err)
		}
	})
}
