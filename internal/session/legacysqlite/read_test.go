package legacysqlite

import (
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"math"
	"path/filepath"
	"strings"
	"testing"
)

// Fingerprints are persisted in migration markers. A different JSON byte stream
// would reject an unchanged legacy database after upgrading the reader.
func TestReadFingerprintMatchesLegacyJSON(t *testing.T) {
	for _, fixture := range []string{"no_tables", "empty_table", "ordered_typed_rows"} {
		t.Run(fixture, func(t *testing.T) {
			path := filepath.Join(t.TempDir(), "sessions.sqlite3")
			db, err := sql.Open("sqlite", path)
			if err != nil {
				t.Fatal(err)
			}
			defer db.Close()
			if _, err := db.Exec("PRAGMA user_version = 1"); err != nil {
				t.Fatal(err)
			}
			if fixture != "no_tables" {
				if _, err := db.Exec(`CREATE TABLE "z<&表" (z_blob BLOB, a_blob BLOB, "text""column" TEXT, nullable, integer_value INTEGER, real_value REAL)`); err != nil {
					t.Fatal(err)
				}
			}
			if fixture == "ordered_typed_rows" {
				if _, err := db.Exec(`CREATE TABLE a_empty (value)`); err != nil {
					t.Fatal(err)
				}
				for _, row := range []struct {
					id          int64
					blob, empty []byte
					text        string
					integer     int64
					real        float64
				}{
					{9, []byte{0, 255, 1}, []byte{}, "quote\" slash\\ controls\n\t\x00 <>& 中文 \u2028\u2029", math.MaxInt64, 1e-7},
					{-2, nil, []byte{}, string([]byte{'b', 0xff, 'a', 0xc0}), math.MinInt64, math.Copysign(0, -1)},
					{4, []byte("same"), nil, "c2FtZQ==", 9007199254740993, 1e21},
				} {
					if _, err := db.Exec(`INSERT INTO "z<&表" (rowid, z_blob, a_blob, "text""column", nullable, integer_value, real_value) VALUES (?, ?, ?, ?, NULL, ?, ?)`, row.id, row.blob, row.empty, row.text, row.integer, row.real); err != nil {
						t.Fatal(err)
					}
				}
			}
			if err := db.Close(); err != nil {
				t.Fatal(err)
			}
			for pass := 0; pass < 2; pass++ {
				snapshot, exists, err := Read(context.Background(), path)
				if err != nil || !exists {
					t.Fatalf("read: exists=%v, err=%v", exists, err)
				}
				// This is the pre-streaming persisted fingerprint algorithm.
				raw, err := json.Marshal(snapshot)
				if err != nil {
					t.Fatal(err)
				}
				digest := sha256.Sum256(raw)
				if want := hex.EncodeToString(digest[:]); snapshot.Fingerprint != want {
					t.Fatalf("fingerprint changed: got %s, want %s", snapshot.Fingerprint, want)
				}
				fingerprint, exists, err := Fingerprint(context.Background(), path)
				if err != nil || !exists || fingerprint != snapshot.Fingerprint {
					t.Fatalf("streaming fingerprint: got %s, exists=%v, err=%v", fingerprint, exists, err)
				}
			}
		})
	}
}

func TestReadFingerprintDistinguishesSQLiteStorageTypes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (value); INSERT INTO records VALUES ('c2FtZQ==')`); err != nil {
		t.Fatal(err)
	}
	text, _, err := Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`UPDATE records SET value = ?`, []byte("same")); err != nil {
		t.Fatal(err)
	}
	blob, _, err := Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	if text.Fingerprint == blob.Fingerprint {
		t.Fatal("text and BLOB with identical JSON values must not share a migration fingerprint")
	}
}

func TestReadFingerprintRejectsUnencodableSQLiteValue(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (value REAL)`); err != nil {
		t.Fatal(err)
	}
	if _, err := db.Exec(`INSERT INTO records VALUES (?)`, math.Inf(1)); err != nil {
		t.Fatal(err)
	}
	snapshot, exists, err := Read(context.Background(), path)
	var unsupported *json.UnsupportedValueError
	if !errors.As(err, &unsupported) || exists || snapshot.Fingerprint != "" {
		t.Fatalf("unencodable value: exists=%v, fingerprint=%q, err=%v", exists, snapshot.Fingerprint, err)
	}
	fingerprint, exists, err := Fingerprint(context.Background(), path)
	if !errors.As(err, &unsupported) || exists || fingerprint != "" {
		t.Fatalf("unencodable streaming value: exists=%v, fingerprint=%q, err=%v", exists, fingerprint, err)
	}
}

