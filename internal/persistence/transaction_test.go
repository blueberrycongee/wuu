package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"sync"
	"testing"
)

func TestDomainTransactionPutSnapshotsMutableInput(t *testing.T) {
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
	value := map[string]int{"before": 1}
	if err := Update(ctx, store, func(tx *Tx) error {
		if err := tx.Put("snapshot", "raw", raw); err != nil {
			return err
		}
		if err := tx.Put("snapshot", "map", value); err != nil {
			return err
		}
		for i := range raw {
			raw[i] = ' '
		}
		value["before"] = 2
		return nil
	}); err != nil {
		t.Fatal(err)
	}
	view, err := store.Read(ctx, Query{Namespace: "snapshot"})
	if err != nil || len(view.Documents) != 2 || string(view.Documents[0].Value) != `{"before":1}` || !bytes.Equal(view.Documents[1].Value, want) {
		t.Fatalf("Put did not snapshot its input: %+v, %v", view, err)
	}
}

// A domain transaction must not combine namespaces from different revisions,
// lose concurrent updates, leak its buffered writes, or retry an uncertain
// commit. These are the boundaries used by history plus tool projection and by
// execution admission plus runtime recovery.
func TestDomainTransactionIsolation(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			store, err := Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if err := Update(ctx, store, func(tx *Tx) error {
				if err := tx.Put("sessions", "one", 0); err != nil {
					return err
				}
				return tx.Put("tools", "one", 0)
			}); err != nil {
				t.Fatal(err)
			}
			firstRead := make(chan struct{})
			advance := make(chan struct{})
			var once sync.Once
			attempts := 0
			done := make(chan error, 1)
			go func() {
				done <- Update(ctx, store, func(tx *Tx) error {
					attempts++
					var count int
					if _, err := tx.Get("sessions", "one", &count); err != nil {
						return err
					}
					once.Do(func() { close(firstRead); <-advance })
					var projected int
					if _, err := tx.Get("tools", "one", &projected); err != nil {
						return err
					}
					if count != projected {
						return errors.New("mixed revision")
					}
					if err := tx.Put("sessions", "one", count+1); err != nil {
						return err
					}
					return tx.Put("tools", "one", projected+1)
				})
			}()
			<-firstRead
			err = Update(ctx, store, func(tx *Tx) error {
				if err := tx.Put("sessions", "one", 1); err != nil {
					return err
				}
				return tx.Put("tools", "one", 1)
			})
			close(advance)
			if err != nil {
				t.Fatal(err)
			}
			if err := <-done; err != nil || attempts != 2 {
				t.Fatalf("retry result: %v, attempts %d", err, attempts)
			}
			view, err := store.Read(ctx, Query{Namespace: "sessions"})
			if err != nil || len(view.Documents) != 1 || string(view.Documents[0].Value) != "2" {
				t.Fatalf("lost update: %+v, %v", view, err)
			}
			before := view.Revision
			rejected := errors.New("domain admission rejected")
			err = Update(ctx, store, func(tx *Tx) error {
				if err := tx.Put("sessions", "two", map[string]int{"current": 3}); err != nil {
					return err
				}
				target := map[string]int{"stale": 9}
				if _, err := tx.Get("sessions", "two", &target); err != nil {
					return err
				}
				if _, stale := target["stale"]; stale || target["current"] != 3 {
					t.Errorf("get merged a previous read into current state: %+v", target)
				}
				if err := tx.Delete("sessions", "one"); err != nil {
					return err
				}
				read, err := tx.Read(Query{Namespace: "sessions"})
				if err != nil || len(read) != 1 || read[0].Key != "two" {
					t.Errorf("transaction did not read its writes: %+v, %v", read, err)
				}
				return rejected
			})
			if !errors.Is(err, rejected) {
				t.Fatal(err)
			}
			view, err = store.Read(ctx, Query{Namespace: "sessions"})
			if err != nil || view.Revision != before || len(view.Documents) != 1 || view.Documents[0].Key != "one" {
				t.Fatalf("aborted writes leaked: %+v, %v", view, err)
			}
		})
	}
}

func TestDomainTransactionDoesNotReplayUncertainCommit(t *testing.T) {
	ctx := context.Background()
	store, err := Open(ctx, t.TempDir(), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	uncertain := errors.New("transport response lost")
	calls := 0
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		switch method {
		case "read":
			var query Query
			if err := json.Unmarshal(raw, &query); err != nil {
				return nil, err
			}
			view, err := store.Read(ctx, query)
			if err != nil {
				return nil, err
			}
			return json.Marshal(view)
		case "commit":
			calls++
			var transaction Transaction
			if err := json.Unmarshal(raw, &transaction); err != nil {
				return nil, err
			}
			if _, err := store.Commit(ctx, transaction); err != nil {
				return nil, err
			}
			return nil, uncertain
		default:
			return nil, errors.New("unexpected method")
		}
	})
	err = Update(ctx, client, func(tx *Tx) error { return tx.Put("sessions", "one", true) })
	if !errors.Is(err, uncertain) || calls != 1 {
		t.Fatalf("uncertain commit replayed: %v, calls %d", err, calls)
	}
	var commitErr *CommitError
	if !errors.As(err, &commitErr) {
		t.Fatalf("uncertain transaction was lost: %v", err)
	}
	receipt, err := store.Commit(ctx, commitErr.Transaction)
	if err != nil || receipt.Revision != 1 {
		t.Fatalf("could not recover original receipt: %+v, %v", receipt, err)
	}
	view, err := store.Read(ctx, Query{Namespace: "sessions"})
	if err != nil || len(view.Documents) != 1 || view.Revision != 1 {
		t.Fatalf("uncertain commit not observable: %+v, %v", view, err)
	}
}
