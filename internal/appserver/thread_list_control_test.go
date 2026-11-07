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
	const manager = "plugin:catalog"
	for _, state := range []string{session.ControlActive, session.ControlPaused, session.ControlTakenOver, session.ControlReleased, "ordinary", "unknown-manager"} {
		id := "session-" + state
		if _, err := rt.Sessions.CreateWithMetadata(id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
		expected[id] = nil
		switch state {
		case "ordinary":
		case "unknown-manager":
			if _, err := rt.Sessions.ChangeControl(id, "manager-gone", session.ControlActive, 0); err != nil {
				t.Fatal(err)
			}
		default:
			control, err := rt.Sessions.ChangeControl(id, manager, state, 0)
			if err != nil {
				t.Fatal(err)
			}
			if state != session.ControlReleased {
				expected[id] = &ThreadSessionControl{ManagerID: control.ManagerID, ManagerName: manager, State: control.State, Revision: control.Revision}
			}
		}
		loaded, err := owner.server.loadPersistedThreadState(testRuntimeState(t, owner.server.rt), id, time.Now().UTC())
		if err != nil {
			t.Fatal(err)
		}
		owner.server.threads[id] = loaded
	}

	// Switching projects selects a different app-server. It shares the store,
	// but has never loaded the original project's managed conversations.
	otherRuntime := newTestRuntime(t, &fakeClient{})
	otherRuntime.SessionDir = rt.SessionDir
	otherRuntime.Documents, otherRuntime.Sessions = rt.Documents, rt.Sessions
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
		} else if got == nil || *got != *want {
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
	if _, err := rt.Sessions.ChangeControl("session-active", manager, session.ControlReleased, expected["session-active"].Revision); err != nil {
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
