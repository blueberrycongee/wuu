package legacysqlite

import (
	"bytes"
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
					{-2, nil, []byte{}, "valid replacement \ufffd\ufffd", math.MinInt64, math.Copysign(0, -1)},
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
				raw := legacySnapshotJSON(t, snapshot)
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

// Encode the original archive shape independently of later Row metadata.
func legacySnapshotJSON(t *testing.T, snapshot Snapshot) []byte {
	t.Helper()
	type legacyRow struct {
		Values      map[string]any `json:"values"`
		BlobColumns []string       `json:"blob_columns,omitempty"`
	}
	type legacyTable struct {
		Name   string      `json:"name"`
		Schema string      `json:"schema"`
		Rows   []legacyRow `json:"rows"`
	}
	var legacy struct {
		Tables []legacyTable `json:"tables"`
	}
	if snapshot.Tables != nil {
		legacy.Tables = make([]legacyTable, len(snapshot.Tables))
	}
	for i, table := range snapshot.Tables {
		legacy.Tables[i].Name, legacy.Tables[i].Schema = table.Name, table.Schema
		if table.Rows != nil {
			legacy.Tables[i].Rows = make([]legacyRow, len(table.Rows))
		}
		for j, row := range table.Rows {
			legacy.Tables[i].Rows[j] = legacyRow{Values: row.Values, BlobColumns: row.BlobColumns}
		}
	}
	raw, err := json.Marshal(legacy)
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

func TestReadPreservesInvalidTextBytes(t *testing.T) {
	path := filepath.Join(t.TempDir(), "sessions.sqlite3")
	db, err := sql.Open("sqlite", path)
	if err != nil {
		t.Fatal(err)
	}
	defer db.Close()
	if _, err := db.Exec(`CREATE TABLE records (content TEXT, valid TEXT, blob BLOB); INSERT INTO records VALUES ('', 'valid <>& 中文', x'fffe')`); err != nil {
		t.Fatal(err)
	}
	inputs := []string{string([]byte{'a', 0xff, 0xfe, 'z'}), string([]byte{'a', 0x80, 0x81, 'z'}), "a\ufffd\ufffdz"}
	var fingerprints []string
	var oldFingerprints [][sha256.Size]byte
	for i, input := range inputs {
		if _, err := db.Exec(`UPDATE records SET content=CAST(? AS TEXT)`, []byte(input)); err != nil {
			t.Fatal(err)
		}
		snapshot, exists, err := Read(context.Background(), path)
		if err != nil || !exists {
			t.Fatalf("read invalid text: exists=%v, err=%v", exists, err)
		}
		row := snapshot.Tables[0].Rows[0]
		if value, ok := row.Values["content"].(string); !ok || value != input {
			t.Fatalf("typed converter lost original SQLite string: %q", row.Values["content"])
		}
		raw, err := json.Marshal(row)
		if err != nil {
			t.Fatal(err)
		}
		var archived struct {
			Values      map[string]any    `json:"values"`
			BlobColumns []string          `json:"blob_columns"`
			TextBytes   map[string][]byte `json:"text_bytes"`
		}
		if err := json.Unmarshal(raw, &archived); err != nil {
			t.Fatal(err)
		}
		if len(archived.BlobColumns) != 1 || archived.BlobColumns[0] != "blob" {
			t.Errorf("TEXT changed BLOB classification: %+v", archived.BlobColumns)
		}
		if i < 2 {
			if len(archived.TextBytes) != 1 || !bytes.Equal(archived.TextBytes["content"], []byte(input)) {
				t.Errorf("invalid TEXT did not survive JSON round trip: input=%x, archived=%x", input, archived.TextBytes["content"])
			}
		} else if len(archived.TextBytes) != 0 {
			t.Errorf("valid replacement characters gained byte metadata: %+v", archived.TextBytes)
		}
		if archived.Values["content"] != inputs[2] {
			t.Errorf("unexpected JSON string normalization: %q", archived.Values["content"])
		}
		fingerprint, exists, err := Fingerprint(context.Background(), path)
		if err != nil || !exists || fingerprint != snapshot.Fingerprint {
			t.Fatalf("invalid TEXT streaming fingerprint differs: %s, %v", fingerprint, err)
		}
		fingerprints = append(fingerprints, fingerprint)
		oldFingerprints = append(oldFingerprints, sha256.Sum256(legacySnapshotJSON(t, snapshot)))
	}
	if oldFingerprints[0] != oldFingerprints[1] {
		t.Fatal("fixture no longer reproduces the old invalid-byte fingerprint collision")
	}
	t.Logf("old invalid-byte variants collide; collision with valid replacement text: %v", oldFingerprints[0] == oldFingerprints[2])
	if fingerprints[0] == fingerprints[1] || fingerprints[0] == fingerprints[2] || fingerprints[1] == fingerprints[2] {
		t.Fatal("distinct SQLite TEXT bytes share a fingerprint")
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
