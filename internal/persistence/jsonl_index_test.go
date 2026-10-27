package persistence

import (
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"strings"
	"testing"
)

// Reopening the journal must not retain every value in RAM. Values containing
// repeated JSON fragments and escapes must still be read exactly from their
// offsets after an update, deletion, a second writer, and a restart.
func TestJSONLBoundedValueMemoryAndRecovery(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	func() {
		s, err := Open(ctx, dir, "jsonl")
		if err != nil {
			t.Fatal(err)
		}
		defer s.Close()
		writes := make([]Write, 256)
		for i := range writes {
			raw, err := json.Marshal(map[string]string{"payload": strings.Repeat("\"\\\n中", 8192), "identity": fmt.Sprintf("%04d", i)})
			if err != nil {
				t.Fatal(err)
			}
			writes[i] = Write{Namespace: "large", Key: fmt.Sprintf("%04d", i), Value: raw}
		}
		if _, err := s.Commit(ctx, Transaction{ID: "seed", Writes: writes}); err != nil {
			t.Fatal(err)
		}
	}()
	runtime.GC()
	var before runtime.MemStats
	runtime.ReadMemStats(&before)
	s, err := Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	runtime.GC()
	var after runtime.MemStats
	runtime.ReadMemStats(&after)
	if after.HeapAlloc > before.HeapAlloc+8*1024*1024 {
		t.Fatalf("reopen retained document values: heap grew %d bytes", after.HeapAlloc-before.HeapAlloc)
	}
	first, err := s.Read(ctx, Query{Namespace: "large", Start: "0127", Limit: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(first.Documents) != 1 || first.NextCursor != "0127" {
		t.Fatalf("first page: %+v", first)
	}
	var value map[string]string
	if err := json.Unmarshal(first.Documents[0].Value, &value); err != nil {
		t.Fatal(err)
	}
	if value["identity"] != "0127" || value["payload"] != strings.Repeat("\"\\\n中", 8192) {
		t.Fatal("wrong value offset")
	}
	writer, err := Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	if _, err := writer.Commit(ctx, Transaction{ID: "update", ExpectedRevision: 1, Writes: []Write{
		{Namespace: "large", Key: "0127", Delete: true},
		{Namespace: "large", Key: "0128", Value: json.RawMessage(`{"nested":{"value":"identical"},"value":"identical"}`)},
		{Namespace: "large", Key: "0126a", Value: json.RawMessage(`"inserted"`)},
	}}); err != nil {
		t.Fatal(err)
	}
	updated, err := s.Read(ctx, Query{Namespace: "large", Start: "0126a", Limit: 2})
	if err != nil {
		t.Fatal(err)
	}
	if len(updated.Documents) != 2 || updated.Documents[0].Key != "0126a" || updated.Documents[1].Key != "0128" || string(updated.Documents[1].Value) != `{"nested":{"value":"identical"},"value":"identical"}` {
		t.Fatal("tail refresh lost value locations")
	}
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	reopened, err := s.Read(ctx, Query{Namespace: "large", Keys: []string{"0127", "0128"}})
	if err != nil {
		t.Fatal(err)
	}
	if len(reopened.Documents) != 1 || string(reopened.Documents[0].Value) != string(updated.Documents[1].Value) {
		t.Fatal("restart changed selected values")
	}
}
