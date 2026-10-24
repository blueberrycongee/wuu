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

func TestSnapshotExportOwnsReturnedValues(t *testing.T) {
	raw := json.RawMessage(`{"version":1,"revision":"0","documents":[{"namespace":"n","key":"a","value":"original"},{"namespace":"n","key":"b","value":"second"}],"receipts":[]}`)
	client := NewClient(func(context.Context, string, json.RawMessage) (json.RawMessage, error) { return raw, nil })
	snapshot, err := client.Export(context.Background())
	if err != nil {
		t.Fatal(err)
	}
	// The completed callback may reuse its response storage for a later call.
	for i := range raw {
		raw[i] = ' '
	}
	if string(snapshot.Documents[0].Value) != `"original"` || string(snapshot.Documents[1].Value) != `"second"` {
		t.Fatal("returned snapshot still aliases callback storage")
	}
}

func TestClientCheckpointMatchesExport(t *testing.T) {
	expected := Snapshot{Version: 1, Documents: []Document{{Namespace: "n", Key: "key", Value: json.RawMessage(`{"big":9007199254740993,"html":"<>&"}`)}}, Receipts: []CommitRecord{}}
	want, err := SnapshotCheckpoint(expected)
	if err != nil {
		t.Fatal(err)
	}
	client, calls := testCheckpointClient(t, expected, nil)
	got, err := CaptureCheckpoint(context.Background(), client)
	if err != nil || got != want || *calls != 2 {
		t.Fatalf("checkpoint mismatch: %#v / %#v, calls %d, err %v", got, want, *calls, err)
	}
	expected.Revision = 1
	client, _ = testCheckpointClient(t, expected, nil)
	if _, err := CaptureCheckpoint(context.Background(), client); !errors.Is(err, ErrCorrupt) {
		t.Fatalf("missing receipts accepted: %v", err)
	}
}
