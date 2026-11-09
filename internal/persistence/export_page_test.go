package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

type testExportCursor struct {
	Namespace string    `json:"namespace,omitempty"`
	Key       string    `json:"key,omitempty"`
	Revision  *Revision `json:"revision,string,omitempty"`
}
type testExportQuery struct {
	Section     string            `json:"section"`
	AtRevision  *Revision         `json:"at_revision,string,omitempty"`
	Generation  string            `json:"generation,omitempty"`
	After       *testExportCursor `json:"after,omitempty"`
	TargetBytes int               `json:"target_bytes"`
	MaxItems    int               `json:"max_items"`
}
type testExportPage struct {
	Format     string         `json:"format"`
	Revision   Revision       `json:"revision,string"`
	Generation string         `json:"generation"`
	Section    string         `json:"section"`
	Documents  []Document     `json:"documents"`
	Receipts   []CommitRecord `json:"receipts"`
	Done       bool           `json:"done"`
}

func testPageCall(t *testing.T, provider *Service, q testExportQuery) (testExportPage, []byte, error) {
	t.Helper()
	raw, err := json.Marshal(struct {
		Page testExportQuery `json:"page"`
	}{q})
	if err != nil {
		t.Fatal(err)
	}
	raw, err = provider.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Method: "export", Params: raw})
	if err != nil {
		return testExportPage{}, nil, err
	}
	var page testExportPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if page.Format != "page.v1" {
		t.Fatalf("wrong page format: %s", raw)
	}
	return page, raw, nil
}
func testNextPage(q *testExportQuery, page testExportPage) {
	q.AtRevision = &page.Revision
	q.Generation = page.Generation
	if q.Section == "documents" && len(page.Documents) > 0 {
		last := page.Documents[len(page.Documents)-1]
		q.After = &testExportCursor{Namespace: last.Namespace, Key: last.Key}
	} else if q.Section == "receipts" && len(page.Receipts) > 0 {
		last := page.Receipts[len(page.Receipts)-1]
		q.After = &testExportCursor{Revision: &last.Revision}
	}
}

// The wire contract must work before activation, retain every record and never
// build a whole export merely to satisfy the requested byte/item page budget.
func TestExportPagesMatchFullSnapshot(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := t.TempDir()
			source, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			writes := []Write{}
			for i, pair := range [][2]string{{"z", "a"}, {"n", "𐀀"}, {"n", "\ue000"}, {"n", "a"}, {"n|a", "b"}, {"n", "a|b"}} {
				value, _ := json.Marshal(strings.Repeat("<>&\\\"中", 180+i))
				writes = append(writes, Write{Namespace: pair[0], Key: pair[1], Value: value})
			}
			if _, err = source.Commit(ctx, Transaction{ID: "seed", Writes: writes}); err != nil {
				t.Fatal(err)
			}
			if _, err = source.Commit(ctx, Transaction{ID: "second", ExpectedRevision: 1, Writes: []Write{{Namespace: "n", Key: "gone", Delete: true}}}); err != nil {
				t.Fatal(err)
			}
			want, err := source.Export(ctx)
			if err != nil {
				t.Fatal(err)
			}
			provider := NewService("prepared", dir, kind)
			defer provider.Close(ctx)
			if err = provider.Prepare(ctx); err != nil {
				t.Fatal(err)
			}
			for _, budget := range []int{512, 8192} {
				q := testExportQuery{Section: "documents", TargetBytes: budget, MaxItems: 2}
				got := Snapshot{Version: 1, Documents: []Document{}, Receipts: []CommitRecord{}}
				for _, section := range []string{"documents", "receipts"} {
					q.Section = section
					q.After = nil
					for count := 0; ; count++ {
						if count > 20 {
							t.Fatal("page traversal made no progress")
						}
						page, raw, err := testPageCall(t, provider, q)
						if err != nil {
							t.Fatal(err)
						}
						total := len(page.Documents) + len(page.Receipts)
						if total > q.MaxItems || (len(raw) > budget && total != 1) {
							t.Fatalf("page exceeded bounds: bytes=%d count=%d", len(raw), total)
						}
						if page.Generation == "" || page.Revision != want.Revision || page.Section != section {
							t.Fatalf("invalid page metadata: %+v", page)
						}
						got.Revision = page.Revision
						got.Documents = append(got.Documents, page.Documents...)
						got.Receipts = append(got.Receipts, page.Receipts...)
						testNextPage(&q, page)
						if page.Done {
							break
						}
						if total == 0 {
							t.Fatal("empty nonterminal page")
						}
					}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("pages differ from complete snapshot: got %d docs/%d receipts, want %d/%d", len(got.Documents), len(got.Receipts), len(want.Documents), len(want.Receipts))
				}
			}
			raw, err := provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: "export", Params: json.RawMessage(`{}`)})
			if err != nil {
				t.Fatal(err)
			}
			var full Snapshot
			if err = json.Unmarshal(raw, &full); err != nil || !reflect.DeepEqual(full, want) {
				t.Fatalf("legacy export changed: %v", err)
			}
		})
	}
}

