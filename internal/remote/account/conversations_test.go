package account

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http/httptest"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/remote/conversations"
	"github.com/blueberrycongee/wuu/internal/remote/pgtest"
)

func TestConversationSyncIsolationRecoveryAndDeletion(t *testing.T) {
	dsn := pgtest.URL(t)
	s, err := Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	defer func() { s.Close() }()
	login := func(user, role string, register bool) Session {
		t.Helper()
		result, err := s.Login(loginInput(t, user, role), register)
		if err != nil {
			t.Fatal(err)
		}
		return result
	}
	host := login("alice", "host", true)
	phone := login("alice", "phone", false)
	bob := login("bobby", "host", true)
	handler := NewHTTP(s, false)
	request := func(token, method, path, body string, want int, out any) {
		t.Helper()
		r := httptest.NewRequest(method, "/v1/account"+path, strings.NewReader(body))
		r.Header.Set("Authorization", "Bearer "+token)
		w := httptest.NewRecorder()
		handler.ServeHTTP(w, r)
		if w.Code != want {
			t.Fatalf("%s %s: %d %s", method, path, w.Code, w.Body.String())
		}
		if out != nil {
			if err := json.Unmarshal(w.Body.Bytes(), out); err != nil {
				t.Fatal(err)
			}
		}
	}
	settingsURL := "/history/settings?host=" + host.Pub
	var settings conversations.Settings
	request(phone.Token, "GET", settingsURL, "", 200, &settings)
	if settings.Enabled {
		t.Fatal("existing account silently opted into sync")
	}
	thread := conversations.Thread{ID: "thread-1", Title: "A decision", UpdatedAt: "2026-09-11T00:00:00Z", Messages: []conversations.Message{{ID: "m1", TurnID: "t1", Role: "user", Text: "private context"}}}
	mutation := conversations.Mutation{Generation: settings.Generation, Expected: "0", Thread: thread}
	put := func(token string, want int) conversations.Entry {
		t.Helper()
		raw, err := json.Marshal(mutation)
		if err != nil {
			t.Fatal(err)
		}
		var entry conversations.Entry
		request(token, "POST", "/history/thread", string(raw), want, &entry)
		return entry
	}
	put(host.Token, 409)
	request(bob.Token, "GET", settingsURL, "", 401, nil)
	request(phone.Token, "POST", "/history/settings", fmt.Sprintf(`{"host":%q,"enabled":true}`, host.Pub), 200, &settings)
	mutation.Generation = settings.Generation
	put(phone.Token, 401)
	entry := put(host.Token, 200)
	put(host.Token, 409)
	mutation.Expected = entry.Revision
	if next := put(host.Token, 200); next.Revision != entry.Revision {
		t.Fatal("unchanged upload advanced the cursor")
	}
	var changes conversations.Changes
	request(phone.Token, "GET", "/history?host="+host.Pub, "", 200, &changes)
	if len(changes.Entries) != 1 || changes.Entries[0].ID != thread.ID {
		t.Fatalf("missing synced conversation: %+v", changes)
	}
	threadURL := "/history/thread?host=" + host.Pub + "&generation=" + settings.Generation + "&id=" + thread.ID
	var snapshot struct {
		Thread conversations.Thread `json:"thread"`
	}
	request(phone.Token, "GET", threadURL, "", 200, &snapshot)
	if snapshot.Thread.Messages[0].Text != "private context" {
		t.Fatal("history lost text")
	}
	request(bob.Token, "GET", threadURL, "", 401, nil)
	request(bob.Token, "GET", "/history?host="+host.Pub, "", 401, nil)
	request(bob.Token, "POST", "/history/settings", fmt.Sprintf(`{"host":%q,"enabled":false}`, host.Pub), 401, nil)
	// Reopening the store proves history is independent of a host connection.
	if err := s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dsn)
	if err != nil {
		t.Fatal(err)
	}
	handler.Store = s
	request(phone.Token, "GET", threadURL, "", 200, &snapshot)
	mutation.Thread.Messages[0].Text = "corrected context"
	entry = put(host.Token, 200)
	request(phone.Token, "GET", "/history?host="+host.Pub+"&generation="+settings.Generation+"&after="+changes.Cursor, "", 200, &changes)
	if len(changes.Entries) != 1 || changes.Cursor != entry.Revision {
		t.Fatal("edit missing from delta")
	}
	mutation.Expected, mutation.Deleted = entry.Revision, true
	entry = put(host.Token, 200)
	request(phone.Token, "GET", threadURL, "", 404, nil)
	request(phone.Token, "GET", "/history?host="+host.Pub+"&generation="+settings.Generation+"&after="+changes.Cursor, "", 200, &changes)
	if len(changes.Entries) != 1 || !changes.Entries[0].Deleted {
		t.Fatal("deletion missing from delta")
	}
	request(phone.Token, "POST", "/history/settings", fmt.Sprintf(`{"host":%q,"enabled":false}`, host.Pub), 200, &settings)
	mutation.Expected = entry.Revision
	put(host.Token, 409)
	request(phone.Token, "GET", "/history?host="+host.Pub, "", 200, &changes)
	if changes.Enabled || len(changes.Entries) != 0 {
		t.Fatal("disabled copy retained data")
	}
	request(phone.Token, "POST", "/history/settings", fmt.Sprintf(`{"host":%q,"enabled":true}`, host.Pub), 200, &settings)
	put(host.Token, 409) // an old generation cannot resurrect the deleted copy
	if err := s.Revoke("alice", phone.Pub); err != nil {
		t.Fatal(err)
	}
	request(phone.Token, "GET", settingsURL, "", 401, nil)
}

func TestConversationDeltaPagination(t *testing.T) {
	s, err := Open(pgtest.URL(t))
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	host, err := s.Login(loginInput(t, "alice", "host"), true)
	if err != nil {
		t.Fatal(err)
	}
	d, err := s.Authenticate(host.Token)
	if err != nil {
		t.Fatal(err)
	}
	enabled := true
	state, err := s.ConversationSettings(context.Background(), d, d.Pub, &enabled)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 105; i++ {
		_, err := s.PutConversation(context.Background(), d, conversations.Mutation{Generation: state.Generation, Expected: "0", Thread: conversations.Thread{ID: fmt.Sprintf("thread-%d", i)}})
		if err != nil {
			t.Fatal(err)
		}
	}
	page, err := s.ConversationChanges(context.Background(), d, d.Pub, state.Generation, 0)
	if err != nil || len(page.Entries) != 100 || !page.More {
		t.Fatalf("first page: %+v %v", page, err)
	}
	page, err = s.ConversationChanges(context.Background(), d, d.Pub, state.Generation, 100)
	if err != nil || len(page.Entries) != 5 || page.More || page.Cursor != "105" {
		t.Fatalf("second page: %+v %v", page, err)
	}
}
