package main

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Failure cases: pending-only projections hide consumed receipts, active history
// hides retractions, and translated accounting fields lose unknown cost/usage.
func TestFixtureInspectionPreservesDurableReceiptsAndAccounting(t *testing.T) {
	home, workdir, cfg := fixtureHome(t, persistence.JSONLModule)
	t.Setenv("WUU_HOME", home)
	state, err := runtime.OpenState(context.Background(), runtime.StateOptions{RootDir: workdir, HomeDir: home, ConfigPath: filepath.Join(home, "config.json"), ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg})
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := state.Close(); err != nil {
			t.Error(err)
		}
	})
	if _, err := state.Sessions.CreateInitialized(session.Session{ID: "receipts"}, nil); err != nil {
		t.Fatal(err)
	}
	for _, id := range []string{"consumed", "pending", "dispatching"} {
		entry := session.HeldUserWork{ID: id, Origin: session.HeldUserWorkOriginSteer, MessageJSON: []byte(`{"text":"canonical payload"}`), RuntimeJSON: []byte(`{}`)}
		if _, err := state.Sessions.AcceptHeldUserWork("receipts", entry); err != nil {
			t.Fatal(err)
		}
		if id == "consumed" {
			if _, err := state.Sessions.AppendAcceptedUserHistoryRecord("receipts", session.HistoryRecord{Role: "user", Content: "retracted input", ClientID: id}, entry); err != nil {
				t.Fatal(err)
			}
		}
		if id == "dispatching" {
			if err := state.Sessions.ClaimHeldUserWork("receipts", entry); err != nil {
				t.Fatal(err)
			}
		}
	}
	if err := state.Sessions.RewriteHistoryRecordsForEdit("receipts", []session.HistoryRecord{{Role: "user", Content: "replacement"}}, 1, 1); err != nil {
		t.Fatal(err)
	}
	journalRuntime, err := state.Sessions.NewInferenceJournalRuntime("fixture-scope")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := journalRuntime.Close(); err != nil {
			t.Error(err)
		}
	})
	journal := journalRuntime.ForOwner("receipts")
	now := time.Date(2026, 1, 2, 3, 4, 5, 0, time.UTC)
	for i, cost := range []providers.InferenceCostState{providers.InferenceCostKnown, providers.InferenceCostUnknownBillable} {
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := strings.Repeat("a", 64)
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{OperationID: op.ID, AttemptID: op.AttemptID(1), Ordinal: 1, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		submission := providers.InferenceSubmissionJournalRecord{OperationID: op.ID, AttemptID: op.AttemptID(1), ID: op.ID + "-s1", Ordinal: 1, AttemptOrdinal: 1, Provider: "fixture", Protocol: "responses", Transport: "http", Mode: "stream", StartedAt: now.Add(time.Duration(i) * time.Millisecond), Outcome: providers.InferenceSubmissionInFlight, CostState: cost}
		if cost == providers.InferenceCostKnown {
			submission.ReportedUsage = &providers.TokenUsage{InputTokens: 17, OutputTokens: 9, CacheReadTokens: 4, CacheCreationTokens: 3}
			submission.CompletedAt = now.Add(time.Second)
			submission.Outcome = providers.InferenceSubmissionSucceeded
		}
		if err := journal.UpsertSubmission(submission); err != nil {
			t.Fatal(err)
		}
	}
	domains := []string{"held_user_work", "session_history_retractions", "inference_operations", "inference_submissions", "inference_workflows"}
	var inspected struct {
		Revision  persistence.Revision              `json:"revision,string"`
		Documents map[string][]persistence.Document `json:"documents"`
	}
	fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "op": "inspect", "domains": domains}, &inspected)
	counts := map[string]int{"held_user_work": 3, "session_history_retractions": 1, "inference_operations": 2, "inference_submissions": 2, "inference_workflows": 2}
	for _, domain := range domains {
		view, err := state.Documents.Read(context.Background(), persistence.Query{Namespace: "wuu." + domain + ".v1", Limit: 256})
		if err != nil {
			t.Fatal(err)
		}
		if len(inspected.Documents[domain]) != counts[domain] || !reflect.DeepEqual(inspected.Documents[domain], view.Documents) {
			t.Fatalf("%s lost durable records: got=%+v want=%+v", domain, inspected.Documents[domain], view.Documents)
		}
	}
	var read struct {
		Sessions []fixtureSession `json:"sessions"`
	}
	fixtureCall(t, map[string]any{"home": home, "workdir": workdir, "op": "read", "ids": []string{"receipts"}}, &read)
	if len(read.Sessions) != 1 || len(read.Sessions[0].History) != 2 || read.Sessions[0].History[0].Content != "retracted input" {
		t.Fatalf("physical history omitted retracted records: %+v", read)
	}
	var receipt struct {
		State       string
		MessageJSON []byte
	}
	for _, document := range inspected.Documents["held_user_work"] {
		if err := json.Unmarshal(document.Value, &receipt); err != nil {
			t.Fatal(err)
		}
		if receipt.State == "pending" && string(receipt.MessageJSON) != `{"text":"canonical payload"}` {
			t.Fatalf("held message changed: %s", receipt.MessageJSON)
		}
	}
}

