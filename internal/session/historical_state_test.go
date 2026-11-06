package session

import (
	"context"
	"encoding/json"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// Failure contract: old/current configuration confusion; completed-but-not-yet
// projected store leakage; restart loss; child/parent state aliasing; forged or
// retracted cutoffs; restoring an old permission mode; default desktop overhead.
func TestHistoricalForkStateRecovery(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		ctx := context.Background()
		api := NewStore(ctx, store.documents)
		_, err := api.CreateInitialized(Session{ID: "parent", CWD: "/repo", Provider: "p", Model: "old-model", Instructions: "old instructions", PermissionMode: "unconfined", ApproveForMe: true}, nil)
		if err != nil {
			t.Fatal(err)
		}
		binding := pluginprotocol.DriverBinding{Profile: "cloud", ProviderID: "custom", DriverID: "custom", DriverVersion: "1", ContractVersion: 1, HistoryState: true}
		if err := api.BindDriver("parent", binding); err != nil {
			t.Fatal(err)
		}
		ledger, err := toolledger.New(store.documents, "parent")
		if err != nil {
			t.Fatal(err)
		}
		scope := `["parent","parent","/repo"]`
		complete := func(operation, value string) string {
			t.Helper()
			calls, err := ledger.AdmitBatch(ctx, operation, "custom@1", 1, []providers.ToolCall{{ID: operation, Name: "run_code", Arguments: `{}`}}, []toolledger.ReplayPolicy{toolledger.ReplayRepeatable})
			if err != nil {
				t.Fatal(err)
			}
			call := calls[0]
			if err := ledger.StartAttempt(ctx, call.ID, call.Attempt); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.CodeJournal(ctx, call.ID, call.Attempt, toolctx.CodeJournalRequest{Operation: "open", Scope: scope, Fingerprint: "program", Start: json.RawMessage(`{}`)}); err != nil {
				t.Fatal(err)
			}
			if _, err := ledger.CodeJournal(ctx, call.ID, call.Attempt, toolctx.CodeJournalRequest{Operation: "complete", Outcome: json.RawMessage(`{}`), State: map[string]string{`"key"`: value}}); err != nil {
				t.Fatal(err)
			}
			if err := ledger.SettleAttempt(ctx, call.ID, call.Attempt, toolresult.FromText(value)); err != nil {
				t.Fatal(err)
			}
			return call.ID
		}
		first := complete("first", `"old"`)
		cutoff, err := api.AppendHistoryRecordReturningSeq("parent", HistoryRecord{Role: "tool", ToolInvocationID: first, Content: "old"})
		if err != nil {
			t.Fatal(err)
		}
		second := complete("second", `"new"`)
		// A normal entry cannot publish the later program's unprojected state.
		beforeProjection, err := api.AppendHistoryRecordReturningSeq("parent", HistoryRecord{Role: "assistant", Content: "before projection"})
		if err != nil {
			t.Fatal(err)
		}
		if _, err := api.SetRuntimeSelection("parent", RuntimeSelection{Provider: "p", Model: "new-model", PermissionMode: "read_only"}); err != nil {
			t.Fatal(err)
		}
		if err := api.AppendHistoryRecord("parent", HistoryRecord{Role: "tool", ToolInvocationID: second, Content: "new"}); err != nil {
			t.Fatal(err)
		}
		restartSessionStore(t, store)
		original, err := api.LoadHistoricalState("parent", cutoff)
		if err != nil {
			t.Fatal(err)
		}
		unprojected, err := api.LoadHistoricalState("parent", beforeProjection)
		if err != nil {
			t.Fatal(err)
		}
		if original.Agent.Model != "old-model" || unprojected.Stores[scope] != original.Stores[scope] {
			t.Fatalf("wrong historical state: %+v %+v", original, unprojected)
		}
		prefix, err := api.LoadActiveHistoryRecords("parent", true)
		if err != nil {
			t.Fatal(err)
		}
		created, err := api.CreateInitialized(Session{ID: "child", ForkedFromID: "parent", ForkedFromSeq: cutoff, ForkHistoryDigest: HistoricalPrefixDigest(prefix, cutoff), CWD: "/repo", Provider: "p", Model: original.Agent.Model, PermissionMode: "read_only"}, []HistoryRecord{{Seq: cutoff, Role: "tool", Content: "old"}})
		if err != nil {
			t.Fatal(err)
		}
		if created.PermissionMode != "read_only" || created.ApproveForMe {
			t.Fatalf("historical permission restored: %+v", created)
		}
		childLedger, _ := toolledger.New(store.documents, "child")
		calls, err := childLedger.AdmitBatch(ctx, "child-call", "custom@1", 1, []providers.ToolCall{{ID: "child-call", Name: "run_code", Arguments: `{}`}}, []toolledger.ReplayPolicy{toolledger.ReplayRepeatable})
		if err != nil {
			t.Fatal(err)
		}
		call := calls[0]
		if err := childLedger.StartAttempt(ctx, call.ID, call.Attempt); err != nil {
			t.Fatal(err)
		}
		state, err := childLedger.CodeJournal(ctx, call.ID, call.Attempt, toolctx.CodeJournalRequest{Operation: "open", Scope: `["child","child","/repo"]`, Fingerprint: "child-program", Start: json.RawMessage(`{}`)})
		if err != nil || state.State[`"key"`] != `"old"` {
			t.Fatalf("fork store=%+v err=%v", state, err)
		}
		// Empty is a committed clear, not the absence of a state commit. Persist it
		// through the selected service, restart, and fork that exact boundary.
		if _, err := childLedger.CodeJournal(ctx, call.ID, call.Attempt, toolctx.CodeJournalRequest{Operation: "complete", Outcome: json.RawMessage(`{}`), State: map[string]string{}}); err != nil {
			t.Fatal(err)
		}
		if err := childLedger.SettleAttempt(ctx, call.ID, call.Attempt, toolresult.FromText("cleared")); err != nil {
			t.Fatal(err)
		}
		clearSeq, err := api.AppendHistoryRecordReturningSeq("child", HistoryRecord{Role: "tool", ToolInvocationID: call.ID, Content: "cleared"})
		if err != nil {
			t.Fatal(err)
		}
		restartSessionStore(t, store)
		clearPrefix, err := api.LoadActiveHistoryRecords("child", true)
		if err != nil {
			t.Fatal(err)
		}
		clearRecords := append([]HistoryRecord(nil), clearPrefix...)
		for i := range clearRecords {
			clearRecords[i].ToolInvocationID = ""
		}
		if _, err := api.CreateInitialized(Session{ID: "cleared-fork", ForkedFromID: "child", ForkedFromSeq: clearSeq, ForkHistoryDigest: HistoricalPrefixDigest(clearPrefix, clearSeq), CWD: "/repo"}, clearRecords); err != nil {
			t.Fatal(err)
		}
		clearedLedger, _ := toolledger.New(store.documents, "cleared-fork")
		clearCalls, err := clearedLedger.AdmitBatch(ctx, "check-clear", "custom@1", 1, []providers.ToolCall{{ID: "check-clear", Name: "run_code", Arguments: `{}`}}, []toolledger.ReplayPolicy{toolledger.ReplayRepeatable})
		if err != nil {
			t.Fatal(err)
		}
		clearCall := clearCalls[0]
		if err := clearedLedger.StartAttempt(ctx, clearCall.ID, clearCall.Attempt); err != nil {
			t.Fatal(err)
		}
		cleared, err := clearedLedger.CodeJournal(ctx, clearCall.ID, clearCall.Attempt, toolctx.CodeJournalRequest{Operation: "open", Scope: `["cleared-fork","cleared-fork","/repo"]`, Fingerprint: "check-clear", Start: json.RawMessage(`{}`)})
		if err != nil || cleared.State == nil || len(cleared.State) != 0 {
			t.Fatalf("empty committed clear lost: %+v %v", cleared, err)
		}
		if _, found, err := api.LoadDriverCheckpoint("child"); err != nil || found {
			t.Fatalf("execution checkpoint inherited: %v %v", found, err)
		}
		if err := api.RewriteHistoryRecordsForEdit("parent", nil, cutoff, beforeProjection+1); err != nil {
			t.Fatal(err)
		}
		if _, err := api.LoadHistoricalState("parent", cutoff); err == nil {
			t.Fatal("retracted historical entry accepted")
		}
	})
}

