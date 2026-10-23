package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Explicit selected-provider import must keep original receipts, fence handles
// of the empty target, reject overwrites, and survive a lost success response.
func TestSelectedProviderImportPreservesReceiptsAndFencesOldHandles(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			sourceKind := "sqlite"
			if kind == sourceKind {
				sourceKind = "jsonl"
			}
			source, err := Open(ctx, t.TempDir(), sourceKind)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			original := Transaction{ID: "original", Writes: []Write{{Namespace: "history", Key: "record", Value: json.RawMessage(`{"content":"keep raw data"}`)}, {Namespace: "ownership", Key: "turn", Value: json.RawMessage(`"run"`)}}}
			receipt, err := source.Commit(ctx, original)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := source.Commit(ctx, Transaction{ID: "update", ExpectedRevision: 1, Writes: []Write{{Namespace: "history", Key: "record", Value: json.RawMessage(`{"content":"current data"}`)}}}); err != nil {
				t.Fatal(err)
			}
			snapshot, err := source.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			expected, err := SnapshotCheckpoint(snapshot)
			if err != nil {
				t.Fatal(err)
			}
			dir := t.TempDir()
			provider := NewService("target", dir, kind)
			if err := provider.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer provider.Close(ctx)
			stale, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer stale.Close()
			loseReply := true
			lost := errors.New("import reply was lost")
			client := NewClient(func(ctx context.Context, method string, params json.RawMessage) (json.RawMessage, error) {
				result, err := provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: params})
				if method == "import" && err == nil && loseReply {
					loseReply = false
					return nil, lost
				}
				return result, err
			})
			if _, err := client.Import(ctx, snapshot); !errors.Is(err, lost) {
				t.Fatalf("lost import response: %v", err)
			}
			if _, err := stale.Commit(ctx, Transaction{ID: "stale", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`true`)}}}); !errors.Is(err, ErrBindingChanged) {
				t.Fatalf("old empty handle remained writable: %v", err)
			}
			if err := provider.Close(ctx); err != nil {
				t.Fatal(err)
			}
			provider = NewService("target", dir, kind)
			if err := provider.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer provider.Close(ctx)
			checkpoint, err := client.Import(ctx, snapshot)
			if err != nil || checkpoint.Revision != expected.Revision {
				t.Fatalf("idempotent import after restart: %+v %v", checkpoint, err)
			}
			actual, err := client.Export(ctx)
			if err != nil || !reflect.DeepEqual(snapshot, actual) {
				t.Fatalf("imported snapshot mismatch: %v", err)
			}
			if got, err := client.Commit(ctx, original); err != nil || got != receipt {
				t.Fatalf("original receipt was not retained: %+v %v", got, err)
			}
			changed := snapshot
			changed.Documents = append([]Document{}, snapshot.Documents...)
			changed.Documents[0].Value = json.RawMessage(`"different"`)
			if _, err := client.Import(ctx, changed); !errors.Is(err, ErrConflict) {
				t.Fatalf("nonempty target overwritten: %v", err)
			}
			corrupt := snapshot
			corrupt.Receipts = nil
			if _, err := client.Import(ctx, corrupt); !errors.Is(err, ErrCorrupt) {
				t.Fatalf("missing receipts imported: %v", err)
			}
			if err := VerifyCheckpoint(ctx, client, expected); err != nil {
				t.Fatal(err)
			}
			if err := VerifyCheckpoint(ctx, source, expected); err != nil {
				t.Fatalf("source changed during import: %v", err)
			}
		})
	}
}
