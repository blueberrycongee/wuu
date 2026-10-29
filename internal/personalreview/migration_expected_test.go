package personalreview

import (
	"context"
	"errors"
	"os"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// Operator apply must bind the exact logical source, not a stale preview or raw
// SQLite file checksum that omits committed WAL rows. Import and marker share CAS.
func TestLegacyMigrationExpectedDigestFreezesTheImportedSnapshot(t *testing.T) {
	ctx := context.Background()
	f := newFixture(t)
	initial, err := InspectLegacyMigration(ctx, f.storePath, nil)
	if err != nil || initial.SourceDigest == "" {
		t.Fatalf("inspect: %+v %v", initial, err)
	}
	if _, err := f.store.GetSettings(ctx, "added-after-preview"); err != nil {
		t.Fatal(err)
	}
	docs, err := persistence.Open(ctx, t.TempDir(), "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer docs.Close()
	if _, err := MigrateLegacyExpected(ctx, f.storePath, docs, initial.SourceDigest); !errors.Is(err, ErrLegacySourceChanged) {
		t.Fatalf("stale preview imported: %v", err)
	}
	current, err := InspectLegacyMigration(ctx, f.storePath, nil)
	if err != nil || current.SourceDigest == initial.SourceDigest {
		t.Fatalf("logical change missed: %+v %v", current, err)
	}
	sourceBefore, err := os.ReadFile(f.storePath)
	if err != nil {
		t.Fatal(err)
	}
	result, err := MigrateLegacyExpected(ctx, f.storePath, docs, current.SourceDigest)
	if err != nil || result.SourceDigest != current.SourceDigest {
		t.Fatalf("apply: %+v %v", result, err)
	}
	if err := CheckLegacyMigration(ctx, f.storePath, docs); err != nil {
		t.Fatal("committed marker missing", err)
	}
	sourceAfter, err := os.ReadFile(f.storePath)
	if err != nil || string(sourceBefore) != string(sourceAfter) {
		t.Fatal("migration modified legacy source", err)
	}
	if _, err := MigrateLegacyExpected(ctx, f.storePath, docs, current.SourceDigest); err == nil {
		t.Fatal("apply silently overwrote populated selected ledger")
	}
}
