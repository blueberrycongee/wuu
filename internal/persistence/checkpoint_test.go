package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"
)

// Admission checkpoints survive reopen, follow writes from another handle, and
// recover a missing/stale cache without changing durable transaction receipts.
func TestDurableCheckpointFollowsCommitsAndReopen(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx, dir := context.Background(), t.TempDir()
			store, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			initial, err := store.Checkpoint(ctx)
			if err != nil || initial.Revision != 0 {
				t.Fatalf("empty: %+v %v", initial, err)
			}
			writer, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			tx := Transaction{ID: "write", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`{"value":"preserve <bytes>"}`)}}}
			if _, err := writer.Commit(ctx, tx); err != nil {
				t.Fatal(err)
			}
			want, err := CaptureCheckpoint(ctx, writer)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := store.Checkpoint(ctx); err != nil || got != want {
				t.Fatalf("shared commit: %+v %v", got, err)
			}
			if _, err := writer.Commit(ctx, tx); err != nil {
				t.Fatal(err)
			}
			if got, err := store.Checkpoint(ctx); err != nil || got != want {
				t.Fatalf("receipt retry: %+v %v", got, err)
			}
			cache := filepath.Join(dir, store.binding.Generation, "checkpoint.json")
			if err := os.Remove(cache); err != nil {
				t.Fatal(err)
			}
			store.Close()
			store, err = Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if got, err := store.Checkpoint(ctx); err != nil || got != want {
				t.Fatalf("missing cache: %+v %v", got, err)
			}
			// Simulate an older writer/crash that advanced storage before publishing its cache.
			if _, err := store.backend.commit(ctx, Transaction{ID: "old-writer", ExpectedRevision: 1, Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`null`)}}}, "0123456789abcdef0123456789abcdef0123456789abcdef0123456789abcdef"); err != nil {
				t.Fatal(err)
			}
			want, err = CaptureCheckpoint(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			if got, err := store.Checkpoint(ctx); err != nil || got != want {
				t.Fatalf("stale cache: %+v %v", got, err)
			}
		})
	}
}

// Admission must compare both durable documents and operation receipts. An
// equal revision or a copy of the dataset's identity is not sufficient.
func TestCheckpointRejectsDivergentStateAtSameRevision(t *testing.T) {
	snapshot := Snapshot{Version: 1, Revision: 1, Documents: []Document{{Namespace: "n", Key: "one", Value: json.RawMessage(`{"value":1}`)}, {Namespace: "n", Key: "two", Value: json.RawMessage(`true`)}}, Receipts: []CommitRecord{{Receipt: Receipt{ID: "operation", Revision: 1}, Digest: strings.Repeat("1", 64)}}}
	want, err := SnapshotCheckpoint(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	reversed := snapshot
	reversed.Documents = []Document{snapshot.Documents[1], snapshot.Documents[0]}
	got, err := SnapshotCheckpoint(reversed)
	if err != nil || got != want {
		t.Fatalf("document ordering changed checkpoint: %+v, %v", got, err)
	}
	for _, mutate := range []func(*Snapshot){
		func(s *Snapshot) { s.Documents[0].Value = json.RawMessage(`{"value":2}`) },
		func(s *Snapshot) { s.Receipts[0].ID = "other-operation" },
		func(s *Snapshot) { s.Receipts[0].Digest = strings.Repeat("2", 64) },
	} {
		changed := snapshot
		changed.Documents = append([]Document{}, snapshot.Documents...)
		changed.Receipts = append([]CommitRecord{}, snapshot.Receipts...)
		mutate(&changed)
		got, err := SnapshotCheckpoint(changed)
		if err != nil || got == want {
			t.Fatalf("divergence accepted: %+v, %v", got, err)
		}
	}
	missing := snapshot
	missing.Receipts = nil
	if _, err := SnapshotCheckpoint(missing); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("missing receipts: %v", err)
	}
}

