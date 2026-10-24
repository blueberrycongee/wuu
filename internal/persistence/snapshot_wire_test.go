package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"reflect"
	"testing"
)

// Export consumers must retain every document and receipt, tolerate the same
// unknown fields as the old client, and reject malformed/trailing responses.
func TestSnapshotClientWireCompatibility(t *testing.T) {
	for _, raw := range []string{
		`{"version":1,"revision":"1","documents":[{"namespace":"n","key":"k","value": { "n":9007199254740993,"same":1,"same":2,"html":"<>&" }}],"receipts":[{"id":"one","revision":"1","digest":"digest"}]}`,
		`{"Version":1,"revision":"0","documents":[],"receipts":[],"unknown":true}`,
		`{"version":1,"documents":[{"namespace":"n","key":"k","value":null}],"receipts":null}`,
		`{"version":1,"documents":[{"namespace":"n","key":"k","value":1}],"documents":null}`,
		`null`, `{}`, `{} {}`, `{"revision":1}`, `{"documents":[{"value":}]}`,
		"{\"documents\":[{\"namespace\":\"\xff\",\"value\":\"\xfe\"}]}",
	} {
		var want Snapshot
		wantErr := json.Unmarshal([]byte(raw), &want)
		input := json.RawMessage(raw)
		client := NewClient(func(_ context.Context, method string, request json.RawMessage) (json.RawMessage, error) {
			if method != "export" || !bytes.Equal(request, []byte(`{}`)) {
				t.Fatal("export request changed")
			}
			return input, nil
		})
		got, err := client.Export(context.Background())
		if (err == nil) != (wantErr == nil) {
			t.Fatalf("acceptance differs: %v / %v", err, wantErr)
		}
		if wantErr == nil && !reflect.DeepEqual(want, got) {
			t.Fatalf("snapshot changed: %#v / %#v", got, want)
		}
		if string(input) != raw {
			t.Fatal("response mutated")
		}
	}
	failure := errors.New("lost export response")
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return nil, failure })
	if _, err := client.Export(context.Background()); !errors.Is(err, failure) {
		t.Fatalf("lost transport error: %v", err)
	}
}
