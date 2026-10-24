package runtime

import (
	"context"
	"database/sql"
	"errors"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/session"
)

// A completed import retires its legacy source from startup. Even an unreadable
// old source must not gate opening the current document store.
func TestStateInitializationDoesNotReauditRetiredLegacySource(t *testing.T) {
	ctx, dir := context.Background(), t.TempDir()
	source := filepath.Join(dir, "sessions.sqlite3")
	if err := os.WriteFile(source, []byte("retired source"), 0600); err != nil {
		t.Fatal(err)
	}
	documents, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "docs"), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	if err := persistence.Update(ctx, documents, func(tx *persistence.Tx) error {
		return tx.Put("wuu.legacy_sqlite_migrations.v1", "sessions.sqlite3", session.LegacyMigrationResult{Fingerprint: "completed-import"})
	}); err != nil {
		t.Fatal(err)
	}
	if err := initializeSessionState(ctx, dir, documents, session.NewStore(ctx, documents)); err != nil {
		t.Fatal(err)
	}
	if raw, err := os.ReadFile(source); err != nil || string(raw) != "retired source" {
		t.Fatalf("source changed: %v", err)
	}
}

// An old live server must not be mistaken for a migration-ready snapshot. The
// source remains untouched and a later exclusive startup may complete import.
func TestStateMigrationRejectsLiveLegacyWriter(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	source := filepath.Join(dir, "sessions.sqlite3")
	db, err := sql.Open("sqlite", source)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE sessions (id TEXT PRIMARY KEY)`); err != nil {
		t.Fatal(err)
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	before, err := os.ReadFile(source)
	if err != nil {
		t.Fatal(err)
	}
	documents, err := persistence.Open(ctx, filepath.Join(t.TempDir(), "docs"), "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	lease, first, err := session.AcquireAppServerPresence(dir)
	if err != nil || !first {
		t.Fatalf("presence: %v %v", first, err)
	}
	if err := lease.FinalizeStartup(); err != nil {
		t.Fatal(err)
	}
	if err := initializeSessionState(ctx, dir, documents, session.NewStore(ctx, documents)); !errors.Is(err, persistence.ErrMigrationRequired) {
		t.Fatalf("live migration: %v", err)
	}
	if err := lease.Release(); err != nil {
		t.Fatal(err)
	}
	if err := initializeSessionState(ctx, dir, documents, session.NewStore(ctx, documents)); err != nil {
		t.Fatal(err)
	}
	after, err := os.ReadFile(source)
	if err != nil || string(before) != string(after) {
		t.Fatalf("source changed: %v", err)
	}
	lease, _, err = session.AcquireAppServerPresence(dir)
	if err != nil {
		t.Fatal(err)
	}
	if err := lease.FinalizeStartup(); err != nil {
		t.Fatal(err)
	}
	defer lease.Release()
	if err := initializeSessionState(ctx, dir, documents, session.NewStore(ctx, documents)); err != nil {
		t.Fatalf("verified migrated source rejected with peer: %v", err)
	}
}
