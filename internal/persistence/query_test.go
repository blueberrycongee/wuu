package persistence

import (
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

// Pages must preserve selectors and ordering across both providers, reject stale
// revisions, and cross the public service boundary without dropping bounds.
func TestBoundedReadProtocol(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			provider := NewService("test.store", t.TempDir(), kind)
			if err := provider.Activate(ctx); err != nil {
				t.Fatal(err)
			}
			defer provider.Close(ctx)
			client := NewClient(func(ctx context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				return provider.InvokeService(ctx, pluginhost.ServiceInvokeParams{Method: method, Params: raw})
			})
			writes := []Write{}
			for _, key := range []string{"0", "h/00", "h/01", "h/02", "h/03", "h/04", "h/05", "h0", "é", "中"} {
				writes = append(writes, Write{Namespace: "history", Key: key, Value: json.RawMessage(`{"saved":true}`)})
			}
			writes = append(writes, Write{Namespace: "other", Key: "h/02", Value: json.RawMessage(`null`)})
			if _, err := client.Commit(ctx, Transaction{ID: "seed", Writes: writes}); err != nil {
				t.Fatal(err)
			}
			for _, reverse := range []bool{false, true} {
				q := Query{Namespace: "history", Prefix: "h/", Start: "h/01", End: "h/05", Limit: 2, Reverse: reverse}
				got := []string{}
				for {
					v, err := client.Read(ctx, q)
					if err != nil {
						t.Fatal(err)
					}
					if len(v.Documents) > q.Limit {
						t.Fatalf("unbounded page: %d", len(v.Documents))
					}
					for _, d := range v.Documents {
						got = append(got, d.Key)
					}
					if v.NextCursor == "" {
						break
					}
					if v.NextCursor != v.Documents[len(v.Documents)-1].Key {
						t.Fatal("cursor is not last returned key")
					}
					q.Cursor = v.NextCursor
					q.AtRevision = &v.Revision
				}
				want := []string{"h/01", "h/02", "h/03", "h/04"}
				if reverse {
					want = []string{"h/04", "h/03", "h/02", "h/01"}
				}
				if !reflect.DeepEqual(got, want) {
					t.Fatalf("keys %v, want %v", got, want)
				}
			}
			for _, tc := range []struct {
				q    Query
				want []string
			}{
				{Query{Namespace: "history", Keys: []string{"h/04", "h/01", "h/01", "missing", "0"}, Prefix: "h/", Limit: 2}, []string{"h/01", "h/04"}},
				{Query{Namespace: "history", Keys: []string{}, Limit: 2}, []string{}},
				{Query{Namespace: "history", Start: "h/02", End: "h/02", Limit: 2}, []string{}},
				{Query{Namespace: "history", Start: "é", Reverse: true, Limit: 2}, []string{"中", "é"}},
			} {
				v, err := client.Read(ctx, tc.q)
				if err != nil {
					t.Fatal(err)
				}
				got := []string{}
				for _, d := range v.Documents {
					got = append(got, d.Key)
				}
				if !reflect.DeepEqual(got, tc.want) || v.NextCursor != "" {
					t.Fatalf("query %+v: %+v", tc.q, v)
				}
			}
			first, err := client.Read(ctx, Query{Namespace: "history", Limit: 2})
			if err != nil {
				t.Fatal(err)
			}
			if _, err := client.Commit(ctx, Transaction{ID: "advance", ExpectedRevision: first.Revision, Writes: []Write{{Namespace: "other", Key: "another", Value: json.RawMessage(`true`)}}}); err != nil {
				t.Fatal(err)
			}
			if _, err := client.Read(ctx, Query{Namespace: "history", Limit: 2, Cursor: first.NextCursor, AtRevision: &first.Revision}); !errors.Is(err, ErrConflict) {
				t.Fatalf("stale page: %v", err)
			}
			zero := Revision(0)
			if _, err := client.Read(ctx, Query{Namespace: "history", Keys: []string{}, AtRevision: &zero}); !errors.Is(err, ErrConflict) {
				t.Fatalf("revision zero lost: %v", err)
			}
			for _, q := range []Query{
				{Namespace: "history", Limit: -1}, {Namespace: "history", Limit: 10001},
				{Namespace: "history", Start: "z", End: "a"}, {Namespace: "history", Cursor: "h/01", Limit: 2},
			} {
				if _, err := client.Read(ctx, q); !errors.Is(err, ErrInvalid) {
					t.Fatalf("invalid %+v: %v", q, err)
				}
			}
		})
	}
}

// Buffered insertions/deletions must be applied before pagination, including
// deletions filling an entire provider page and writes outside the key range.
func TestBoundedTransactionReadYourWrites(t *testing.T) {
	for _, kind := range []string{"sqlite", "jsonl"} {
		t.Run(kind, func(t *testing.T) {
			ctx := context.Background()
			s, err := Open(ctx, t.TempDir(), kind)
			if err != nil {
				t.Fatal(err)
			}
			defer s.Close()
			if err := Update(ctx, s, func(tx *Tx) error {
				for _, key := range []string{"a", "b", "c", "d", "e", "f"} {
					if err := tx.Put("n", key, key); err != nil {
						return err
					}
				}
				return nil
			}); err != nil {
				t.Fatal(err)
			}
			for _, reverse := range []bool{false, true} {
				err := Update(ctx, s, func(tx *Tx) error {
					for _, key := range []string{"a", "b", "e", "f"} {
						if err := tx.Delete("n", key); err != nil {
							return err
						}
					}
					if err := tx.Put("n", "bb", "insert"); err != nil {
						return err
					}
					if err := tx.Put("n", "z", "out of range"); err != nil {
						return err
					}
					q := Query{Namespace: "n", Start: "a", End: "g", Limit: 2, Reverse: reverse}
					got := []string{}
					for {
						v, err := tx.ReadView(q)
						if err != nil {
							return err
						}
						if len(v.Documents) > 2 {
							t.Fatal("buffered writes exceeded limit")
						}
						for _, d := range v.Documents {
							got = append(got, d.Key)
						}
						if v.NextCursor == "" {
							break
						}
						q.Cursor = v.NextCursor
						q.AtRevision = &v.Revision
					}
					want := []string{"bb", "c", "d"}
					if reverse {
						want = []string{"d", "c", "bb"}
					}
					if !reflect.DeepEqual(got, want) {
						t.Fatalf("buffered pages %v, want %v", got, want)
					}
					return errors.New("abort fixture")
				})
				if err == nil || err.Error() != "abort fixture" {
					t.Fatal(err)
				}
			}
		})
	}
}
