package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Reusing a normalized write must not retain caller-owned input or expose its
// private value through a read from an abandoned CAS attempt.
func TestPreparedWriteOwnsValuesAcrossRetries(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	raw := json.RawMessage(` { "large":9007199254740993, "html":"<>&", "number":1e+00 } `)
	want, err := json.Marshal(raw)
	if err != nil {
		t.Fatal(err)
	}
	prepared, err := PrepareWrite("prepared", "one", raw)
	if err != nil {
		t.Fatal(err)
	}
	for i := range raw {
		raw[i] = ' '
	}
	attempts := 0
	if err := Update(ctx, store, func(tx *Tx) error {
		attempts++
		if err := tx.PutPrepared(prepared); err != nil {
			return err
		}
		rows, err := tx.Read(Query{Namespace: "prepared"})
		if err != nil || len(rows) != 1 || !bytes.Equal(rows[0].Value, want) {
			t.Fatalf("prepared value changed on attempt %d: %+v, %v", attempts, rows, err)
		}
		for i := range rows[0].Value {
			rows[0].Value[i] = ' '
		}
		if attempts == 1 {
			return ErrConflict
		}
		return tx.Put("marker", "complete", true)
	}); err != nil {
		t.Fatal(err)
	}
	view, err := store.Read(ctx, Query{Namespace: "prepared"})
	if err != nil || attempts != 2 || len(view.Documents) != 1 || !bytes.Equal(view.Documents[0].Value, want) {
		t.Fatalf("prepared retry lost its snapshot: %+v, attempts=%d, err=%v", view, attempts, err)
	}
	if _, err := PrepareWrite("prepared", "bad", json.RawMessage(`{"broken":`)); err == nil {
		t.Fatal("invalid prepared JSON was accepted")
	}
}

type preparedUncertainDocuments struct {
	Documents
	commits int
}

func (d *preparedUncertainDocuments) Commit(ctx context.Context, tx Transaction) (Receipt, error) {
	d.commits++
	receipt, err := d.Documents.Commit(ctx, tx)
	if err != nil {
		return receipt, err
	}
	return Receipt{}, errors.New("prepared response lost")
}

func TestPreparedWriteRetainsExactUncertainIntent(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	documents := &preparedUncertainDocuments{Documents: store}
	err = Update(ctx, documents, func(tx *Tx) error {
		prepared, err := PrepareWrite("prepared", "one", json.RawMessage(` { "fraction":1e+00, "html":"<>&" } `))
		if err != nil {
			return err
		}
		return tx.PutPrepared(prepared)
	})
	var uncertain *CommitError
	if !errors.As(err, &uncertain) || documents.commits != 1 {
		t.Fatalf("uncertain result replayed: commits=%d, err=%v", documents.commits, err)
	}
	for i := 0; i < 2; i++ {
		receipt, err := store.Commit(ctx, uncertain.Transaction)
		if err != nil || receipt.ID != uncertain.Transaction.ID || receipt.Revision != 1 {
			t.Fatalf("exact receipt lost: %+v, %v", receipt, err)
		}
	}
	snapshot, err := store.Export(ctx)
	if err != nil || len(snapshot.Receipts) != 1 {
		t.Fatalf("prepared transaction replayed: receipts=%d, err=%v", len(snapshot.Receipts), err)
	}
}
