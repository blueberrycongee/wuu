package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"os"
	"path/filepath"
	"strconv"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

// Existing heads commit to exactly the payload bytes produced by json.Marshal.
// The second Marshal is the on-disk envelope oracle, including its escaping.
func TestStateAuthorityWriterMatchesLegacyEnvelope(t *testing.T) {
	checkpoint := persistence.Checkpoint{Version: 1, Revision: 9007199254740993, DocumentsDigest: strings.Repeat("0", 64), ReceiptsDigest: strings.Repeat("1", 64)}
	after := checkpoint
	after.Revision++
	for _, pending := range []bool{false, true} {
		t.Run(strconv.FormatBool(pending), func(t *testing.T) {
			record := &authorityRecord{Version: 1, Checkpoint: checkpoint, Epoch: "epoch\"<>&\n\u2028\u2029 中文"}
			if pending {
				record.Pending = &authorityPending{Before: checkpoint, After: after,
					Transaction: persistence.Transaction{ID: "pending<&", ExpectedRevision: checkpoint.Revision, Writes: []persistence.Write{
						{Namespace: "n<&", Key: "k\"", Value: json.RawMessage(` { "z":9007199254740993, "a":"<>&\u2028", "a":[1e+09,-0,null] } `)},
						{Namespace: "n", Key: "deleted", Delete: true},
					}},
				}
			}
			payload, err := json.Marshal(record)
			if err != nil {
				t.Fatal(err)
			}
			digest := sha256.Sum256(payload)
			want, err := json.Marshal(authorityEnvelope{Payload: payload, Digest: hex.EncodeToString(digest[:])})
			if err != nil {
				t.Fatal(err)
			}
			a, err := NewStateAuthority(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			if err := a.writeRecord(record); err != nil {
				t.Fatal(err)
			}
			got, err := os.ReadFile(filepath.Join(a.dir, "head.json"))
			if err != nil || !bytes.Equal(got, want) {
				t.Fatalf("head bytes changed: err %v\ngot  %s\nwant %s", err, got, want)
			}
			decoded, err := a.readRecord()
			if err != nil {
				t.Fatal(err)
			}
			decodedPayload, err := json.Marshal(decoded)
			if err != nil || !bytes.Equal(decodedPayload, payload) {
				t.Fatalf("head did not retain its exact pending intent: %v", err)
			}
		})
	}
}

func TestStateAuthorityWriterDoesNotPublishInvalidPending(t *testing.T) {
	for _, existing := range []bool{false, true} {
		t.Run(strconv.FormatBool(existing), func(t *testing.T) {
			a, err := NewStateAuthority(t.TempDir())
			if err != nil {
				t.Fatal(err)
			}
			record := &authorityRecord{Version: 1, Checkpoint: persistence.Checkpoint{Version: 1, DocumentsDigest: strings.Repeat("0", 64), ReceiptsDigest: strings.Repeat("1", 64)}}
			var previous []byte
			if existing {
				if err := a.writeRecord(record); err != nil {
					t.Fatal(err)
				}
				previous, err = os.ReadFile(filepath.Join(a.dir, "head.json"))
				if err != nil {
					t.Fatal(err)
				}
			}
			record.Pending = &authorityPending{Transaction: persistence.Transaction{ID: "invalid", Writes: []persistence.Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`{"unfinished":`)}}}}
			if err := a.writeRecord(record); err == nil {
				t.Fatal("invalid pending published")
			}
			current, err := os.ReadFile(filepath.Join(a.dir, "head.json"))
			if existing {
				if err != nil || !bytes.Equal(current, previous) {
					t.Fatalf("prior head replaced on encoding failure: %v", err)
				}
			} else {
				if !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed first write published a head: %v", err)
				}
				if _, err := os.Stat(filepath.Join(a.dir, "initialized")); !errors.Is(err, os.ErrNotExist) {
					t.Fatalf("failed first write published initialization: %v", err)
				}
			}
			leftovers, err := filepath.Glob(filepath.Join(a.dir, ".securefs-*.tmp"))
			if err != nil || len(leftovers) != 0 {
				t.Fatalf("partial head leaked: %v, %v", leftovers, err)
			}
		})
	}
}

func BenchmarkStateAuthorityWritePending(b *testing.B) {
	a, err := NewStateAuthority(b.TempDir())
	if err != nil {
		b.Fatal(err)
	}
	value, _ := json.Marshal(strings.Repeat("<result>&\n", 1024))
	tx := persistence.Transaction{ID: "bulk", Writes: make([]persistence.Write, 1024)}
	for i := range tx.Writes {
		tx.Writes[i] = persistence.Write{Namespace: "n", Key: strconv.Itoa(i), Value: value}
	}
	record := &authorityRecord{Version: 1, Pending: &authorityPending{Transaction: tx}}
	b.ReportAllocs()
	b.SetBytes(int64(len(value) * len(tx.Writes)))
	b.ResetTimer()
	for i := 0; i < b.N; i++ {
		if err := a.writeRecord(record); err != nil {
			b.Fatal(err)
		}
	}
}
