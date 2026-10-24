package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Physical compaction may replace journal inodes but must not change the
// selected authority, logical revision, receipts, or another handle's writes.
func TestProviderCompactionPreservesActiveHandlesAndReceipts(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			provider := NewService("test.store", dir, kind)
			if err := provider.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer provider.Close(ctx)
			client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: raw})
			})
			peer, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer peer.Close()
			var last Transaction
			for i := range 12 {
				value := strings.Repeat("superseded payload", 4096)
				if i == 11 {
					value = "current"
				}
				raw, _ := json.Marshal(value)
				last = Transaction{ID: fmt.Sprintf("write-%02d", i), ExpectedRevision: Revision(i), Writes: []Write{{Namespace: "n", Key: "key", Value: raw}}}
				if _, err := client.Commit(ctx, last); err != nil {
					t.Fatal(err)
				}
			}
			if _, err := peer.Read(ctx, Query{Namespace: "n"}); err != nil {
				t.Fatal(err)
			}
			before, err := client.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			bindingBefore, err := readBinding(dir)
			if err != nil {
				t.Fatal(err)
			}
			result, err := client.Compact(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if result.Revision != before.Revision || result.BytesBefore <= 0 || result.BytesAfter <= 0 {
				t.Fatalf("compaction result: %+v", result)
			}
			if kind == "jsonl" && result.BytesAfter >= result.BytesBefore/2 {
				t.Fatalf("journal did not reclaim overwritten values: %+v", result)
			}
			bindingAfter, err := readBinding(dir)
			if err != nil || bindingBefore != bindingAfter {
				t.Fatalf("compaction replaced provider binding: %v", err)
			}
			after, err := peer.Export(ctx)
			if err != nil || !reflect.DeepEqual(before, after) {
				t.Fatalf("active peer changed snapshot after compaction: %v", err)
			}
			if receipt, err := peer.Commit(ctx, last); err != nil || receipt.Revision != before.Revision {
				t.Fatalf("receipt replay after compaction: %+v, %v", receipt, err)
			}
			if _, err := peer.Commit(ctx, Transaction{ID: "peer-after", ExpectedRevision: before.Revision, Writes: []Write{{Namespace: "other", Key: "tail", Value: json.RawMessage(`true`)}}}); err != nil {
				t.Fatal(err)
			}
			view, err := client.Read(ctx, Query{Namespace: "other", Limit: 1})
			if err != nil || len(view.Documents) != 1 {
				t.Fatalf("writer appended to retired inode: %+v, %v", view, err)
			}
			reopened, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer reopened.Close()
			view, err = reopened.Read(ctx, Query{Namespace: "other", Limit: 1})
			if err != nil || view.Revision != before.Revision+1 || len(view.Documents) != 1 {
				t.Fatalf("reopen: %+v, %v", view, err)
			}
			canceled, cancel := context.WithCancel(ctx)
			cancel()
			if _, err := client.Compact(canceled); !errors.Is(err, context.Canceled) {
				t.Fatalf("canceled compaction: %v", err)
			}
		})
	}
}

// Disk-indexed values must reject checksum damage even when the corruption is
// still valid JSON and the handle indexed the journal before it was damaged.
func TestJSONLIndexedReadRejectsValueCorruption(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	if _, err := store.Commit(ctx, Transaction{ID: "one", Writes: []Write{{Namespace: "n", Key: "key", Value: json.RawMessage(`"original-value"`)}}}); err != nil {
		t.Fatal(err)
	}
	binding, err := readBinding(dir)
	if err != nil {
		t.Fatal(err)
	}
	path := filepath.Join(dir, binding.Generation, "state.jsonl")
	raw, err := os.ReadFile(path)
	if err != nil {
		t.Fatal(err)
	}
	offset := bytes.Index(raw, []byte(`"original-value"`))
	if offset < 0 {
		t.Fatal("fixture value missing")
	}
	file, err := os.OpenFile(path, os.O_WRONLY, 0)
	if err != nil {
		t.Fatal(err)
	}
	_, writeErr := file.WriteAt([]byte(`"modified-value"`), int64(offset))
	closeErr := file.Close()
	if err := errors.Join(writeErr, closeErr); err != nil {
		t.Fatal(err)
	}
	if _, err := store.Read(ctx, Query{Namespace: "n", Limit: 1}); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("damaged indexed value accepted: %v", err)
	}
}
