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
	// A live project names its sessions' manager; a fence left by an unknown
	// manager no longer manages anything.
	const project = "project-a"
	if _, err := session.CreateWithMetadata(rt.SessionDir, project, rt.RootDir); err != nil {
		t.Fatal(err)
	}
	if _, err := session.SetSource(rt.SessionDir, project, projectSource); err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdateTitle(rt.SessionDir, project, "Catalog"); err != nil {
		t.Fatal(err)
	}
	expected[project] = nil
	for _, state := range []string{session.ControlActive, session.ControlPaused, session.ControlTakenOver, session.ControlReleased, "ordinary", "unknown-manager"} {
		id := "session-" + state
		if _, err := session.CreateWithMetadata(rt.SessionDir, id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
		expected[id] = nil
		switch state {
		case "ordinary":
		case "unknown-manager":
			if _, err := session.ChangeControl(rt.SessionDir, id, "manager-gone", session.ControlActive, 0); err != nil {
				t.Fatal(err)
			}
		default:
			control, err := session.ChangeControl(rt.SessionDir, id, project, state, 0)
			if err != nil {
				t.Fatal(err)
			}
			if state != session.ControlReleased {
				expected[id] = &ThreadSessionControl{ManagerID: control.ManagerID, ManagerName: "Catalog", State: control.State, Revision: control.Revision}
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
	if _, err := session.ChangeControl(rt.SessionDir, "session-active", project, session.ControlReleased, expected["session-active"].Revision); err != nil {
		t.Fatal(err)
	}
	expected["session-active"] = nil
	assertLists([]string{MethodThreadList, MethodThreadListAll})

	// Metadata responses feed the same sidebar cache, including for unloaded
	// sessions; archiving must not strip their management relationship either.
	for id := range expected {
		if id == project {
			continue
		}
		var archived ThreadArchiveResult
		other.rpc(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: id, Archived: true}, &archived)
		assertControl(t, archived.Thread)
	}
	delete(expected, project)
	assertLists([]string{MethodThreadListArchived})
}

func TestProjectGroupingDistinguishesArchivedAndMissingCoordinators(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	client := &rpcClient{server: srv, out: out}
	for _, id := range []string{"project", "member", "orphan"} {
		if _, err := session.CreateWithMetadata(rt.SessionDir, id, rt.RootDir); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := session.SetSource(rt.SessionDir, "project", projectSource); err != nil {
		t.Fatal(err)
	}
	for member, parent := range map[string]string{"member": "project", "orphan": "missing"} {
		if _, err := session.SetProjectMembership(rt.SessionDir, member, projectSessionSource, parent, ""); err != nil {
			t.Fatal(err)
		}
	}
	assertGrouping := func(memberExists bool) {
		t.Helper()
		for _, method := range []string{MethodThreadList, MethodThreadListAll} {
			var result struct {
				Threads []struct {
					ID            string `json:"id"`
					ProjectID     string `json:"project_id"`
					ProjectExists bool   `json:"project_exists"`
				} `json:"threads"`
			}
			client.rpc(t, method, ThreadListParams{SummaryOnly: true}, &result)
			found := 0
			for _, thread := range result.Threads {
				if thread.ID != "member" && thread.ID != "orphan" {
					continue
				}
				found++
				want := thread.ID == "member" && memberExists
				if thread.ProjectID == "" || thread.ProjectExists != want {
					t.Fatalf("%s grouping = %+v, exists want %t", method, thread, want)
				}
			}
			if found != 2 {
				t.Fatalf("missing surviving sessions: %+v", result)
			}
		}
	}
	assertGrouping(true)
	client.rpc(t, MethodThreadArchive, ThreadArchiveParams{ThreadID: "project", Archived: true}, nil)
	if err := srv.notifyThreadStarted(Thread{ID: "member", Source: projectSessionSource, ProjectID: "project"}); err != nil {
		t.Fatal(err)
	}
	rows := parseOutput(t, out.String())
	started := remarshal[struct {
		Thread struct {
			ProjectExists bool `json:"project_exists"`
		} `json:"thread"`
	}](t, rows[len(rows)-1]["params"])
	if !started.Thread.ProjectExists {
		t.Fatal("member notification lost archived coordinator grouping")
	}
	assertGrouping(true)
	// Loading a member and then deleting its coordinator must not retain stale grouping.
	var resumed struct {
		Thread struct {
			ProjectExists bool `json:"project_exists"`
		} `json:"thread"`
	}
	client.rpc(t, MethodThreadResume, ThreadResumeParams{SessionID: "member"}, &resumed)
	if !resumed.Thread.ProjectExists {
		t.Fatal("resume lost archived coordinator")
	}
	client.rpc(t, MethodThreadDelete, ThreadDeleteParams{ThreadID: "project"}, nil)
	updatedMember := false
	for _, row := range parseOutput(t, out.String()) {
		if row["method"] != NotificationThreadUpdated {
			continue
		}
		params := remarshal[struct {
			Thread struct {
				ID            string `json:"id"`
				ProjectExists *bool  `json:"project_exists"`
			} `json:"thread"`
		}](t, row["params"])
		if params.Thread.ID == "member" && params.Thread.ProjectExists != nil && !*params.Thread.ProjectExists {
			updatedMember = true
		}
	}
	if !updatedMember {
		t.Fatal("deletion did not refresh surviving member grouping")
	}
	assertGrouping(false)
	// A fresh server must recover pre-existing orphans without rewriting membership.
	freshOut := &lockedBuffer{}
	fresh := New(rt, freshOut)
	t.Cleanup(fresh.Close)
	client = &rpcClient{server: fresh, out: freshOut}
	assertGrouping(false)
}