func TestExportPagePinsAndPreparation(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			dir := filepath.Join(t.TempDir(), "store")
			empty := NewService("empty", dir, kind)
			defer empty.Close(ctx)
			q := testExportQuery{Section: "documents", TargetBytes: 512, MaxItems: 1}
			page, _, err := testPageCall(t, empty, q)
			if err != nil || !page.Done || page.Revision != 0 {
				t.Fatalf("empty page: %+v %v", page, err)
			}
			if _, err = os.Stat(dir); !errors.Is(err, os.ErrNotExist) {
				t.Fatalf("prepared page created storage: %v", err)
			}
			source, err := Open(ctx, dir, kind)
			if err != nil {
				t.Fatal(err)
			}
			defer source.Close()
			testNextPage(&q, page)
			if _, _, err = testPageCall(t, empty, q); err == nil {
				t.Fatal("missing-to-created generation accepted")
			}
			provider := NewService("candidate", dir, kind)
			defer provider.Close(ctx)
			q = testExportQuery{Section: "documents", TargetBytes: 512, MaxItems: 1}
			first, _, err := testPageCall(t, provider, q)
			if err != nil {
				t.Fatal(err)
			}
			if _, err = source.Commit(ctx, Transaction{ID: "new", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`true`)}}}); err != nil {
				t.Fatal(err)
			}
			testNextPage(&q, first)
			if _, _, err = testPageCall(t, provider, q); err == nil {
				t.Fatal("stale revision accepted")
			}
			q = testExportQuery{Section: "documents", TargetBytes: 512, MaxItems: 1}
			current, _, err := testPageCall(t, provider, q)
			if err != nil || current.Revision != 1 {
				t.Fatalf("prepared reader did not follow same generation: %v", err)
			}
			testNextPage(&q, current)
			other := "jsonl"
			if kind == other {
				other = "sqlite"
			}
			if err = Migrate(ctx, dir, kind, other); err != nil {
				t.Fatal(err)
			}
			if _, _, err = testPageCall(t, provider, q); err == nil {
				t.Fatal("replacement generation accepted")
			}
		})
	}
}

func TestExportPageRejectsInvalidRequests(t *testing.T) {
	provider := NewService("candidate", filepath.Join(t.TempDir(), "missing"), "sqlite")
	defer provider.Close(context.Background())
	for _, raw := range []string{
		`{"page":{}}`,
		`{"page":{"section":"other","target_bytes":512,"max_items":1}}`,
		`{"page":{"section":"documents","target_bytes":0,"max_items":1}}`,
		`{"page":{"section":"documents","target_bytes":512,"max_items":0}}`,
		`{"page":{"section":"documents","target_bytes":512,"max_items":1,"after":{"namespace":"n","key":"a"}}}`,
		`{"page":{"section":"documents","target_bytes":512,"max_items":1,"at_revision":"0"}}`,
		`{"page":{"section":"receipts","target_bytes":512,"max_items":1}}`,
		`{"page":{"section":"documents","target_bytes":512,"max_items":1,"generation":"empty","at_revision":"0","after":{"revision":"0"}}}`,
		`{"page":{"section":"receipts","target_bytes":512,"max_items":1,"generation":"empty","at_revision":"0","after":{"namespace":"n","key":"a"}}}`,
	} {
		if _, err := provider.InvokeService(context.Background(), pluginhost.ServiceInvokeParams{Method: "export", Params: json.RawMessage(raw)}); err == nil {
			t.Fatalf("accepted invalid request: %s", raw)
		}
	}
}

