package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"fmt"
	"runtime"
	"testing"
)

// A large migration request needs one wire buffer. Encoder spare-capacity growth
// must not allocate another whole request after its exact size was reserved.
func TestTransactionWireAllocationBudget(t *testing.T) {
	value := json.RawMessage(`"` + string(bytes.Repeat([]byte("x"), 256<<10)) + `"`)
	tx := Transaction{ID: "large", Writes: make([]Write, 64)}
	for i := range tx.Writes {
		tx.Writes[i] = Write{Namespace: "bulk", Key: fmt.Sprint(i), Value: value}
	}
	var wireBytes int
	client := NewClient(func(_ context.Context, _ string, raw json.RawMessage) (json.RawMessage, error) {
		wireBytes = len(raw)
		decoded, err := DecodeTransaction(raw)
		if err != nil {
			return nil, err
		}
		if len(decoded.Writes) != len(tx.Writes) {
			t.Fatal("writes lost")
		}
		return json.Marshal(Receipt{ID: tx.ID, Revision: 1})
	})
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	_, err := client.Commit(context.Background(), tx)
	runtime.ReadMemStats(&after)
	runtime.KeepAlive(tx)
	if err != nil {
		t.Fatal(err)
	}
	allocated := after.TotalAlloc - before.TotalAlloc
	budget := uint64(wireBytes)*2 + 2<<20
	t.Logf("wire_bytes=%d allocated=%d budget=%d", wireBytes, allocated, budget)
	if allocated > budget {
		t.Fatalf("wire encoding retained full-size growth copies: allocated=%d budget=%d", allocated, budget)
	}
}
