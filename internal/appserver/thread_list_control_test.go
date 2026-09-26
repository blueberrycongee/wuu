package appserver

import (
	"fmt"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/session"
)

func TestThreadListsPreserveSessionControlAcrossWorkspaces(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	ownerOutput := &lockedBuffer{}
	owner := &rpcClient{server: New(rt, ownerOutput), out: ownerOutput}
	t.Cleanup(owner.server.Close)
	expected := make(map[string]*ThreadSessionControl)
	for _, state := range []string{session.ControlActive, session.ControlPaused, session.ControlTakenOver, session.ControlReleased, "ordinary"} {
		id := "session-" + state
		if _, err := session.CreateWithMetadata(rt.SessionDir, id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
		expected[id] = nil
		if state != "ordinary" {
			control, err := session.ChangeControl(rt.SessionDir, id, "manager-a", state, 0)
			if err != nil {
				t.Fatal(err)
			}
			if state != session.ControlReleased {
				expected[id] = &ThreadSessionControl{ManagerID: control.ManagerID, State: control.State, Revision: control.Revision}
			}
		}
		loaded, err := owner.server.loadPersistedThreadState(id, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		owner.server.threads[id] = loaded
	}

	// Switching projects selects a different app-server. It shares the store,
	// but has never loaded the original project's managed conversations.
	otherRuntime := newTestRuntime(t, &fakeClient{})
	otherRuntime.SessionDir = rt.SessionDir
	otherOutput := &lockedBuffer{}
	other := &rpcClient{server: New(otherRuntime, otherOutput), out: otherOutput}
	t.Cleanup(other.server.Close)

	assertControl := func(t *testing.T, thread Thread) {
		t.Helper()
		want, ok := expected[thread.ID]
		if !ok {
			t.Fatalf("unexpected thread %q", thread.ID)
		}
		got := thread.SessionControl
		if want == nil {
			if got != nil {
				t.Fatalf("%s retained released or absent control: %+v", thread.ID, got)
			}
		} else if got == nil || got.ManagerID != want.ManagerID || got.State != want.State || got.Revision != want.Revision {
			t.Fatalf("%s lost persisted management: got %+v, want %+v", thread.ID, got, want)
		}
	}
	assertLists := func(methods []string) {
		for _, source := range []struct {
			name string
			rpc  *rpcClient
		}{{"owner", owner}, {"other-workspace", other}} {
			for _, method := range methods {
				for _, summaryOnly := range []bool{false, true} {
					t.Run(fmt.Sprintf("%s/%s/summary=%t", source.name, method, summaryOnly), func(t *testing.T) {
						var result ThreadListResult
						source.rpc.rpc(t, method, ThreadListParams{CWD: rt.RootDir, SummaryOnly: summaryOnly}, &result)
						if len(result.Threads) != len(expected) {
							t.Fatalf("listed %d sessions, want %d", len(result.Threads), len(expected))
						}
						for _, thread := range result.Threads {
							assertControl(t, thread)
						}
					})
				}
			}
		}
	}
	assertLists([]string{MethodThreadList, MethodThreadListAll})

	// A relationship released elsewhere must also override a loaded snapshot.
	if _, err := session.ChangeControl(rt.SessionDir, "session-active", "manager-a", session.ControlReleased, expected["session-active"].Revision); err != nil {
		t.Fatal(err)
	}
	expected["session-active"] = nil
	assertLists([]string{MethodThreadList, MethodThreadListAll})

	// Metadata responses feed the same sidebar cache, including for unloaded
	// sessions; archiving must not strip their management relationship either.
	for id := range expected {
		var archived ThreadArchiveResult
		other.rpc(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true}, &archived)
		assertControl(t, archived.Thread)
	}
	assertLists([]string{MethodThreadListArchived})
}