// A commit between namespaces must restart the entire pure read, including
// previously collected pages; it must never combine two durable revisions.
func TestInspectionPagesAtOneRevisionWithoutWrites(t *testing.T) {
	ctx := context.Background()
	store, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if err := store.Close(); err != nil {
			t.Error(err)
		}
	})
	if err := persistence.Update(ctx, store, func(tx *persistence.Tx) error {
		for i := 0; i < 300; i++ {
			if err := tx.Put("wuu.held_user_work.v1", fmt.Sprintf("receipt-%03d", i), map[string]string{"version": "before"}); err != nil {
				return err
			}
		}
		return tx.Put("wuu.inference_submissions.v1", "submission", map[string]string{"version": "before"})
	}); err != nil {
		t.Fatal(err)
	}
	documents := &changingInspectionDocuments{Documents: store, t: t}
	inspected, err := inspectDocuments(ctx, documents, []string{"held_user_work", "inference_submissions"})
	if err != nil {
		t.Fatal(err)
	}
	if !documents.changed || !documents.conflicted || len(inspected.Documents["held_user_work"]) != 300 || len(inspected.Documents["inference_submissions"]) != 1 {
		t.Fatalf("read omitted pages or did not retry: %+v", documents)
	}
	for _, domain := range []string{"held_user_work", "inference_submissions"} {
		var value map[string]string
		if err := json.Unmarshal(inspected.Documents[domain][0].Value, &value); err != nil {
			t.Fatal(err)
		}
		if value["version"] != "after" {
			t.Fatalf("mixed revision in %s: %+v", domain, value)
		}
	}
	if _, err := inspectDocuments(ctx, documents, []string{"credentials"}); err == nil {
		t.Fatal("unapproved domain accepted")
	}
}

type changingInspectionDocuments struct {
	persistence.Documents
	t                   *testing.T
	changed, conflicted bool
}

func (d *changingInspectionDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if q.Limit <= 0 || q.Limit > persistence.MaxReadLimit {
		d.t.Fatalf("unbounded query: %+v", q)
	}
	if q.Namespace == "wuu.inference_submissions.v1" && !d.changed {
		d.changed = true
		if err := persistence.Update(ctx, d.Documents, func(tx *persistence.Tx) error {
			if err := tx.Put("wuu.held_user_work.v1", "receipt-000", map[string]string{"version": "after"}); err != nil {
				return err
			}
			return tx.Put("wuu.inference_submissions.v1", "submission", map[string]string{"version": "after"})
		}); err != nil {
			return persistence.View{}, err
		}
	}
	view, err := d.Documents.Read(ctx, q)
	if errors.Is(err, persistence.ErrConflict) {
		d.conflicted = true
	}
	return view, err
}

func (d *changingInspectionDocuments) Commit(context.Context, persistence.Transaction) (persistence.Receipt, error) {
	d.t.Fatal("inspection attempted a write")
	return persistence.Receipt{}, errors.New("unexpected commit")
}
