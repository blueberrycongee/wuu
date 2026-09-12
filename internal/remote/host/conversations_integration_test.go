package host

import (
	"context"
	"encoding/base64"
	"net/http/httptest"
	"net/url"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/conversations"
	"github.com/blueberrycongee/wuu/internal/remote/pgtest"
	"github.com/blueberrycongee/wuu/internal/remote/secure"
	"github.com/blueberrycongee/wuu/internal/runtime"
	"github.com/blueberrycongee/wuu/internal/session"
)

// Use the production snapshot RPC, publisher, HTTP API and PostgreSQL together.
// Reading and publishing persisted conversations must never require an Agent.
func TestConversationPublisherPersistsHistoryWithoutAnOnlineHost(t *testing.T) {
	dsn := pgtest.URL(t)
	db, err := account.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { db.Close() }()
	handler := account.NewHTTP(db, true)
	server := httptest.NewServer(handler)
	defer server.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	root := t.TempDir()
	store, err := LoadOrCreateStore(filepath.Join(root, "remote.json"), "history host")
	if err != nil {
		t.Fatal(err)
	}
	login := func(id *secure.Identity, role string, register bool) account.Session {
		t.Helper()
		path := "/login"
		if register {
			path = "/register"
		}
		var result account.Session
		in := account.Login{Username: "history-test", Password: "test-history-password", Pub: secure.EncodeKey(id.Public()), Name: role, Role: role,
			Proof: base64.RawURLEncoding.EncodeToString(id.SignRelayAuth([]byte("wuu/account/enroll/v1:history-test"), role))}
		if err := account.Request(ctx, server.URL, "", "POST", path, in, &result); err != nil {
			t.Fatal(err)
		}
		return result
	}
	hostSession := login(store.Identity(), "host", true)
	phoneID, err := secure.NewIdentity()
	if err != nil {
		t.Fatal(err)
	}
	phone := login(phoneID, "phone", false)
	c := account.Credentials{Server: server.URL, Token: hostSession.Token, Username: hostSession.Username}
	rt := &runtime.Session{RootDir: root, ConfigPath: filepath.Join(root, "config.json"), SessionDir: filepath.Join(root, "sessions"), StreamRunner: &agent.StreamRunner{}}
	h := &Host{store: store, rt: rt}
	thread, err := session.CreateWithMetadata(rt.SessionDir, "server-history", root)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdateTitle(rt.SessionDir, thread.ID, "Stored on the account server"); err != nil {
		t.Fatal(err)
	}
	appendText := func(role, text string) {
		t.Helper()
		if err := session.AppendHistoryRecord(rt.SessionDir, thread.ID, session.HistoryRecord{Role: role, Content: text, At: time.Now().UTC()}); err != nil {
			t.Fatal(err)
		}
	}
	appendText("user", "Remember this conversation")
	appendText("assistant", "First response")
	request := func(method, path string, in, out any) {
		t.Helper()
		if err := account.Request(ctx, server.URL, phone.Token, method, path, in, out); err != nil {
			t.Fatal(err)
		}
	}
	var state conversations.Settings
	settingsPath := "/history/settings?host=" + url.QueryEscape(hostSession.Pub)
	request("GET", settingsPath, nil, &state)
	if state.Enabled {
		t.Fatal("history sync enabled without consent")
	}
	pass := func() {
		t.Helper()
		if err := h.publishConversationPass(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	pass()
	var page conversations.Changes
	changesPath := "/history?host=" + url.QueryEscape(hostSession.Pub)
	request("GET", changesPath, nil, &page)
	if len(page.Entries) != 0 {
		t.Fatal("disabled sync uploaded history")
	}
	request("POST", "/history/settings", map[string]any{"host": hostSession.Pub, "enabled": true}, &state)
	pass()
	request("GET", changesPath, nil, &page)
	if len(page.Entries) != 1 || page.Entries[0].ID != thread.ID {
		t.Fatalf("missing server copy: %+v", page)
	}
	cursor := page.Cursor
	read := func() conversations.Thread {
		t.Helper()
		var result struct {
			Thread conversations.Thread `json:"thread"`
		}
		query := url.Values{"host": {hostSession.Pub}, "generation": {state.Generation}, "id": {thread.ID}}
		request("GET", "/history/thread?"+query.Encode(), nil, &result)
		return result.Thread
	}
	if body := read(); len(body.Messages) != 2 || body.Messages[1].Text != "First response" {
		t.Fatalf("incomplete persisted text: %+v", body)
	}
	// Restart the account service with no publisher/relay connection running.
	server.Close()
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}
	db, err = account.Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	server = httptest.NewServer(account.NewHTTP(db, false))
	defer server.Close()
	c.Server = server.URL
	if body := read(); len(body.Messages) != 2 {
		t.Fatal("server restart lost offline history")
	}
	pass()
	request("GET", changesPath+"&generation="+state.Generation+"&after="+cursor, nil, &page)
	if len(page.Entries) != 0 {
		t.Fatal("unchanged history generated another revision")
	}
	appendText("user", "Continue on the computer")
	appendText("assistant", "Response after reconnect")
	pass()
	request("GET", changesPath+"&generation="+state.Generation+"&after="+cursor, nil, &page)
	if len(page.Entries) != 1 || page.Cursor == cursor {
		t.Fatal("reconnected host did not publish new text")
	}
	if body := read(); len(body.Messages) != 4 || body.Messages[3].Text != "Response after reconnect" {
		t.Fatalf("missing continuation: %+v", body)
	}
	if _, err := session.UpdateArchived(rt.SessionDir, thread.ID, true); err != nil {
		t.Fatal(err)
	}
	pass()
	if len(read().Messages) != 4 {
		t.Fatal("archiving deleted the server history")
	}
	if _, err := session.Delete(rt.SessionDir, thread.ID); err != nil {
		t.Fatal(err)
	}
	pass()
	request("GET", changesPath, nil, &page)
	if len(page.Entries) != 1 || !page.Entries[0].Deleted {
		t.Fatal("deleted conversation retained on server")
	}
	request("POST", "/history/settings", map[string]any{"host": hostSession.Pub, "enabled": false}, &state)
	request("GET", changesPath, nil, &page)
	if page.Enabled || len(page.Entries) != 0 {
		t.Fatal("disabling sync retained server records")
	}
	request("POST", "/logout", nil, nil)
	if err := account.Request(ctx, server.URL, phone.Token, "GET", settingsPath, nil, nil); !account.Unauthorized(err) {
		t.Fatalf("revoked phone retained history access: %v", err)
	}
}
