package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"testing"
)

// Inspections fence every handle and survive a provider restart. Unrelated
// journal writes remain available; unknown/late acquires cannot escape release.
func TestInspectionFencesSelectedNamespaceAcrossRestart(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			store, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			request := InspectionRequest{ID: "one", Namespaces: []string{"authority"}}
			if _, err := store.AcquireInspection(ctx, request); err != nil {
				t.Fatal(err)
			}
			if err := store.Close(); err != nil {
				t.Fatal(err)
			}
			store, err = Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			write := func(namespace string) error {
				return Update(ctx, store, func(tx *Tx) error { return tx.Put(namespace, "key", true) })
			}
			if err := write("authority"); !errors.Is(err, ErrInspectionActive) {
				t.Fatalf("guard lost on restart: %v", err)
			}
			if err := write("journal"); err != nil {
				t.Fatal(err)
			}
			if err := Migrate(ctx, dir, kind, map[string]string{"sqlite": "jsonl", "jsonl": "sqlite"}[kind]); !errors.Is(err, ErrInspectionActive) {
				t.Fatalf("migration bypassed guard: %v", err)
			}
			if err := store.ReleaseInspection(ctx, request); err != nil {
				t.Fatal(err)
			}
			if err := store.ReleaseInspection(ctx, request); err != nil {
				t.Fatal(err)
			}
			if err := write("authority"); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AcquireInspection(ctx, request); !errors.Is(err, ErrInspectionReleased) {
				t.Fatalf("released token revived: %v", err)
			}
			late := InspectionRequest{ID: "late", Namespaces: []string{"authority"}}
			if err := store.ReleaseInspection(ctx, late); err != nil {
				t.Fatal(err)
			}
			if _, err := store.AcquireInspection(ctx, late); !errors.Is(err, ErrInspectionReleased) {
				t.Fatalf("late acquire revived: %v", err)
			}
		})
	}
}

func TestInspectionUnknownAcquireNeverEntersEffect(t *testing.T) {
	calls := []string{}
	entered := false
	client := NewClient(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		calls = append(calls, method)
		if method == "acquire-inspection" {
			return nil, errors.New("reply lost")
		}
		return json.RawMessage(`{}`), nil
	})
	err := WithInspection(context.Background(), client, []string{"authority"}, func() error { entered = true; return nil })
	if err == nil || entered || len(calls) != 2 || calls[1] != "release-inspection" {
		t.Fatalf("unknown acquire admitted effect: entered=%v calls=%v err=%v", entered, calls, err)
	}
}

func TestInspectionExplicitReconciliationIsAuditedAndExact(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	request := InspectionRequest{ID: "crashed-holder", Namespaces: []string{"authority"}}
	if _, err := store.AcquireInspection(ctx, request); err != nil {
		t.Fatal(err)
	}
	active, err := store.ListInspections(ctx)
	if err != nil || len(active.Inspections) != 1 || active.Inspections[0].ID != request.ID {
		t.Fatalf("undiscoverable guard: %+v %v", active, err)
	}
	resolution := InspectionResolution{InspectionRequest: request, ExpectedOwner: "other", Outcome: "completed", Reason: "verified downstream completion"}
	if _, err := store.ReconcileInspection(ctx, resolution); err == nil {
		t.Fatal("wrong owner reconciled")
	}
	resolution.ExpectedOwner = "local"
	resolution.Outcome = "unknown"
	if _, err := store.ReconcileInspection(ctx, resolution); err == nil {
		t.Fatal("uncertain running effect silently unlocked")
	}
	resolution.Outcome = "completed"
	receipt, err := store.ReconcileInspection(ctx, resolution)
	if err != nil || receipt.ReconciledBy != "local" || receipt.Reason != resolution.Reason {
		t.Fatalf("missing reconciliation audit: %+v %v", receipt, err)
	}
	retry, err := store.ReconcileInspection(ctx, resolution)
	if err != nil || retry.Reason != receipt.Reason {
		t.Fatalf("lost reconcile reply not retryable: %+v %v", retry, err)
	}
	active, err = store.ListInspections(ctx)
	if err != nil || len(active.Inspections) != 0 {
		t.Fatalf("reconciled guard retained: %+v %v", active, err)
	}
	if err := Update(ctx, store, func(tx *Tx) error { return tx.Put("authority", "key", true) }); err != nil {
		t.Fatal(err)
	}
}
