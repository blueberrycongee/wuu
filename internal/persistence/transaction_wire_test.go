package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"io"
	"reflect"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestTransactionWireDecodeCompatibility(t *testing.T) {
	cases := []string{
		`{"id":"one","expected_revision":"0","writes":[{"namespace":"n","key":"k","value":{"unknown":true,"n":9007199254740993}}]}`,
		`{"ID":"one","EXPECTED_REVISION":"2","WRITES":[{"Namespace":"n","Key":"k","Value": { "x": "<>&\u2028" }}]}`,
		`{"id":"old","id":"new","expected_revision":"0","writes":[{"namespace":"n","key":"k","value":1,"value":2}]}`,
		`{"id":"one","expected_revision":"0","writes":[{"namespace":"n","key":"k","value":1}],"writes":[{"namespace":"other","key":"x","delete":true}]}`,
		`{"id":"one","expected_revision":"0","writes":[{"namespace":"n","key":"k","value":null}]}`,
		`{"id":"one","expected_revision":"0","writes":[{"namespace":"n","key":"k","value":1},null]}`,
		`{"id":"one","expected_revision":"0","writes":null}`, `{}`, `null`,
		`{"id":"one","expected_revision":"0","writes":[]}`, `{"id":"one","id":null}`,
		`{"id":"one","expected_revision":0}`, `{"expected_revision":"-1"}`, `{"expected_revision":"18446744073709551616"}`,
		`{"unknown":1}`, `{"writes":[{"namespace":"n","key":"k","extra":true}]}`,
		`{"writes":[{"namespace":"n","key":"k","value":{"duplicate":1,"duplicate":2}}]}`,
		`{"writes":[{"value": }]}`, `{`, `[]`, `true`, `{} {}`, `{} garbage`,
		"{\"id\":\"\xff\",\"writes\":[{\"value\":\"\xfe\"}]}",
	}
	for _, raw := range cases {
		t.Run(raw, func(t *testing.T) {
			var want, got Transaction
			legacy := json.NewDecoder(bytes.NewReader([]byte(raw)))
			legacy.DisallowUnknownFields()
			wantErr := legacy.Decode(&want)
			if wantErr == nil {
				if err := legacy.Decode(new(any)); err != io.EOF {
					wantErr = errors.New("trailing JSON")
				}
			}
			input := []byte(raw)
			gotErr := decode(input, &got)
			if (gotErr == nil) != (wantErr == nil) {
				t.Fatalf("acceptance differs: got %v want %v", gotErr, wantErr)
			}
			if gotErr != nil && !errors.Is(gotErr, ErrInvalid) {
				t.Fatalf("lost invalid category: %v", gotErr)
			}
			if wantErr == nil && !reflect.DeepEqual(got, want) {
				t.Fatalf("decoded transaction differs: got %#v want %#v", got, want)
			}
			if string(input) != raw {
				t.Fatal("decoder changed caller bytes")
			}
		})
	}
}

func TestTransactionClientWireCompatibility(t *testing.T) {
	for _, tx := range []Transaction{
		{ID: "html<>&\u2028", ExpectedRevision: 9007199254740993, Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(` {"big":9007199254740993, "html":"<>&", "dupe":1,"dupe":2} `)}, {Namespace: "n", Key: "gone", Delete: true}}},
		{ID: "empty", Writes: []Write{}}, {ID: "nil"},
		{ID: "invalid", Writes: []Write{{Namespace: "n", Key: "k", Value: json.RawMessage(`{`)}}},
	} {
		t.Run(tx.ID, func(t *testing.T) {
			want, wantErr := json.Marshal(tx)
			calls := 0
			client := NewClient(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
				calls++
				if method != "commit" || !bytes.Equal(raw, want) {
					t.Fatalf("wire bytes changed: %s", raw)
				}
				return json.Marshal(Receipt{ID: tx.ID, Revision: tx.ExpectedRevision + 1})
			})
			got, err := client.Commit(context.Background(), tx)
			if (err == nil) != (wantErr == nil) {
				t.Fatalf("marshal failure differs: %v / %v", err, wantErr)
			}
			if wantErr != nil {
				if calls != 0 {
					t.Fatal("invalid request invoked provider")
				}
				return
			}
			if calls != 1 || got.ID != tx.ID || got.Revision != tx.ExpectedRevision+1 {
				t.Fatalf("wrong commit outcome: %#v calls=%d", got, calls)
			}
		})
	}
	ctx, cancel := context.WithCancel(context.Background())
	cancel()
	for code, cause := range map[string]error{"state_conflict": ErrConflict, "state_operation_conflict": ErrOperationConflict, "state_binding_changed": ErrBindingChanged, "state_migration_required": ErrMigrationRequired, "state_corrupt": ErrCorrupt, "invalid_request": ErrInvalid, "request_cancelled": context.Canceled} {
		client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) {
			return nil, &pluginhost.HostServiceError{Code: code, Message: "transport failure"}
		})
		_, err := client.Commit(ctx, Transaction{})
		if !errors.Is(err, cause) {
			t.Fatalf("%s category lost: %v", code, err)
		}
	}
}
