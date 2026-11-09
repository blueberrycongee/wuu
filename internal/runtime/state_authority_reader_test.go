package runtime

import (
	"bytes"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	goruntime "runtime"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func readerFixturePayload(t *testing.T) []byte {
	t.Helper()
	before := persistence.Checkpoint{Version: 1, Revision: 9007199254740993, DocumentsDigest: strings.Repeat("0", 64), ReceiptsDigest: strings.Repeat("1", 64)}
	after := before
	after.Revision++
	raw, err := json.Marshal(authorityRecord{Version: 1, Checkpoint: before, Epoch: "saved", Pending: &authorityPending{Before: before, After: after,
		Transaction: persistence.Transaction{ID: "exact", ExpectedRevision: before.Revision, Writes: []persistence.Write{
			{Namespace: "n", Key: "a", Value: json.RawMessage(`{"large":9007199254740993,"escape":"<>&\u2028","nested":[1e9,-0,null]}`)},
			{Namespace: "n", Key: "b", Value: json.RawMessage(`null`)},
			{Namespace: "n", Key: "c", Delete: true},
		}},
	}})
	if err != nil {
		t.Fatal(err)
	}
	return raw
}

// Construct the envelope without compacting the payload: the checksum commits
// to its exact bytes, including whitespace and escaped strings.
func readerFixtureEnvelope(payload []byte) []byte {
	digest := sha256.Sum256(payload)
	return []byte(fmt.Sprintf("{\"payload\":%s,\"digest\":\"%s\"}", payload, hex.EncodeToString(digest[:])))
}

func readerFixtureAuthority(t *testing.T, raw []byte) *StateAuthority {
	t.Helper()
	a, err := NewStateAuthority(t.TempDir())
	if err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(a.dir, 0700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(a.dir, "head.json"), raw, 0600); err != nil {
		t.Fatal(err)
	}
	return a
}

// These valid historical heads deliberately use v1's permissive member rules.
// The previous two-Unmarshal boundary is the oracle for exact pending values.
func TestStateAuthorityReaderPreservesLegacyHeads(t *testing.T) {
	payload := readerFixturePayload(t)
	cases := map[string][]byte{
		"normal":           payload,
		"whitespace":       bytes.ReplaceAll(payload, []byte(`,"`), []byte(", \n  \"")),
		"unknown-members":  bytes.Replace(payload, []byte(`"transaction":{`), []byte(`"future":{"nested":true},"transaction":{"future":42,`), 1),
		"case-insensitive": bytes.ReplaceAll(bytes.ReplaceAll(payload, []byte(`"version"`), []byte(`"VERSION"`)), []byte(`"value"`), []byte(`"VALUE"`)),
		"duplicate-scalar": bytes.Replace(payload, []byte(`"epoch":"saved"`), []byte(`"epoch":"old","epoch":"saved"`), 1),
		"duplicate-object": bytes.Replace(payload, []byte(`"checkpoint":{`), []byte(`"checkpoint":{"version":0},"checkpoint":{`), 1),
		"duplicate-raw":    bytes.Replace(payload, []byte(`"value":null`), []byte(`"value":{"old":1},"value":null`), 1),
		"invalid-utf8-raw": bytes.Replace(payload, []byte(`"value":null`), []byte{'"', 'v', 'a', 'l', 'u', 'e', '"', ':', '"', 0xff, '"'}, 1),
	}
	for name, body := range cases {
		t.Run(name, func(t *testing.T) {
			raw := readerFixtureEnvelope(body)
			raw = bytes.Replace(raw, []byte(`{"payload":`), []byte(`{"future":{"x":true},"PAYLOAD":`), 1)
			var envelope authorityEnvelope
			if err := json.Unmarshal(raw, &envelope); err != nil {
				t.Fatal(err)
			}
			var want authorityRecord
			if err := json.Unmarshal(envelope.Payload, &want); err != nil {
				t.Fatal(err)
			}
			a := readerFixtureAuthority(t, raw)
			got, err := a.readRecord()
			if err != nil || !reflect.DeepEqual(got, &want) {
				t.Fatalf("legacy head changed: err=%v got=%+v want=%+v", err, got, want)
			}
			// A later read and replacement must not invalidate the returned record.
			if err := os.WriteFile(filepath.Join(a.dir, "head.json"), readerFixtureEnvelope(payload), 0600); err != nil {
				t.Fatal(err)
			}
			if _, err := a.readRecord(); err != nil {
				t.Fatal(err)
			}
			goruntime.GC()
			if !reflect.DeepEqual(got, &want) {
				t.Fatal("returned pending intent changed after another read")
			}
			// No borrowed value may append into the bytes of the next field/value.
			value := got.Pending.Transaction.Writes[0].Value
			_ = append(value, bytes.Repeat([]byte("x"), 128)...)
			if !reflect.DeepEqual(got, &want) {
				t.Fatal("appending a value overwrote another pending field")
			}
		})
	}
}

func TestStateAuthorityReaderRejectsCorruptHeads(t *testing.T) {
	payload := readerFixturePayload(t)
	valid := readerFixtureEnvelope(payload)
	cases := map[string][]byte{
		"checksum":           bytes.Replace(valid, []byte(`"epoch":"saved"`), []byte(`"epoch":"other"`), 1),
		"trailing-json":      append(bytes.Clone(valid), []byte(` {}`)...),
		"truncated":          valid[:len(valid)-1],
		"record-version":     readerFixtureEnvelope(bytes.Replace(payload, []byte(`"version":1`), []byte(`"version":2`), 1)),
		"checkpoint-version": readerFixtureEnvelope(bytes.Replace(payload, []byte(`"checkpoint":{"version":1`), []byte(`"checkpoint":{"version":2`), 1)),
		"checkpoint-digest":  readerFixtureEnvelope(bytes.Replace(payload, []byte(strings.Repeat("0", 64)), []byte("bad"), 1)),
		"pending-before":     readerFixtureEnvelope(bytes.Replace(payload, []byte(`"before":{"version":1`), []byte(`"before":{"version":2`), 1)),
		"pending-after":      readerFixtureEnvelope(bytes.Replace(payload, []byte(`"revision":"9007199254740994"`), []byte(`"revision":"9007199254740995"`), 1)),
		"pending-revision":   readerFixtureEnvelope(bytes.Replace(payload, []byte(`"expected_revision":"9007199254740993"`), []byte(`"expected_revision":"1"`), 1)),
	}
	for name, raw := range cases {
		t.Run(name, func(t *testing.T) {
			got, err := readerFixtureAuthority(t, raw).readRecord()
			if got != nil || !errors.Is(err, persistence.ErrCorrupt) {
				t.Fatalf("corrupt head accepted: got=%v err=%v", got, err)
			}
		})
	}
}

// Large pending intents survive a restart. Only the file buffer and small
// metadata are needed; copying both payload and every value exceeds this budget.
func TestStateAuthorityReaderDoesNotCopyPendingValues(t *testing.T) {
	payload := readerFixturePayload(t)
	large := []byte(`"` + strings.Repeat("x", 8<<20) + `"`)
	payload = bytes.Replace(payload, []byte(`"value":null`), append([]byte(`"value":`), large...), 1)
	raw := readerFixtureEnvelope(payload)
	a := readerFixtureAuthority(t, raw)
	goruntime.GC()
	var before, after goruntime.MemStats
	goruntime.ReadMemStats(&before)
	record, err := a.readRecord()
	goruntime.ReadMemStats(&after)
	if err != nil {
		t.Fatal(err)
	}
	if !bytes.Equal(record.Pending.Transaction.Writes[1].Value, large) {
		t.Fatal("pending value changed")
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	budget := uint64(2*len(raw) + (1 << 20))
	t.Logf("head_bytes=%d allocated=%d budget=%d", len(raw), allocated, budget)
	if allocated > budget {
		t.Fatalf("pending reader copied full values: allocated=%d budget=%d", allocated, budget)
	}
}
