package session

import (
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// The recovery read must never cross a session/driver boundary or manufacture
// zero usage when a retained correlation or authoritative submission is absent.
func TestModelUsageReceiptRecoveryBoundary(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, s *documentStore) {
		err := s.transaction(func(tx *persistence.Tx) error {
			payload := json.RawMessage(`{"request_id":"boundary","model":"test-model"}`)
			if err := tx.Put(documentNamespace("session_model_input_receipts"), documentPrefix("session")+"op", modelReceiptDocument{OperationID: "op", DriverID: "driver", Payload: payload}); err != nil {
				return err
			}
			if err := tx.Put(documentNamespace("inference_operations"), "op", inferenceOperationDocument{InferenceOperation: providers.InferenceOperation{ID: "op"}, OwnerID: "session", Status: "terminal", AttemptIDs: []string{"attempt"}}); err != nil {
				return err
			}
			if err := tx.Put(documentNamespace("inference_attempts"), "attempt", inferenceAttemptDocument{ID: "attempt", OperationID: "op", SubmissionIDs: []string{"submission"}}); err != nil {
				return err
			}
			return tx.Put(documentNamespace("inference_submissions"), "submission", providers.InferenceSubmissionJournalRecord{ID: "submission", OperationID: "op", AttemptID: "attempt", Provider: "provider", Outcome: providers.InferenceSubmissionFailed, CostState: providers.InferenceCostKnown, ReportedUsage: &providers.TokenUsage{InputTokens: 13, OutputTokens: 7}})
		})
		if err != nil {
			t.Fatal(err)
		}
		restartSessionStore(t, s)
		got, err := s.ReadModelUsageReceipts("session", "driver", "boundary")
		if err != nil {
			t.Fatal(err)
		}
		if got.Status != "complete" || len(got.Receipts) != 1 || got.Receipts[0].Usage.InputTokens != 13 {
			t.Fatalf("receipt: %+v", got)
		}
		again, err := s.ReadModelUsageReceipts("session", "driver", "boundary")
		if err != nil || len(again.Receipts) != 1 {
			t.Fatalf("read is not stable: %+v %v", again, err)
		}
		for _, args := range [][3]string{{"other", "driver", "boundary"}, {"session", "other", "boundary"}, {"session", "driver", "missing"}} {
			r, e := s.ReadModelUsageReceipts(args[0], args[1], args[2])
			if e != nil || r.Status != "unavailable" || len(r.Receipts) != 0 {
				t.Fatalf("boundary leak: %+v %v", r, e)
			}
		}
	})
}

func TestModelRequestIDCannotAuthorizeReplay(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, s *documentStore) {
		if _, err := s.CreateInitialized(Session{ID: "session", CWD: "/workspace"}, nil); err != nil {
			t.Fatal(err)
		}
		record := ModelInputReceiptRecord{OperationID: "first", ContractVersion: 1, DriverID: "driver", DriverVersion: "1", Payload: json.RawMessage(`{"request_id":"boundary"}`)}
		if err := s.SaveModelInputReceipt("session", record); err != nil {
			t.Fatal(err)
		}
		record.OperationID = "second"
		if err := s.SaveModelInputReceipt("session", record); err == nil {
			t.Fatal("duplicate request correlation admitted a new operation")
		}
	})
}
