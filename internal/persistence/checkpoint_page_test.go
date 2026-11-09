package persistence

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func testCheckpointSnapshot() Snapshot {
	return Snapshot{Version: 1, Revision: 2, Documents: []Document{
		{Namespace: "n", Key: "a", Value: json.RawMessage(`{"big":9007199254740993,"html":"<>&","same":1,"same":2}`)},
		{Namespace: "n", Key: "b", Value: json.RawMessage(`true`)},
	}, Receipts: []CommitRecord{
		{Receipt: Receipt{ID: "one", Revision: 1}, Digest: strings.Repeat("1", 64)},
		{Receipt: Receipt{ID: "two", Revision: 2}, Digest: strings.Repeat("2", 64)},
	}}
}
func testCheckpointClient(t *testing.T, snapshot Snapshot, mutate func(int, *testExportPage)) (*Client, *int) {
	t.Helper()
	calls := new(int)
	client := NewClient(func(_ context.Context, method string, raw json.RawMessage) (json.RawMessage, error) {
		*calls++
		var request struct {
			Page *testExportQuery `json:"page"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		if method != "export" || request.Page == nil {
			return nil, errors.New("checkpoint attempted an unbounded export")
		}
		q := request.Page
		if *calls > 1 && (q.AtRevision == nil || *q.AtRevision != snapshot.Revision || q.Generation != "fixture") {
			return nil, errors.New("checkpoint lost revision/generation pin")
		}
		page := testExportPage{Format: "page.v1", Revision: snapshot.Revision, Generation: "fixture", Section: q.Section, Documents: []Document{}, Receipts: []CommitRecord{}}
		index := 0
		if q.Section == "documents" {
			if q.After != nil {
				for index < len(snapshot.Documents) && (snapshot.Documents[index].Namespace < q.After.Namespace || snapshot.Documents[index].Namespace == q.After.Namespace && snapshot.Documents[index].Key <= q.After.Key) {
					index++
				}
			}
			if index < len(snapshot.Documents) {
				page.Documents = append(page.Documents, snapshot.Documents[index])
				index++
			}
			page.Done = index == len(snapshot.Documents)
		} else {
			if q.After != nil && q.After.Revision != nil {
				index = int(*q.After.Revision)
			}
			if index < len(snapshot.Receipts) {
				page.Receipts = append(page.Receipts, snapshot.Receipts[index])
				index++
			}
			page.Done = index == len(snapshot.Receipts)
		}
		if mutate != nil {
			mutate(*calls, &page)
		}
		return json.Marshal(page)
	})
	return client, calls
}

func TestCheckpointConsumesBoundedPages(t *testing.T) {
	snapshot := testCheckpointSnapshot()
	want, err := SnapshotCheckpoint(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	client, calls := testCheckpointClient(t, snapshot, nil)
	got, err := CaptureCheckpoint(context.Background(), client)
	if err != nil || got != want || *calls != 4 {
		t.Fatalf("paged checkpoint: got %+v want %+v calls=%d err=%v", got, want, *calls, err)
	}
}

func TestCheckpointRejectsMalformedPages(t *testing.T) {
	for name, mutate := range map[string]func(int, *testExportPage){
		"duplicate-document": func(n int, p *testExportPage) {
			if n == 2 {
				p.Documents[0].Key = "a"
			}
		},
		"changed-generation": func(n int, p *testExportPage) {
			if n == 2 {
				p.Generation = "replacement"
			}
		},
		"changed-revision": func(n int, p *testExportPage) {
			if n == 2 {
				p.Revision++
			}
		},
		"empty-nonterminal": func(n int, p *testExportPage) {
			if n == 1 {
				p.Documents = nil
				p.Done = false
			}
		},
		"wrong-section": func(n int, p *testExportPage) {
			if n == 1 {
				p.Section = "receipts"
			}
		},
		"wrong-format": func(n int, p *testExportPage) {
			if n == 1 {
				p.Format = "snapshot"
			}
		},
		"mixed-section": func(n int, p *testExportPage) {
			if n == 1 {
				p.Receipts = []CommitRecord{{Receipt: Receipt{ID: "x", Revision: 1}, Digest: strings.Repeat("1", 64)}}
			}
		},
		"receipt-gap": func(n int, p *testExportPage) {
			if n == 3 {
				p.Receipts[0].Revision = 2
			}
		},
		"duplicate-receipt-id": func(n int, p *testExportPage) {
			if n == 4 {
				p.Receipts[0].ID = "one"
			}
		},
		"invalid-receipt-digest": func(n int, p *testExportPage) {
			if n == 4 {
				p.Receipts[0].Digest = "bad"
			}
		},
		"missing-receipt": func(n int, p *testExportPage) {
			if n == 3 {
				p.Done = true
			}
		},
	} {
		t.Run(name, func(t *testing.T) {
			mutated := false
			client, _ := testCheckpointClient(t, testCheckpointSnapshot(), func(n int, page *testExportPage) { mutated = true; mutate(n, page) })
			if _, err := CaptureCheckpoint(context.Background(), client); err == nil || !mutated {
				t.Fatal("malformed page accepted")
			}
		})
	}
}

func TestCheckpointDetectsOmittedDocument(t *testing.T) {
	snapshot := testCheckpointSnapshot()
	want, err := SnapshotCheckpoint(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	snapshot.Documents = snapshot.Documents[:1]
	client, _ := testCheckpointClient(t, snapshot, nil)
	if err := VerifyCheckpoint(context.Background(), client, want); !errors.Is(err, ErrMigrationRequired) {
		t.Fatalf("omission not detected against trusted checkpoint: %v", err)
	}
}

// A JavaScript provider's valid JSON may use literal HTML characters. Its wire
// budget is measured before the host's distinct checkpoint canonicalization.
func TestCheckpointUsesActualPageWireBudget(t *testing.T) {
	snapshot := Snapshot{Version: 1, Documents: []Document{{Namespace: "n", Key: "a", Value: json.RawMessage(`"` + strings.Repeat("<", 3<<20) + `"`)}, {Namespace: "n", Key: "b", Value: json.RawMessage(`"` + strings.Repeat("<", 3<<20) + `"`)}}, Receipts: []CommitRecord{}}
	want, err := SnapshotCheckpoint(snapshot)
	if err != nil {
		t.Fatal(err)
	}
	client := NewClient(func(_ context.Context, _ string, raw json.RawMessage) (json.RawMessage, error) {
		var request struct {
			Page testExportQuery `json:"page"`
		}
		if err := json.Unmarshal(raw, &request); err != nil {
			return nil, err
		}
		page := testExportPage{Format: "page.v1", Generation: "external", Section: request.Page.Section, Documents: []Document{}, Receipts: []CommitRecord{}, Done: true}
		if page.Section == "documents" {
			page.Documents = snapshot.Documents
		}
		var output bytes.Buffer
		encoder := json.NewEncoder(&output)
		encoder.SetEscapeHTML(false)
		if err := encoder.Encode(page); err != nil {
			return nil, err
		}
		if output.Len() > request.Page.TargetBytes {
			t.Fatalf("fixture exceeded actual wire target: %d", output.Len())
		}
		return output.Bytes(), nil
	})
	got, err := CaptureCheckpoint(context.Background(), client)
	if err != nil || got != want {
		t.Fatalf("valid external wire page rejected or checkpoint changed: %v", err)
	}
}