func TestFingerprintMissingSource(t *testing.T) {
	fingerprint, exists, err := Fingerprint(context.Background(), filepath.Join(t.TempDir(), "missing.sqlite3"))
	if err != nil || exists || fingerprint != "" {
		t.Fatalf("missing source: fingerprint=%q, exists=%v, err=%v", fingerprint, exists, err)
	}
}

// A concurrent WAL writer must not split the fingerprint across database versions.
func TestReadFingerprintUsesOneSnapshot(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`PRAGMA journal_mode=WAL; CREATE TABLE a (value); INSERT INTO a VALUES ('before'); CREATE TABLE z (value); INSERT INTO z VALUES ('before')`); err != nil {
		t.Fatal(err)
	}
	before, _, err := Read(context.Background(), path)
	if err != nil {
		t.Fatal(err)
	}
	snapshot, exists, err := read(context.Background(), path, func(table *Table, row Row) {
		if table.Name == "a" {
			if _, err := db.Exec(`UPDATE z SET value='after'; CREATE TABLE later (value)`); err != nil {
				t.Fatal(err)
			}
		}
	})
	if err != nil || !exists || snapshot.Fingerprint != before.Fingerprint {
		t.Fatalf("mixed source versions: got %s, want %s, exists=%v, err=%v", snapshot.Fingerprint, before.Fingerprint, exists, err)
	}
	after, _, err := Fingerprint(context.Background(), path)
	if err != nil || after == before.Fingerprint {
		t.Fatalf("later read missed committed source change: %s, %v", after, err)
	}
}

func TestReadFingerprintCancellation(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (value); INSERT INTO records VALUES ('one'), ('two'), ('three')`); err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	visited := 0
	snapshot, exists, err := read(ctx, path, func(_ *Table, _ Row) {
		visited++
		cancel()
	})
	if !errors.Is(err, context.Canceled) || exists || snapshot.Fingerprint != "" || visited != 1 {
		t.Fatalf("cancellation: visited=%d, exists=%v, fingerprint=%q, err=%v", visited, exists, snapshot.Fingerprint, err)
	}
	fingerprint, exists, err := Fingerprint(ctx, path)
	if !errors.Is(err, context.Canceled) || exists || fingerprint != "" {
		t.Fatalf("canceled fingerprint: exists=%v, fingerprint=%q, err=%v", exists, fingerprint, err)
	}
}

// Use the public reader so allocation evidence includes the same retained
// snapshot and SQLite work on both sides of the fingerprint change.
func BenchmarkReadFingerprint(b *testing.B) {
	path := filepath.Join(b.TempDir(), "sessions.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		b.Fatal(err)
	}
	if _, err := db.Exec(`CREATE TABLE records (value TEXT)`); err != nil {
		b.Fatal(err)
	}
	tx, err := db.Begin()
	if err != nil {
		b.Fatal(err)
	}
	payload := strings.Repeat("<tool-result>&\n", 4096)
	for i := 0; i < 64; i++ {
		if _, err := tx.Exec(`INSERT INTO records VALUES (?)`, payload); err != nil {
			b.Fatal(err)
		}
	}
	if err := tx.Commit(); err != nil {
		b.Fatal(err)
	}
	if err := db.Close(); err != nil {
		b.Fatal(err)
	}
	b.ReportAllocs()
	b.SetBytes(int64(64 * len(payload)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, _, err := Read(context.Background(), path); err != nil {
			b.Fatal(err)
		}
	}
}