func TestHistoricalStateRequiresOptInAndPersistedProof(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		if _, err := store.CreateInitialized(Session{ID: "desktop"}, []HistoryRecord{{Role: "user", Content: "lightweight", HistoricalState: &HistoricalState{Version: 1}}}); err != nil {
			t.Fatal(err)
		}
		records, err := store.LoadHistoryRecords("desktop", true)
		if err != nil {
			t.Fatal(err)
		}
		if records[0].HistoricalState != nil {
			t.Fatal("default desktop acquired historical state")
		}
		if _, err := store.LoadHistoricalState("desktop", 1); err == nil || !strings.Contains(err.Error(), "snapshot") {
			t.Fatalf("missing historical proof was accepted: %v", err)
		}
		if _, err := store.CreateInitialized(Session{ID: "bad", ForkedFromID: "desktop", ForkedFromSeq: 1}, nil); err == nil {
			t.Fatal("fork without snapshot accepted")
		}
		if _, found, err := store.Find("bad"); err != nil || found {
			t.Fatalf("failed fork left a child: %v %v", found, err)
		}
	})
}

// A source edit may retract an earlier entry while leaving the selected cutoff
// alive. The atomic prefix fence must reject the half-old prepared fork.
func TestHistoricalForkRejectsChangedPrefix(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		api := NewStore(context.Background(), store.documents)
		if _, err := api.Create("source"); err != nil {
			t.Fatal(err)
		}
		if err := api.BindDriver("source", pluginprotocol.DriverBinding{Profile: "cloud", ProviderID: "custom", DriverID: "custom", DriverVersion: "1", ContractVersion: 1, HistoryState: true}); err != nil {
			t.Fatal(err)
		}
		if err := api.AppendHistoryRecords("source", []HistoryRecord{{Role: "user", Content: "old first"}, {Role: "assistant", Content: "surviving cutoff"}}); err != nil {
			t.Fatal(err)
		}
		prefix, err := api.LoadActiveHistoryRecords("source", true)
		if err != nil {
			t.Fatal(err)
		}
		if err := api.RewriteHistoryRecordsForEdit("source", nil, 1, 1); err != nil {
			t.Fatal(err)
		}
		if _, err := api.LoadHistoricalState("source", 2); err != nil {
			t.Fatal(err)
		}
		if _, err := api.CreateInitialized(Session{ID: "child", ForkedFromID: "source", ForkedFromSeq: 2, ForkHistoryDigest: HistoricalPrefixDigest(prefix, 2)}, prefix); err == nil || !strings.Contains(err.Error(), "source changed") {
			t.Fatalf("mixed prefix accepted: %v", err)
		}
		if _, found, err := api.Find("child"); err != nil || found {
			t.Fatalf("rejected creation left child: %v %v", found, err)
		}
	})
}