// A 17 MiB record must remain valid, and an intentionally smaller target must
// return it alone instead of truncating it or introducing a document-size cap.
func TestExportPageRetainsLargeSingleRecord(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value := json.RawMessage(`"` + strings.Repeat("x", 17<<20) + `"`)
	if _, err = store.Commit(ctx, Transaction{ID: "large", Writes: []Write{{Namespace: "n", Key: "a", Value: value}, {Namespace: "n", Key: "b", Value: json.RawMessage(`true`)}}}); err != nil {
		t.Fatal(err)
	}
	provider := NewService("candidate", dir, "sqlite")
	defer provider.Close(ctx)
	for _, target := range []int{1 << 20, 32 << 20} {
		page, raw, err := testPageCall(t, provider, testExportQuery{Section: "documents", TargetBytes: target, MaxItems: 1})
		if err != nil {
			t.Fatal(err)
		}
		if len(page.Documents) != 1 || string(page.Documents[0].Value) != string(value) || len(raw) > 64<<20 {
			t.Fatal("large record lost or framing exceeded")
		}
	}
}

func TestExportSQLitePageDoesNotLoadUnrelatedValues(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir, "sqlite")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value := json.RawMessage(`"` + strings.Repeat("x", 256<<10) + `"`)
	writes := make([]Write, 128)
	for i := range writes {
		writes[i] = Write{Namespace: "n", Key: fmt.Sprintf("%04d", i), Value: value}
	}
	if _, err = store.Commit(ctx, Transaction{ID: "seed", Writes: writes}); err != nil {
		t.Fatal(err)
	}
	provider := NewService("candidate", dir, "sqlite")
	defer provider.Close(ctx)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	page, _, err := testPageCall(t, provider, testExportQuery{Section: "documents", TargetBytes: 1 << 20, MaxItems: 1})
	runtime.ReadMemStats(&after)
	if err != nil || len(page.Documents) != 1 {
		t.Fatalf("page: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 8<<20 {
		t.Fatalf("single page allocated unrelated payload: %d bytes", allocated)
	}
}

// A prepared JSONL traversal reuses its read-only index; repeating a small page
// must not allocate another copy of the large historical transaction frame.
func TestExportPreparedJSONLDoesNotReplayEachPage(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	store, err := Open(ctx, dir, "jsonl")
	if err != nil {
		t.Fatal(err)
	}
	defer store.Close()
	value := json.RawMessage(`"` + strings.Repeat("x", 64<<10) + `"`)
	writes := make([]Write, 64)
	for i := range writes {
		writes[i] = Write{Namespace: "n", Key: fmt.Sprintf("%04d", i), Value: value}
	}
	if _, err = store.Commit(ctx, Transaction{ID: "seed", Writes: writes}); err != nil {
		t.Fatal(err)
	}
	provider := NewService("candidate", dir, "jsonl")
	defer provider.Close(ctx)
	q := testExportQuery{Section: "documents", TargetBytes: 1 << 20, MaxItems: 1}
	first, _, err := testPageCall(t, provider, q)
	if err != nil {
		t.Fatal(err)
	}
	testNextPage(&q, first)
	runtime.GC()
	var before, after runtime.MemStats
	runtime.ReadMemStats(&before)
	second, _, err := testPageCall(t, provider, q)
	runtime.ReadMemStats(&after)
	if err != nil || len(second.Documents) != 1 || second.Documents[0].Key != "0001" {
		t.Fatalf("second page: %v", err)
	}
	if allocated := after.TotalAlloc - before.TotalAlloc; allocated > 2<<20 {
		t.Fatalf("prepared traversal replayed unrelated journal payload: %d bytes", allocated)
	}
}
