package toolledger

import (
	"context"
	"encoding/json"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"testing"
)

// The checkpoint cannot be forged by a remote worker, moved between owners,
// overwritten by a stale attempt, or commit state independently of its outcome.
func TestCodeJournalFencesAttemptsAndCommitsStateWithOutcome(t *testing.T) {
	forEachLedgerStore(t, func(t *testing.T, documents *ledgerTestDocuments) {
		ctx := context.Background()
		ledger, _ := New(documents, "owner")
		call := providers.ToolCall{ID: "program", Name: "run_code", Arguments: `{}`}
		parents, err := ledger.AdmitBatch(ctx, "operation/batch", "driver@1", 1, []providers.ToolCall{call}, []ReplayPolicy{ReplayRepeatable})
		if err != nil {
			t.Fatal(err)
		}
		parent := parents[0]
		if err := ledger.StartAttempt(ctx, parent.ID, parent.Attempt); err != nil {
			t.Fatal(err)
		}
		access := func(req toolctx.CodeJournalRequest) (toolctx.CodeJournalResponse, error) {
			return ledger.CodeJournal(ctx, parent.ID, parent.Attempt, req)
		}
		open := toolctx.CodeJournalRequest{Operation: "open", Fingerprint: "exact", Scope: "scope", Start: json.RawMessage(`{"seed":7}`)}
		first, err := access(open)
		if err != nil || string(first.Start) != `{"seed":7}` {
			t.Fatalf("open %+v %v", first, err)
		}
		if _, err := access(toolctx.CodeJournalRequest{Operation: "append", Index: 1, Entry: json.RawMessage(`{}`)}); err == nil {
			t.Fatal("skipped journal entry accepted")
		}
		if _, err := access(toolctx.CodeJournalRequest{Operation: "append", Entry: json.RawMessage(`{"calls":[1]}`)}); err != nil {
			t.Fatal(err)
		}
		changed := open
		changed.Fingerprint = "changed"
		if _, err := access(changed); err == nil {
			t.Fatal("changed program admitted")
		}
		other, _ := New(documents, "other")
		if _, err := other.CodeJournal(ctx, parent.ID, parent.Attempt, open); err == nil {
			t.Fatal("cross-owner journal access")
		}
		if err := ledger.Reconcile(ctx); err != nil {
			t.Fatal(err)
		}
		parents, err = ledger.RecoverBatch(ctx, "operation/batch", "driver@1", []providers.ToolCall{call}, []ReplayPolicy{ReplayRepeatable})
		if err != nil {
			t.Fatal(err)
		}
		if err := ledger.StartAttempt(ctx, parents[0].ID, parents[0].Attempt); err != nil {
			t.Fatal(err)
		}
		if _, err := access(toolctx.CodeJournalRequest{Operation: "complete", Index: 1, Outcome: json.RawMessage(`{"Value":7}`), State: map[string]string{`"key"`: `1`}}); err == nil {
			t.Fatal("stale completion committed")
		}
		parent = parents[0]
		resumed, err := access(open)
		if err != nil || resumed.Length != 1 {
			t.Fatalf("resume %+v %v", resumed, err)
		}
		entry, err := access(toolctx.CodeJournalRequest{Operation: "read", Index: 0})
		if err != nil || string(entry.Entry) != `{"calls":[1]}` {
			t.Fatalf("entry %+v %v", entry, err)
		}
		if _, err := access(toolctx.CodeJournalRequest{Operation: "complete", Index: 1, Outcome: json.RawMessage(`{"Value":7}`), State: map[string]string{`"key"`: `1`}}); err != nil {
			t.Fatal(err)
		}
		complete, err := access(open)
		if err != nil || string(complete.Outcome) != `{"Value":7}` {
			t.Fatalf("outcome %+v %v", complete, err)
		}
		if _, err := access(toolctx.CodeJournalRequest{Operation: "append", Index: 1, Entry: json.RawMessage(`{}`)}); err == nil {
			t.Fatal("completed journal changed")
		}
		next, err := ledger.AdmitBatch(ctx, "next/batch", "driver@1", 2, []providers.ToolCall{call}, []ReplayPolicy{ReplayRepeatable})
		if err != nil {
			t.Fatal(err)
		}
		if err := ledger.StartAttempt(ctx, next[0].ID, next[0].Attempt); err != nil {
			t.Fatal(err)
		}
		state, err := ledger.CodeJournal(ctx, next[0].ID, next[0].Attempt, open)
		if err != nil || state.State[`"key"`] != `1` {
			t.Fatalf("atomic state %+v %v", state, err)
		}
	})
}