// Planning the durable host checkpoint reads only the keys a transaction can
// replace. Its predicted digest must equal an independent full export after CAS.
func TestNextCheckpointUsesOnlyChangedKeys(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			writes := make([]Write, 300)
			for i := range writes {
				writes[i] = Write{Namespace: "n", Key: fmt.Sprintf("key-%03d", i), Value: json.RawMessage(`{"original":true}`)}
			}
			if _, err := store.Commit(ctx, Transaction{ID: "seed", Writes: writes}); err != nil {
				t.Fatal(err)
			}
			before, err := CaptureCheckpoint(ctx, store)
			if err != nil {
				t.Fatal(err)
			}
			tx := Transaction{ID: "change", ExpectedRevision: before.Revision, Writes: []Write{{Namespace: "n", Key: "key-000", Value: json.RawMessage(` { "updated" : true } `)}, {Namespace: "n", Key: "key-001", Delete: true}, {Namespace: "other", Key: "inserted", Value: json.RawMessage(`"new"`)}, {Namespace: "other", Key: "absent", Delete: true}}}
			bounded := &checkpointReads{Documents: store}
			after, err := NextCheckpoint(ctx, bounded, before, tx)
			if err != nil {
				t.Fatal(err)
			}
			if bounded.keys != 4 || bounded.documents != 2 {
				t.Fatalf("read unrelated state: %+v", bounded)
			}
			if _, err := store.Commit(ctx, tx); err != nil {
				t.Fatal(err)
			}
			actual, err := CaptureCheckpoint(ctx, store)
			if err != nil || actual != after {
				t.Fatalf("predicted %+v actual %+v: %v", after, actual, err)
			}
			if err := VerifyCheckpoint(ctx, store, after); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCheckpoint(ctx, store, before); !errors.Is(err, ErrMigrationRequired) {
				t.Fatalf("stale checkpoint admitted: %v", err)
			}
			tx.ID = "stale"
			if _, err := NextCheckpoint(ctx, store, before, tx); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale planner: %v", err)
			}
		})
	}
}

type checkpointReads struct {
	Documents
	keys, documents int
}

func (c *checkpointReads) Read(ctx context.Context, q Query) (View, error) {
	if q.Keys == nil || q.AtRevision == nil {
		return View{}, errors.New("checkpoint planner attempted unbounded or unpinned read")
	}
	c.keys += len(q.Keys)
	view, err := c.Documents.Read(ctx, q)
	c.documents += len(view.Documents)
	return view, err
}

// Even an exact exported clone cannot replace a writer while its predecessor
// remains live. A second handle of the same authority must observe the probe.
func TestSharedAuthorityRejectsAnIdenticalIndependentCopy(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			current, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer current.Close()
			if _, err := current.Commit(ctx, Transaction{ID: "business", Writes: []Write{{Namespace: "business", Key: "key", Value: json.RawMessage(`"keep"`)}}}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := current.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			copyDir := t.TempDir()
			if err := Import(ctx, copyDir, kind, snapshot); err != nil {
				t.Fatal(err)
			}
			copied, err := Open(ctx, copyDir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer copied.Close()
			if err := VerifySharedAuthority(ctx, current, copied); !errors.Is(err, ErrMigrationRequired) {
				t.Fatalf("independent copy accepted: %v", err)
			}
			shared, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer shared.Close()
			if err := VerifySharedAuthority(ctx, current, shared); err != nil {
				t.Fatalf("shared authority rejected: %v", err)
			}
			after, err := current.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if !reflect.DeepEqual(snapshot.Documents, after.Documents) || after.Revision != snapshot.Revision+4 {
				t.Fatalf("probe changed business state or lost receipts: %+v", after)
			}
			if receipt, err := current.Commit(ctx, Transaction{ID: "business", Writes: []Write{{Namespace: "business", Key: "key", Value: json.RawMessage(`"keep"`)}}}); err != nil || receipt.Revision != 1 {
				t.Fatalf("business receipt lost: %+v, %v", receipt, err)
			}
		})
	}
}
