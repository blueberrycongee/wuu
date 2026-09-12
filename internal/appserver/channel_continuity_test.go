package appserver

import (
	"context"
	"github.com/blueberrycongee/wuu/internal/channels"
	"strings"
	"testing"
)

func TestChannelContinuityUserManagement(t *testing.T) {
	ctx := context.Background()
	f := newCollaborationRPCFixture(t)
	c, err := f.server.channelService.BindAgent(ctx, f.identity.ID)
	if err != nil {
		t.Fatal(err)
	}
	if _, err = c.BindCollaborationSession(ctx, channels.CollaborationSessionBindParams{SessionRef: "continuity", RoomID: f.room.ID}); err != nil {
		t.Fatal(err)
	}
	c, err = f.server.channelService.BindAgentSession(ctx, f.identity.ID, "continuity")
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.SetFollowup(ctx, channels.FollowupSetParams{RequestID: "weekly", Scope: "agent", Cron: "0 9 * * 5", Timezone: "Asia/Shanghai", Note: "Weekly review"})
	if err != nil {
		t.Fatal(err)
	}
	var page channels.FollowupPage
	f.rpc(t, MethodChannelContinuity, ChannelContinuityParams{Action: "list", RoomID: f.room.ID}, &page)
	if len(page.Arrangements) != 1 || page.Arrangements[0].ID != plan.ID {
		t.Fatalf("plans = %+v", page)
	}
	f.rpc(t, MethodChannelContinuity, ChannelContinuityParams{Action: "control", RoomID: f.room.ID, ID: plan.ID, State: "cancelled", Revision: plan.Revision}, &page)
	if len(page.Arrangements) != 1 || page.Arrangements[0].State != "cancelled" {
		t.Fatalf("cancel = %+v", page)
	}
	var memory channels.NotebookResult
	p := ChannelContinuityParams{Action: "memory", RoomID: f.room.ID, OwnerID: f.identity.ID, Memory: channels.NotebookParams{Action: "write", Name: "preferences.md", Revision: "missing", Content: "Weekly review on Friday"}}
	f.rpc(t, MethodChannelContinuity, p, &memory)
	p.Memory.Action = "delete"
	p.Memory.Revision = memory.Entries[0].Revision
	f.rpc(t, MethodChannelContinuity, p, &memory)
	p.Memory.Action = "read"
	p.Memory.Name = "MEMORY.md"
	f.rpc(t, MethodChannelContinuity, p, &memory)
	if len(memory.Entries) != 1 || strings.Contains(memory.Entries[0].Content, "preferences.md") {
		t.Fatalf("deleted memory index = %+v", memory)
	}
}

func TestContinuationEventReentersTheExactSession(t *testing.T) {
	ctx := context.Background()
	f := newCollaborationRPCFixture(t)
	a, callA := f.create(t, "Own the ongoing investigation")
	b, callB := f.create(t, "Inspect a separate source")
	c, err := f.server.channelService.BindAgentSession(ctx, f.identity.ID, a.SessionRef)
	if err != nil {
		t.Fatal(err)
	}
	plan, err := c.SetFollowup(ctx, channels.FollowupSetParams{RequestID: "wait-for-source", WhenSession: b.SessionRef, Note: "Continue with the separate source result"})
	if err != nil {
		t.Fatal(err)
	}
	close(callA.release)
	f.waitForCompletion(t)
	binding, err := f.server.channelService.LookupCollaborationSession(ctx, a.SessionRef)
	if err != nil || binding.State != channels.CollaborationSessionWaiting {
		t.Fatalf("waiting = %+v, %v", binding, err)
	}
	close(callB.release)
	f.waitForCompletion(t)
	if _, err = f.server.channelService.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	if err = f.server.deliverNamedAgentWake(ctx, f.identity.ID); err != nil {
		t.Fatal(err)
	}
	continued := f.nextCall(t)
	text := collaborationRequestText(continued.request)
	if !strings.Contains(text, plan.Note) || !strings.Contains(text, a.Objective) {
		t.Fatalf("continuation lost context: %s", text)
	}
	close(continued.release)
	f.waitForCompletion(t)
	var inspected ChannelSessionReadResult
	f.rpc(t, MethodChannelSessionRead, ChannelSessionRefParams{SessionRef: a.SessionRef}, &inspected)
	if len(inspected.Thread.Turns) != 2 {
		t.Fatalf("turns = %d", len(inspected.Thread.Turns))
	}
}
