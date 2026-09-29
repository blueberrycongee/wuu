//go:build !project_agent

package appserver

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/session"
)

// Release builds must reject creation and direct execution, leave queued work
// dormant across recovery, and keep ordinary conversation creation available.
func TestProjectAgentDisabled(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WorkspaceID = "workspace-one"
	for _, source := range []string{"project", "project-session"} {
		if _, err := session.CreateWithMetadata(rt.SessionDir, source, rt.RootDir); err != nil {
			t.Fatal(err)
		}
		if _, err := session.SetSource(rt.SessionDir, source, source); err != nil {
			t.Fatal(err)
		}
		if err := session.EnqueueInbox(rt.SessionDir, session.InboxMessage{ClientID: source, SessionID: source, Content: "pending work", Wake: true}); err != nil {
			t.Fatal(err)
		}
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	client := &rpcClient{server: srv, out: out}
	if failure := client.call(t, MethodThreadStart, ThreadStartParams{Project: &ThreadProjectParams{Name: "Disabled"}}, nil); failure == nil {
		t.Fatal("release build created a project")
	}
	if failure := client.call(t, MethodProjectSession, ProjectSessionParams{Action: "adopt", ProjectID: "project", SessionID: "project-session"}, nil); failure == nil {
		t.Fatal("release build accepted project membership changes")
	}
	for _, id := range []string{"project", "project-session"} {
		if failure := client.call(t, MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "run"}, nil); failure == nil {
			t.Fatalf("release build executed %s", id)
		}
		srv.drainSessionInbox(id)
		pending, err := session.PendingInbox(rt.SessionDir, id)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending work changed for %s: %v, %v", id, pending, err)
		}
		var result ThreadResumeResult
		client.rpc(t, MethodThreadResume, ThreadResumeParams{SessionID: id}, &result)
		if !result.Thread.ReadOnly || len(result.Thread.Turns) != 0 {
			t.Fatalf("disabled project must remain readable and dormant: %+v", result.Thread)
		}
	}
	var ordinary ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{}, &ordinary)
	if ordinary.Thread.ReadOnly {
		t.Fatal("ordinary conversations became read-only")
	}
}
