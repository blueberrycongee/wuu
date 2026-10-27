package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"reflect"
	"sync"
	"testing"
)

// Both providers must preserve these failure boundaries: a failed CAS writes
// nothing; retries after a lost acknowledgement do not replay; reused operation
// IDs cannot change intent; cancellation before admission writes nothing;
// concurrent writers cannot lose an update; reopening rebuilds the same state.
func TestDurableStoreContract(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			s, err := Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			initial, err := s.Read(ctx, Query{Namespace: "sessions"})
			if err != nil || initial.Revision != 0 || len(initial.Documents) != 0 {
				t.Fatalf("initial: %+v, %v", initial, err)
			}
			tx := Transaction{ID: "create", ExpectedRevision: 0, Writes: []Write{
				{Namespace: "sessions", Key: "one", Value: json.RawMessage(`{"title":"first"}`)},
				{Namespace: "history", Key: "one/1", Value: json.RawMessage(`{"content":"hello"}`)},
			}}
			receipt, err := s.Commit(ctx, tx)
			if err != nil || receipt.Revision != 1 {
				t.Fatalf("commit: %+v, %v", receipt, err)
			}
			stale := Transaction{ID: "stale", Writes: []Write{{Namespace: "sessions", Key: "two", Value: json.RawMessage(`{}`)}}}
			if _, err := s.Commit(ctx, stale); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale commit: %v", err)
			}
			retry, err := s.Commit(ctx, tx)
			if err != nil || retry != receipt {
				t.Fatalf("retry: %+v, %v", retry, err)
			}
			changed := tx
			changed.ExpectedRevision = 1
			if _, err := s.Commit(ctx, changed); !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("changed intent: %v", err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := s.Commit(canceled, Transaction{ID: "cancel", ExpectedRevision: 1, Writes: tx.Writes}); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled: %v", err)
			}
			all, err := s.Export(ctx)
			if err != nil || all.Revision != 1 || len(all.Documents) != 2 || len(all.Receipts) != 1 {
				t.Fatalf("export: %+v, %v", all, err)
			}
			if err := s.Close(); err != nil {
				t.Fatal(err)
			}
			s, err = Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			restored, err := s.Export(ctx)
			if err != nil || !reflect.DeepEqual(all, restored) {
				t.Fatalf("reopen: %+v, %v", restored, err)
			}
			if retry, err := s.Commit(ctx, tx); err != nil || retry != receipt {
				t.Fatalf("durable retry: %+v, %v", retry, err)
			}
			if _, err := s.Commit(ctx, Transaction{ID: "delete", ExpectedRevision: 1, Writes: []Write{{Namespace: "history", Key: "one/1", Delete: true}}}); err != nil {
				t.Fatal(err)
			}
			page, err := s.Read(ctx, Query{Namespace: "history"})
			if err != nil || page.Revision != 2 || len(page.Documents) != 0 {
				t.Fatalf("delete: %+v, %v", page, err)
			}
		})
	}
}

func TestConcurrentWritersAndMigration(t *testing.T) {
	for _, backend := range []string{"sqlite", "jsonl"} {
		t.Run(backend, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			a, err := Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer a.Close()
			b, err := Open(ctx, dir, backend)
			if err != nil {
				t.Fatal(err)
			}
			defer b.Close()
			start := make(chan struct{})
			results := make(chan error, 2)
			var wg sync.WaitGroup
			for i, s := range []*Store{a, b} {
				wg.Add(1)
				go func() {
					defer wg.Done()
					<-start
					_, err := s.Commit(ctx, Transaction{ID: []string{"a", "b"}[i], Writes: []Write{{Namespace: "sessions", Key: "one", Value: json.RawMessage(`{}`)}}})
					results <- err
				}()
			}
			close(start)
			wg.Wait()
			close(results)
			var success, conflict int
			for err := range results {
				if err == nil {
					success++
				} else if errors.Is(err, ErrConflict) {
					conflict++
				} else {
					t.Fatal(err)
				}
			}
			if success != 1 || conflict != 1 {
				t.Fatalf("success=%d conflict=%d", success, conflict)
			}
			before, err := a.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			target := "jsonl"
			if backend == target {
				target = "sqlite"
			}
			if _, err := Open(ctx, dir, target); !errors.Is(err, ErrMigrationRequired) {
				t.Fatalf("implicit switch: %v", err)
			}
			if err := Migrate(ctx, dir, backend, target); err != nil {
				t.Fatal(err)
			}
			if _, err := a.Commit(ctx, Transaction{ID: "late", ExpectedRevision: 1, Writes: []Write{{Namespace: "sessions", Key: "late", Value: json.RawMessage(`{}`)}}}); !errors.Is(err, ErrBindingChanged) {
				t.Fatalf("old writer: %v", err)
			}
			c, err := Open(ctx, dir, target)
			if err != nil {
				t.Fatal(err)
			}
			defer c.Close()
			after, err := c.Export(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("migration: %+v, %v", after, err)
			}
		})
	}
}

func TestJSONLRecoveryRejectsCorruptionAndDiscardsTornTail(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	_, err = s.Commit(ctx, Transaction{ID: "one", Writes: []Write{{Namespace: "sessions", Key: "one", Value: json.RawMessage(`{}`)}}})
	if err != nil {
		t.Fatal(err)
	}
	binding := s.binding
	s.Close()
	path := filepath.Join(dir, binding.Generation, "state.jsonl")
	f, err := os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := f.WriteString(`{"torn":`); err != nil {
		t.Fatal(err)
	}
	f.Close()
	s, err = Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	state, err := s.Export(ctx)
	if err != nil || state.Revision != 1 || len(state.Documents) != 1 {
		t.Fatalf("recovered: %+v, %v", state, err)
	}
	s.Close()
	f, err = os.OpenFile(path, os.O_WRONLY|os.O_APPEND, 0)
	if err != nil {
		t.Fatal(err)
	}
	f.WriteString("{\"corrupt\":true}\n")
	f.Close()
	if _, err := Open(ctx, dir, "jsonl"); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("corruption: %v", err)
	}
}
