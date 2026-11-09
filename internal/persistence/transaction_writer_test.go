package persistence

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"io"
	"math"
	"strconv"
	"strings"
	"testing"
)

// Persisted receipts use the old full-value Marshal digest. Streaming must keep
// its exact bytes, including RawMessage spelling, ordering and legacy escaping.
func TestTransactionWriterMatchesLegacyJSON(t *testing.T) {
	for name, tx := range map[string]Transaction{
		"nil_writes":   {},
		"empty_writes": {Writes: []Write{}, ExpectedRevision: math.MaxUint64},
		"mixed": {
			ID:               "quote\" slash\\ <>& \u2028\u2029 中文 " + string([]byte{0xff}),
			ExpectedRevision: 9007199254740993,
			Writes: []Write{
				{Namespace: "z<&", Key: "quoted\"\n", Value: json.RawMessage(` { "z": [9007199254740993, 1e+09, -0, true, null], "a":"<>&\u2028", "a":"second" } `)},
				{Namespace: "a", Key: "null", Value: json.RawMessage(`null`)},
				{Namespace: "a", Key: "deleted", Delete: true},
			},
		},
	} {
		t.Run(name, func(t *testing.T) {
			want, err := json.Marshal(tx)
			if err != nil {
				t.Fatal(err)
			}
			var got bytes.Buffer
			if err := WriteTransactionJSON(&got, tx); err != nil {
				t.Fatal(err)
			}
			if !bytes.Equal(got.Bytes(), want) {
				t.Fatalf("transaction bytes changed:\ngot  %s\nwant %s", got.Bytes(), want)
			}
			if len(tx.Writes) > 0 {
				digest := sha256.Sum256(want)
				got, err := transactionDigest(tx)
				if err != nil || got != hex.EncodeToString(digest[:]) {
					t.Fatalf("persisted digest changed: got %q, err %v", got, err)
				}
			}
		})
	}
}

func TestTransactionDigestRejectsInvalidIntent(t *testing.T) {
	valid := Write{Namespace: "n", Key: "k", Value: json.RawMessage(`true`)}
	for name, tx := range map[string]Transaction{
		"missing_id":   {Writes: []Write{valid}},
		"empty":        {ID: "id"},
		"revision":     {ID: "id", ExpectedRevision: math.MaxInt64, Writes: []Write{valid}},
		"duplicate":    {ID: "id", Writes: []Write{valid, valid}},
		"namespace":    {ID: "id", Writes: []Write{{Namespace: " ", Key: "k", Value: json.RawMessage(`true`)}}},
		"missing_key":  {ID: "id", Writes: []Write{{Namespace: "n", Value: json.RawMessage(`true`)}}},
		"invalid_json": {ID: "id", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`{"unfinished":`)}}},
		"delete_value": {ID: "id", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`null`), Delete: true}}},
	} {
		t.Run(name, func(t *testing.T) {
			if _, err := transactionDigest(tx); !errors.Is(err, ErrInvalid) {
				t.Fatalf("invalid transaction accepted: %v", err)
			}
		})
	}
}

func TestTransactionWriterPreservesLegacyReceipt(t *testing.T) {
	ctx := context.Background()
	tx := Transaction{ID: "legacy<&", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(` { "large":9007199254740993, "text":"<>&" } `)}}}
	raw, err := json.Marshal(tx)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(raw)
	receipt := Receipt{ID: tx.ID, Revision: 1}
	snapshot := Snapshot{Version: 1, Revision: 1,
		Documents: []Document{{Namespace: "n", Key: "k", Value: tx.Writes[0].Value}},
		Receipts:  []CommitRecord{{Receipt: receipt, Digest: hex.EncodeToString(digest[:])}},
	}
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			dir := t.TempDir()
			store, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer store.Close()
			if _, err := store.Import(ctx, snapshot); err != nil {
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
			if got, err := store.Commit(ctx, tx); err != nil || got != receipt {
				t.Fatalf("legacy receipt retry: %+v, %v", got, err)
			}
			changed := tx
			changed.Writes = []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`false`)}}
			if _, err := store.Commit(ctx, changed); !errors.Is(err, ErrOperationConflict) {
				t.Fatalf("changed intent accepted: %v", err)
			}
		})
	}
}

type transactionFailWriter struct{ err error }

func (w transactionFailWriter) Write(p []byte) (int, error) { return len(p) / 2, w.err }

func TestTransactionWriterReportsErrors(t *testing.T) {
	tx := Transaction{ID: "id", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`true`)}}}
	failure := errors.New("write failed")
	if err := WriteTransactionJSON(transactionFailWriter{failure}, tx); !errors.Is(err, failure) {
		t.Fatalf("writer error lost: %v", err)
	}
	tx.Writes[0].Value = json.RawMessage(`{"unfinished":`)
	if err := WriteTransactionJSON(io.Discard, tx); err == nil {
		t.Fatal("invalid RawMessage accepted")
	}
}

func BenchmarkTransactionDigest(b *testing.B) {
	value, _ := json.Marshal(strings.Repeat("<result>&\n", 1024))
	tx := Transaction{ID: "bulk", Writes: make([]Write, 1024)}
	for i := range tx.Writes {
		tx.Writes[i] = Write{Namespace: "n", Key: strconv.Itoa(i), Value: value}
	}
	b.ReportAllocs()
	b.SetBytes(int64(len(value) * len(tx.Writes)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if _, err := transactionDigest(tx); err != nil {
			b.Fatal(err)
		}
	}
}
