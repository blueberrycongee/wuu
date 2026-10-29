package personalmemory

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// A candidate record and terminal receipt are one selected-provider transaction.
// Lost replies and delayed requests must resolve without publishing a second
// record, reviving a tombstone, or allowing an aborted intent to publish later.
func TestJournalUnknownCommitAndAbortFence(t *testing.T) {
	for _, mode := range []string{"committed-reply-lost", "delayed-before-commit"} {
		t.Run(mode, func(t *testing.T) {
			ctx := context.Background()
			docs, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "documents"), "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer docs.Close()
			transport := &uncertainDocuments{Documents: docs}
			journal := NewJournal(transport, "space", "subject")
			intent := Operation{ID: "candidate-call", Digest: "digest", Key: "mem_test", Value: `{"revision":1,"status":"candidate"}`}
			if _, err = journal.Prepare(ctx, intent); err != nil {
				t.Fatal(err)
			}
			transport.mode = mode
			provisional, publishErr := journal.Publish(ctx, intent.ID, intent.Digest, nil, nil)
			err = publishErr
			if err != nil && provisional.State == "committed" {
				t.Fatal("unacknowledged transaction exposed a committed outcome")
			}
			if err == nil {
				t.Fatal("expected uncertain commit")
			}
			var commit *persistence.CommitError
			if !errors.As(err, &commit) {
				t.Fatal(err)
			}
			transport.mode = ""
			resolved, err := journal.Abort(ctx, intent.ID, intent.Digest)
			if err != nil {
				t.Fatal(err)
			}
			if mode == "committed-reply-lost" {
				if resolved.State != "committed" || resolved.Result != intent.Value {
					t.Fatalf("lost committed receipt %+v", resolved)
				}
			} else {
				if resolved.State != "not-committed" {
					t.Fatalf("missing abort receipt %+v", resolved)
				}
				if _, err = docs.Commit(ctx, commit.Transaction); !errors.Is(err, persistence.ErrConflict) {
					t.Fatalf("late original commit crossed abort: %v", err)
				}
				if _, err = journal.Publish(ctx, intent.ID, intent.Digest, nil, nil); !errors.Is(err, ErrAborted) {
					t.Fatalf("aborted operation replay: %v", err)
				}
			}
			records, err := journal.Records(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if (len(records) == 1) != (mode == "committed-reply-lost") {
				t.Fatalf("records %+v", records)
			}
			reopened := NewJournal(docs, "space", "subject")
			receipt, err := reopened.Operation(ctx, intent.ID)
			if err != nil || receipt.State != resolved.State {
				t.Fatalf("recovery %+v %v", receipt, err)
			}
		})
	}
}

type uncertainDocuments struct {
	persistence.Documents
	mode string
}

func (s *uncertainDocuments) ClaimNamespaces(ctx context.Context, names []string) error {
	return persistence.ClaimNamespaces(ctx, s.Documents, names)
}
func (s *uncertainDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	if s.mode == "" {
		return s.Documents.Commit(ctx, tx)
	}
	if s.mode == "committed-reply-lost" {
		if _, err := s.Documents.Commit(ctx, tx); err != nil {
			return persistence.Receipt{}, err
		}
	}
	return persistence.Receipt{}, errors.New("synthetic transport lost")
}

func TestJournalSingleAuthorityAndCollision(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	j := NewJournal(docs, "space", "subject")
	first := Operation{ID: "one", Digest: "d1", Key: "id", Value: `{"revision":1,"status":"candidate"}`}
	if _, err = j.Prepare(ctx, first); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Publish(ctx, "one", "d1", nil, nil); err != nil {
		t.Fatal(err)
	}
	collision := first
	collision.Digest = "other"
	if _, err = j.Prepare(ctx, collision); !errors.Is(err, ErrIntentConflict) {
		t.Fatalf("changed intent accepted: %v", err)
	}
	expected := first.Value
	forgotten := Operation{ID: "two", Digest: "d2", Key: "id", Expected: &expected, Value: `{"revision":2,"status":"forgotten","blocked_sources":["source"]}`}
	if _, err = j.Prepare(ctx, forgotten); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Publish(ctx, "two", "d2", map[string]string{"id": `{"revision":9,"status":"active"}`}, nil); err != nil {
		t.Fatal(err)
	}
	receipt, err := j.Operation(ctx, "one")
	if err != nil || !receipt.Redacted || receipt.Value != "" || receipt.Result != "" || receipt.ReceiptDigest == "" {
		t.Fatalf("forgotten content retained in operation %+v %v", receipt, err)
	}
	records, err := j.Records(ctx)
	if err != nil {
		t.Fatal(err)
	}
	var record map[string]any
	if json.Unmarshal([]byte(records["id"]), &record) != nil || record["status"] != "forgotten" {
		t.Fatalf("legacy shadow resurrected journal: %+v", records)
	}
	stale := Operation{ID: "three", Digest: "d3", Key: "id", Expected: &expected, Value: first.Value}
	if _, err = j.Prepare(ctx, stale); err != nil {
		t.Fatal(err)
	}
	if _, err = j.Publish(ctx, "three", "d3", nil, nil); !errors.Is(err, ErrRevisionConflict) {
		t.Fatalf("stale revision: %v", err)
	}
}

func TestJournalRecoveryRecordsReason(t *testing.T) {
	ctx := context.Background()
	docs, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	j := NewJournal(docs, "space", "subject")
	if _, err = j.Prepare(ctx, Operation{ID: "recover", Digest: "exact", Key: "id", Value: "private"}); err != nil {
		t.Fatal(err)
	}
	op, err := j.Reconcile(ctx, "recover", "exact", "Owner verified writers stopped")
	if err != nil || op.RecoveryReason != "Owner verified writers stopped" || op.State != "not-committed" {
		t.Fatalf("missing audit %+v %v", op, err)
	}
}
