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
	sessions := testSessionStore(t, sessDir)
	if _, err := sessions.CreateWithMetadata("source", t.TempDir()); err != nil {
		t.Fatalf("create session: %v", err)
	}
	if err := sessions.AppendHistoryRecord("source", session.HistoryRecord{Role: "user", Content: "keep this fact"}); err != nil {
		t.Fatalf("append: %v", err)
	}
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, t.TempDir(), home, nil)
	handler.sessions = sessions
	raw, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"source","start_seq":1}`))
	if err != nil {
		t.Fatal(err)
	}
	var page pluginhost.SessionHistoryPage
	if err := json.Unmarshal(raw, &page); err != nil {
		t.Fatal(err)
	}
	if page.SessionID != "source" || len(page.Records) != 1 || page.Records[0].Content != "keep this fact" {
		t.Fatalf("page = %+v", page)
	}
}

func TestHistoryServiceHidesForeignPluginSessions(t *testing.T) {
	home := t.TempDir()
	sessDir := statepath.SessionsDir(home)
	sessions := testSessionStore(t, sessDir)
	if _, err := sessions.CreateManagedWithMetadata("hidden", t.TempDir(), session.ManagedMetadata{
		Owner: "plugin:other", Visibility: pluginhost.SessionVisibilityPlugin,
	}); err != nil {
		t.Fatalf("create hidden: %v", err)
	}
	item := serviceTestPlugin("demo", "plugin:user:demo", "generation")
	handler := newPluginHostServices(item, t.TempDir(), home, nil)
	handler.sessions = sessions
	_, err := handler.HandleHostService(context.Background(), pluginhost.HostServiceSessionHistoryRead, json.RawMessage(`{"session_id":"hidden","start_seq":1}`))
	if err == nil {
		t.Fatal("expected hidden session to be unavailable")
	}
}
