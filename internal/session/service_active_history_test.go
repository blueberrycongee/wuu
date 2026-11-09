package session

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

func TestSessionActiveHistoryQueryRevokesRetractedSources(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, native *documentStore) {
		client := NewStore(context.Background(), native.documents)
		records := []HistoryRecord{{Role: "user", Content: "retained", ClientID: "one"}, {Role: "assistant", Content: "first reply"}, {Role: "user", Content: "edited source", ClientID: "two"}, {Role: "assistant", Content: "old reply"}}
		if _, err := client.CreateInitialized(Session{ID: "active-source"}, records); err != nil {
			t.Fatal(err)
		}
		query := HistoryReadQuery{SessionID: "active-source", StartSeq: 3, EndSeq: 3, Limit: 1}
		page, err := client.ReadActiveHistoryQuery(query)
		if err != nil || len(page.Records) != 1 || page.Records[0].ClientID != "two" {
			t.Fatalf("exact source: %+v %v", page, err)
		}
		query.SnapshotSeq = page.SnapshotSeq
		records[0].Seq, records[1].Seq = 1, 2
		if err := client.RewriteHistoryRecordsForEdit("active-source", records[:2], 3, page.HeadSeq); err != nil {
			t.Fatal(err)
		}
		physical, err := client.ReadHistoryQuery(query)
		if err != nil || len(physical.Records) != 1 {
			t.Fatalf("audit source lost: %+v %v", physical, err)
		}
		page, err = client.ReadActiveHistoryQuery(query)
		if err != nil || len(page.Records) != 0 {
			t.Fatalf("retracted source revived through an older snapshot: %+v %v", page, err)
		}
		page, err = client.ReadActiveHistoryQuery(HistoryReadQuery{SessionID: "active-source", StartSeq: 1, Limit: 1})
		if err != nil || len(page.Records) != 1 || !page.HasMore || page.Next == nil {
			t.Fatalf("active page: %+v %v", page, err)
		}
		next, err := client.ReadActiveHistoryQuery(HistoryReadQuery{SessionID: "active-source", Limit: 1, Cursor: page.Next})
		if err != nil || len(next.Records) != 1 || next.Records[0].Seq != 2 || next.HasMore {
			t.Fatalf("active continuation: %+v %v", next, err)
		}
	})
}

func TestSessionActiveHistoryQueryRequiresSelectedMethod(t *testing.T) {
	calls := 0
	client := NewClient(context.Background(), func(_ context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
		calls++
		if method != "ReadActiveHistoryQuery" {
			t.Fatalf("substituted another history contract: %s", method)
		}
		return nil, &pluginhost.HostServiceError{Code: "method_not_found", Message: "active query unsupported"}
	})
	if _, err := client.ReadActiveHistoryQuery(HistoryReadQuery{SessionID: "source", StartSeq: 1, EndSeq: 1, Limit: 1}); err == nil || calls != 1 {
		t.Fatalf("unsupported provider did not fail closed: %v calls=%d", err, calls)
	}
}
