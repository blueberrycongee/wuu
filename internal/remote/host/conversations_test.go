package host

import (
	"bufio"
	"context"
	"encoding/json"
	"io"
	"net/http"
	"net/http/httptest"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/account"
	"github.com/blueberrycongee/wuu/internal/remote/conversations"
)

func TestConversationPublisherOptInUpdatesAndSafeDeletion(t *testing.T) {
	store, err := LoadOrCreateStore(t.TempDir()+"/host.json", "test computer")
	if err != nil {
		t.Fatal(err)
	}
	var mu sync.Mutex
	enabled, listFails, removed := false, false, false
	connections, writes := 0, 0
	previous := conversations.Entry{}
	text := "first answer"
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		mu.Lock()
		defer mu.Unlock()
		settings := conversations.Settings{Enabled: enabled, Generation: "generation"}
		switch r.URL.Path {
		case "/v1/account/history/settings":
			json.NewEncoder(w).Encode(settings)
		case "/v1/account/history":
			entries := []conversations.Entry{}
			if previous.ID != "" {
				entries = append(entries, previous)
			}
			json.NewEncoder(w).Encode(conversations.Changes{Settings: settings, Entries: entries, Cursor: "0"})
		case "/v1/account/history/thread":
			var in conversations.Mutation
			if err := json.NewDecoder(r.Body).Decode(&in); err != nil {
				t.Error(err)
			}
			if in.Generation != settings.Generation {
				t.Error("missing generation fence")
			}
			expected := previous.Revision
			if expected == "" {
				expected = "0"
			}
			if in.Expected != expected {
				t.Errorf("CAS expected %s, got %s", expected, in.Expected)
			}
			writes++
			previous = conversations.Entry{ID: in.Thread.ID, Revision: "1", Deleted: in.Deleted}
			// Returning no digest forces an idempotent upload on later passes; the
			// account store's digest behavior is exercised in its HTTP tests.
			json.NewEncoder(w).Encode(previous)
		default:
			t.Errorf("unexpected endpoint %s", r.URL.Path)
			w.WriteHeader(404)
		}
	}))
	defer server.Close()
	h := &Host{store: store, appServer: func(ctx context.Context, in io.Reader, out io.Writer) error {
		mu.Lock()
		connections++
		mu.Unlock()
		if err := json.NewEncoder(out).Encode(map[string]string{"method": "ready-notification"}); err != nil {
			return err
		}
		scanner := bufio.NewScanner(in)
		for scanner.Scan() {
			var req struct {
				ID      string
				Method  string
				Workdir string
			}
			if err := json.Unmarshal(scanner.Bytes(), &req); err != nil {
				return err
			}
			mu.Lock()
			var result any = map[string]any{}
			var rpcError any
			switch req.Method {
			case "initialize":
			case "thread/listAll":
				list := appserver.ThreadListResult{Threads: []appserver.Thread{}}
				if !removed {
					list.Threads = append(list.Threads, appserver.Thread{ID: "thread", CWD: "/workspace"})
				}
				result = list
				if listFails {
					rpcError = map[string]string{"message": "inventory unavailable"}
				}
			case "thread/listArchived":
				result = appserver.ThreadListResult{Threads: []appserver.Thread{}}
			case "thread/textSnapshot":
				if req.Workdir != "/workspace" {
					t.Error("snapshot routed to wrong workspace")
				}
				result = conversations.Thread{ID: "thread", Messages: []conversations.Message{{ID: "answer", TurnID: "turn", Role: "assistant", Text: text}}}
			default:
				t.Errorf("unexpected execution method %s", req.Method)
			}
			mu.Unlock()
			if err := json.NewEncoder(out).Encode(map[string]any{"id": req.ID, "result": result, "error": rpcError}); err != nil {
				return err
			}
		}
		return scanner.Err()
	}}
	c := account.Credentials{Server: server.URL, Token: "test"}
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	pass := func() {
		t.Helper()
		if err := h.publishConversationPass(ctx, c); err != nil {
			t.Fatal(err)
		}
	}
	pass()
	mu.Lock()
	if connections != 0 || writes != 0 {
		t.Fatal("disabled sync read conversation data")
	}
	enabled = true
	mu.Unlock()
	pass()
	mu.Lock()
	if writes != 1 {
		t.Fatal("first upload missing")
	}
	text = "completed answer"
	mu.Unlock()
	pass()
	mu.Lock()
	if writes != 2 {
		t.Fatal("updated answer not uploaded")
	}
	listFails = true
	removed = true
	mu.Unlock()
	if err := h.publishConversationPass(ctx, c); err == nil {
		t.Fatal("inventory failure hidden")
	}
	mu.Lock()
	if writes != 2 {
		t.Fatal("failed inventory deleted copy")
	}
	listFails = false
	mu.Unlock()
	pass()
	mu.Lock()
	defer mu.Unlock()
	if !previous.Deleted || writes != 3 {
		t.Fatal("deleted conversation remains on server")
	}
}
