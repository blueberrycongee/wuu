package appserver

import (
	"context"
	"encoding/json"
	"testing"

	"github.com/blueberrycongee/wuu/internal/session"
)

// dispatchPayload is a tiny helper around handleLine for tests that need to
// fire a JSON-RPC method and read its response by id from the locked buffer.
func dispatchPayload(t *testing.T, srv *Server, id, method string, params any) {
	t.Helper()
	payload, err := json.Marshal(map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	})
	if err != nil {
		t.Fatalf("marshal %s payload: %v", method, err)
	}
	if err := srv.handleLine(context.Background(), payload); err != nil {
		t.Fatalf("dispatch %s: %v", method, err)
	}
}

func TestServerThreadListArchivedOmitsActiveThreads(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	dispatchPayload(t, srv, "1", "thread/start", nil)
	archivedID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"]).Thread.ID
	dispatchPayload(t, srv, "2", "thread/archive", ThreadArchiveParams{ThreadID: archivedID, Archived: true})
	if resp := responseByID(t, parseOutput(t, out.String()), "2"); resp["error"] != nil {
		t.Fatalf("thread/archive rejected: %+v", resp["error"])
	}
	dispatchPayload(t, srv, "3", "thread/start", nil)
	activeID := remarshal[ThreadStartResult](t, responseByID(t, parseOutput(t, out.String()), "3")["result"]).Thread.ID

	dispatchPayload(t, srv, "4", "thread/listArchived", nil)
	list := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "4")["result"])

	if len(list.Threads) != 1 {
		t.Fatalf("archived list should hold exactly one entry, got %d: %+v", len(list.Threads), list.Threads)
	}
	if list.Threads[0].ID != archivedID {
		t.Fatalf("archived list returned %q, want %q", list.Threads[0].ID, archivedID)
	}

	dispatchPayload(t, srv, "5", "thread/list", ThreadListParams{})
	active := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "5")["result"])

	sawActive := false
	sawArchived := false
	for _, th := range active.Threads {
		if th.ID == activeID {
			sawActive = true
		}
		if th.ID == archivedID {
			sawArchived = true
		}
	}
	if !sawActive {
		t.Fatalf("thread/list missing active thread %q, got %+v", activeID, active.Threads)
	}
	if sawArchived {
		t.Fatalf("thread/list surfaced archived thread %q", archivedID)
	}
}

func TestServerThreadListArchivedIgnoresCallerCWD(t *testing.T) {
	// The Settings → Archive panel is global across every workspace the user
	// has touched. handleThreadListArchived must return archived sessions
	// regardless of the cwd the caller passes, including sessions stored
	// under a workspace path the active runtime never visited.
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)

	foreignID := "foreign-archived"
	foreignCWD := "/tmp/some-other-project"
	if _, err := session.CreateWithMetadata(rt.SessionDir, foreignID, foreignCWD); err != nil {
		t.Fatalf("seed foreign session: %v", err)
	}
	if _, err := session.UpdateArchived(rt.SessionDir, foreignID, true); err != nil {
		t.Fatalf("archive foreign session: %v", err)
	}

	dispatchPayload(t, srv, "1", "thread/listArchived", ThreadListParams{CWD: rt.RootDir})
	list := remarshal[ThreadListResult](t, responseByID(t, parseOutput(t, out.String()), "1")["result"])

	var found *Thread
	for i := range list.Threads {
		if list.Threads[i].ID == foreignID {
			found = &list.Threads[i]
			break
		}
	}
	if found == nil {
		t.Fatalf("thread/listArchived ignored foreign-cwd archived session: %+v", list.Threads)
	}
	if found.CWD != foreignCWD {
		t.Fatalf("archived thread lost its cwd: got %q want %q", found.CWD, foreignCWD)
	}
	if !found.Archived {
		t.Fatalf("archived thread dropped its Archived flag: %+v", found)
	}
}
