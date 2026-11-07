package appserver

import (
	"testing"

	"github.com/blueberrycongee/wuu/internal/session"
)

// Retired sessions reject creation and direct execution, leave queued work
// dormant across recovery, and keep ordinary conversation creation available.
func TestRetiredSessionsRemainReadable(t *testing.T) {
	rt := newTestRuntime(t, &fakeClient{})
	rt.WorkspaceID = "workspace-one"
	for _, source := range []string{"project", "project-session"} {
		if _, err := rt.Sessions.CreateWithMetadata(source, rt.RootDir); err != nil {
			t.Fatal(err)
		}
		if _, err := rt.Sessions.SetSource(source, source); err != nil {
			t.Fatal(err)
		}
		if err := rt.Sessions.EnqueueInbox(session.InboxMessage{ClientID: source, SessionID: source, Content: "pending work", Wake: true}); err != nil {
			t.Fatal(err)
		}
	}
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	client := &rpcClient{server: srv, out: out}
	if failure := client.call(t, MethodThreadStart, map[string]any{"project": map[string]any{"name": "Removed"}}, nil); failure == nil {
		t.Fatal("host created a project")
	}
	if failure := client.call(t, "project/session", map[string]any{"action": "adopt", "project_id": "project", "session_id": "project-session"}, nil); failure == nil {
		t.Fatal("host accepted project membership changes")
	}
	for _, id := range []string{"project", "project-session"} {
		if failure := client.call(t, MethodTurnStart, TurnStartParams{ThreadID: id, Prompt: "run"}, nil); failure == nil {
			t.Fatalf("host executed %s", id)
		}
		pending, err := rt.Sessions.PendingInbox(id)
		if err != nil || len(pending) != 1 {
			t.Fatalf("pending work changed for %s: %v, %v", id, pending, err)
		}
		var result ThreadResumeResult
		client.rpc(t, MethodThreadResume, ThreadResumeParams{SessionID: id}, &result)
		if !result.Thread.ReadOnly || len(result.Thread.Turns) != 0 {
			t.Fatalf("retired session must remain readable and dormant: %+v", result.Thread)
		}
	}
	var ordinary ThreadStartResult
	client.rpc(t, MethodThreadStart, ThreadStartParams{}, &ordinary)
	if ordinary.Thread.ReadOnly {
		t.Fatal("ordinary conversations became read-only")
	}
}
