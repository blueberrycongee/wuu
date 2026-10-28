package session

import (
	"context"
	"errors"
	"sync"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

// Admission is durable and immutable, races select one writer, checkpoints
// cannot cross identities, and forks inherit identity without execution state.
func TestDriverBindingAdmission(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *documentStore) {
		if _, err := store.Create("session"); err != nil {
			t.Fatal(err)
		}
		bindings := []pluginprotocol.DriverBinding{
			{Profile: "first", ProviderID: "one", ContractVersion: 1, DriverID: "loop", DriverVersion: "1.0.0"},
			{Profile: "second", ProviderID: "two", ContractVersion: 1, DriverID: "other", DriverVersion: "2.0.0"},
		}
		start := make(chan struct{})
		results := make([]error, 2)
		var wg sync.WaitGroup
		for i := range bindings {
			wg.Go(func() { <-start; results[i] = store.BindDriver("session", bindings[i]) })
		}
		close(start)
		wg.Wait()
		if (results[0] == nil) == (results[1] == nil) {
			t.Fatalf("concurrent bindings = %v", results)
		}
		winner, ok, err := store.LoadDriverBinding("session")
		if err != nil || !ok {
			t.Fatalf("load binding = %+v, %v, %v", winner, ok, err)
		}
		if err := store.BindDriver("session", winner); err != nil {
			t.Fatalf("same binding is not idempotent: %v", err)
		}
		checkpoint := DriverCheckpointRecord{ContractVersion: winner.ContractVersion, DriverID: winner.DriverID, DriverVersion: winner.DriverVersion, State: []byte(`{"phase":"running"}`)}
		if err := store.SaveDriverCheckpoint("session", checkpoint); err != nil {
			t.Fatal(err)
		}
		checkpoint.DriverVersion = "9.0.0"
		if err := store.SaveDriverCheckpoint("session", checkpoint); err == nil {
			t.Fatal("checkpoint replaced another driver version")
		}
		if _, err := store.CreateForkWithMetadata("fork", "", ForkMetadata{ForkedFromID: "session"}); err != nil {
			t.Fatal(err)
		}
		fork, ok, err := store.LoadDriverBinding("fork")
		if err != nil || !ok || fork != winner {
			t.Fatalf("fork binding = %+v, %v, %v", fork, ok, err)
		}
		if _, ok, err := store.LoadDriverCheckpoint("fork"); err != nil || ok {
			t.Fatalf("fork copied live execution state: %v, %v", ok, err)
		}
		ctx, cancel := context.WithCancel(context.Background())
		cancel()
		if err := store.WithContext(ctx).BindDriver("session", winner); !errors.Is(err, context.Canceled) {
			t.Fatalf("canceled admission = %v", err)
		}
		if err := store.BindDriver("missing", winner); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("missing admission = %v", err)
		}
		if _, err := store.Delete("fork"); err != nil {
			t.Fatal(err)
		}
		if _, _, err := store.LoadDriverBinding("fork"); !errors.Is(err, ErrSessionNotFound) {
			t.Fatalf("deleted binding survived = %v", err)
		}
	})
}

func TestDriverBindingLegacyMigration(t *testing.T) {
	dir := t.TempDir()
	for _, id := range []string{"legacy", "checkpoint", "unknown"} {
		if _, err := Create(dir, id); err != nil {
			t.Fatal(err)
		}
	}
	for _, id := range []string{"checkpoint", "unknown"} {
		driverID := "wuu.default-loop"
		if id == "unknown" {
			driverID = "external-loop"
		}
		if err := SaveDriverCheckpoint(dir, id, DriverCheckpointRecord{ContractVersion: 1, DriverID: driverID, DriverVersion: "1.0.0", State: []byte(`{}`)}); err != nil {
			t.Fatal(err)
		}
	}
	db, err := openStore(dir)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`DROP TABLE session_driver_bindings`); err != nil {
		t.Fatal(err)
	}
	db.Close()
	for _, id := range []string{"legacy", "checkpoint"} {
		binding, ok, err := LoadDriverBinding(dir, id)
		if err != nil || !ok || binding.Profile != "default" || binding.ProviderID != "wuu.default-loop" || binding.DriverID != "wuu.default-loop" || binding.DriverVersion != "1.0.0" {
			t.Fatalf("legacy binding %s = %+v, %v, %v", id, binding, ok, err)
		}
	}
	unknown, ok, err := LoadDriverBinding(dir, "unknown")
	if err != nil || !ok || unknown.DriverID != "external-loop" || unknown.ProviderID != "" {
		t.Fatalf("migration invented missing provider identity: %+v, %v, %v", unknown, ok, err)
	}
	if _, err := Create(dir, "new"); err != nil {
		t.Fatal(err)
	}
	if _, ok, err := LoadDriverBinding(dir, "new"); err != nil || ok {
		t.Fatalf("migration rebound a new session: %v, %v", ok, err)
	}
}
