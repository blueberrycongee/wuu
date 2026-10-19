package runtime

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/session"
	"github.com/blueberrycongee/wuu/internal/statepath"
)

func TestHistoryServiceReadsVisibleSessionWithoutNotePlugin(t *testing.T) {
	home := t.TempDir()
	sessDir := statepath.SessionsDir(home)
	if _, err := session.CreateWithMetadata(sessDir, "source", t.TempDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, "source", session.HistoryRecord{Role: "user", Content: "keep this fact"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	if err := session.AppendHistoryRecord(sessDir, "source", session.HistoryRecord{Role: "user", Content: "durable plugin context", Hidden: true, ReadOnly: true, Origin: "plugin"}); err != nil {
		t.Fatalf("append plugin context: %v", err)
	}
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, t.TempDir(), home, nil)
	raw, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"source","start_seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var page pluginhost.SessionHistoryPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if page.SessionID != "source" || len(page.Records) != 2 || page.Records[0].Content != "keep this fact" || page.Records[1].Content != "durable plugin context" {
		t.Fatalf("page = %+v", page)
	}
	var provenance struct {
		Records []struct {
			Seq      int    `json:"seq"`
			Hidden   bool   `json:"hidden"`
			ReadOnly bool   `json:"read_only"`
			Origin   string `json:"origin"`
		} `json:"records"`
	}
	if err := json.Unmarshal(raw, &provenance); err != nil {
		t.Fatal(err)
	}
	if record := provenance.Records[1]; record.Seq != 2 || !record.Hidden || !record.ReadOnly || record.Origin != "plugin" {
		t.Fatalf("raw audit history lost its presentation provenance: %+v", record)
	}
}

func TestHistoryServiceHidesForeignPluginSessions(t *testing.T) {
	home := t.TempDir()
	sessDir := statepath.SessionsDir(home)
	if _, err := session.CreateManagedWithMetadata(sessDir, "hidden", t.TempDir(), session.ManagedMetadata{
		Owner: "plugin:other", Visibility: pluginhost.SessionVisibilityPlugin,
	}); err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, t.TempDir(), home, nil)
	_, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"hidden","start_seq":1}`))
	if err == nil {
		t.Fatal("expected hidden session to be unavailable")
	}
}

// Provider and native engine identities are useful for reconciling stream items,
// but neither may be relabeled as a host item/turn ID. Older rows omit both.
func TestHistoryServicePreservesProviderAndEngineIdentities(t *testing.T) {
	home := t.TempDir()
	sessDir := statepath.SessionsDir(home)
	if _, err := session.CreateWithMetadata(sessDir, "source", t.TempDir()); err != nil {
		t.Fatal(err)
	}
	for _, record := range []session.HistoryRecord{{Role: "user", Content: "question"}, {Role: "assistant", Content: "answer", ProviderItemID: "provider-output", EngineTurnID: "native-turn"}} {
		if err := session.AppendHistoryRecord(sessDir, "source", record); err != nil {
			t.Fatal(err)
		}
	}
	page, err := readPluginSessionHistory(context.Background(), home, "demo", pluginhost.SessionHistoryReadParams{SessionID: "source", StartSeq: 1})
	if err != nil {
		t.Fatal(err)
	}
	if len(page.Records) != 2 || page.Records[1].Seq != 2 || page.Records[1].ProviderItemID != "provider-output" || page.Records[1].EngineTurnID != "native-turn" {
		t.Fatalf("lost persisted identities: %+v", page)
	}
	raw, err := json.Marshal(page.Records)
	if err != nil {
		t.Fatal(err)
	}
	var records []map[string]any
	if err := json.Unmarshal(raw, &records); err != nil {
		t.Fatal(err)
	}
	if records[0]["provider_item_id"] != nil || records[0]["engine_turn_id"] != nil {
		t.Fatalf("legacy row gained invented identities: %s", raw)
	}
	if records[1]["item_id"] != nil || records[1]["turn_id"] != nil {
		t.Fatalf("provider identity mislabeled as host identity: %s", raw)
	}
}
