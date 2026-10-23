package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"testing"
)

// Transfer must retain receipts, reject corrupt or occupied destinations, and
// never turn a lost authority file into a new empty store. An interrupted
// preparation must leave the published source writable and unchanged.
func TestTransferRecoveryBoundaries(t *testing.T) {
	ctx := context.Background()
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			s, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			tx := Transaction{ID: "create", Writes: []Write{{Namespace: "sessions", Key: "one", Value: json.RawMessage(`{"title":"saved"}`)}}}
			receipt, err := s.Commit(ctx, tx)
			if err != nil {
				t.Fatal(err)
			}
			snapshot, err := s.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target := "jsonl"
			if kind == target {
				target = "sqlite"
			}
			importDir := t.TempDir()
			if err := Import(ctx, importDir, target, snapshot); err != nil {
				t.Fatal(err)
			}
			imported, err := Open(ctx, importDir, target)
			if err != nil {
				t.Fatal(err)
			}
			defer imported.Close()
			if actual, err := imported.Export(ctx); err != nil || !reflect.DeepEqual(snapshot, actual) {
				t.Fatalf("import: %+v, %v", actual, err)
			}
			if actual, err := imported.Commit(ctx, tx); err != nil || actual != receipt {
				t.Fatalf("imported retry: %+v, %v", actual, err)
			}
			if err := Import(ctx, importDir, target, snapshot); !errors.Is(err, ErrConflict) {
				t.Fatalf("overwrote existing store: %v", err)
			}
			bad := snapshot
			bad.Receipts = nil
			if err := Import(ctx, t.TempDir(), target, bad); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("accepted missing receipts: %v", err)
			}
			// Simulate interruption after durable target preparation, before the
			// atomic authority switch. No consumer may select this generation.
			if _, err := createGeneration(ctx, dir, target, snapshot); err != nil {
				t.Fatal(err)
			}
			if _, err := s.Commit(ctx, Transaction{ID: "after-interruption", ExpectedRevision: 1, Writes: []Write{{Namespace: "sessions", Key: "two", Value: json.RawMessage(`{}`)}}}); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, dir, kind, target); err != nil {
				t.Fatal(err)
			}
			migrated, err := Open(ctx, dir, target)
			if err != nil {
				t.Fatal(err)
			}
			view, err := migrated.Read(ctx, Query{Namespace: "sessions"})
			migrated.Close()
			if err != nil || view.Revision != 2 || len(view.Documents) != 2 {
				t.Fatalf("migration resumed stale target: %+v, %v", view, err)
			}
			if err := os.Remove(filepath.Join(dir, "authority.json")); err != nil {
				t.Fatal(err)
			}
			if reset, err := Open(ctx, dir, target); !errors.Is(err, ErrCorrupt) {
				if reset != nil {
					reset.Close()
				}
				t.Fatalf("missing authority reset existing data: %v", err)
			}
		})
	}
}
