package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Prepared reads have the same selectors and revision contract as active reads,
// including pagination; admission separately verifies the complete checkpoint.
func TestPreparedQueryMatchesActiveProvider(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			writer, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer writer.Close()
			writes := []Write{}
			for _, key := range []string{"a", "b", "ba", "bb", "c"} {
				writes = append(writes, Write{Namespace: "n", Key: key, Value: json.RawMessage(`{"saved":true}`)})
			}
			writes = append(writes, Write{Namespace: "other", Key: "a", Value: json.RawMessage(`null`)})
			receipt, err := writer.Commit(ctx, Transaction{ID: "seed", Writes: writes})
			if err != nil {
				t.Fatal(err)
			}
			provider := NewService("candidate", dir, kind)
			defer provider.Close(ctx)
			if err := provider.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: raw})
			})
			revision := receipt.Revision
			stale := revision - 1
			queries := []Query{
				{Namespace: "n", Keys: []string{}},
				{Namespace: "n", Keys: []string{"bb", "a", "bb", "missing"}},
				{Namespace: "n", Limit: 1},
				{Namespace: "n", Limit: 2, Reverse: true},
				{Namespace: "n", Prefix: "b", Start: "ba", End: "c", Limit: 1},
				{Namespace: "n", Cursor: "b", AtRevision: &revision, Limit: 2},
				{Namespace: "n", Cursor: "bb", AtRevision: &revision, Reverse: true, Limit: 2},
				{Namespace: "missing", Limit: 1},
				{Namespace: "n", Keys: []string{}, AtRevision: &stale},
				{Namespace: "n", Cursor: "b", Limit: 1},
			}
			for i, query := range queries {
				want, wantErr := writer.Read(ctx, query)
				got, gotErr := client.Read(ctx, query)
				if wantErr != nil {
					sameClass := errors.Is(wantErr, ErrConflict) && errors.Is(gotErr, ErrConflict) || errors.Is(wantErr, ErrInvalid) && errors.Is(gotErr, ErrInvalid)
					if !sameClass {
						t.Fatalf("query %d error: got %v, want %v", i, gotErr, wantErr)
					}
					continue
				}
				if gotErr != nil || !reflect.DeepEqual(got, want) {
					t.Fatalf("query %d: got %+v (%v), want %+v", i, got, gotErr, want)
				}
			}
		})
	}
}

// A migrated SQLite store can contain gigabytes outside the requested page.
// The generous budget catches whole-store value copies, not allocator details.
func TestPreparedSQLiteSmallReadDoesNotLoadWholeStore(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	writer, err := Open(ctx, dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer writer.Close()
	value, err := json.Marshal(strings.Repeat("x", 256<<10))
	if err != nil {
		t.Fatal(err)
	}
	writes := make([]Write, 64)
	for i := range writes {
		writes[i] = Write{Namespace: "large", Key: fmt.Sprintf("%03d", i), Value: value}
	}
	if _, err := writer.Commit(ctx, Transaction{ID: "large", Writes: writes}); err != nil {
		t.Fatal(err)
	}
	provider := NewService("candidate", dir, "sqlite")
	defer provider.Close(ctx)
	if err := provider.Prepare(ctx); err != nil {
		t.Fatal(err)
	}
	client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: raw})
	})
	for _, query := range []Query{{Namespace: "large", Keys: []string{}}, {Namespace: "large", Limit: 1}} {
		runtime.GC()
		var before, after runtime.MemStats
		runtime.ReadMemStats(&before)
		view, err := client.Read(ctx, query)
		runtime.ReadMemStats(&after)
		if err != nil {
			t.Fatal(err)
		}
		wantCount := 1
		if query.Keys != nil {
			wantCount = 0
		}
		if view.Revision != 1 || len(view.Documents) != wantCount {
			t.Fatalf("unexpected prepared page: revision=%d count=%d", view.Revision, len(view.Documents))
		}
		allocated := after.TotalAlloc - before.TotalAlloc
		t.Logf("keys=%v limit=%d allocated=%d stored_value_bytes=%d", query.Keys, query.Limit, allocated, len(value)*len(writes))
		if allocated > 8<<20 {
			t.Errorf("small prepared query allocated %d bytes for unrelated values; budget %d", allocated, 8<<20)
		}
	}
}
