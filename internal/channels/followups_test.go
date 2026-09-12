package channels

import (
	"context"
	"errors"
	"testing"
	"time"
)

func advanceFollowupClock(s *Service, now time.Time) {
	s.mu.Lock()
	s.now = func() time.Time { return now }
	s.mu.Unlock()
}

func TestFollowupDurableTargetedDeliveryAndWaiting(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	s, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	a := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "long-term")
	b := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "sibling")
	p := FollowupSetParams{RequestID: "review", After: "1h", Note: "Review the new evidence"}
	f, err := a.SetFollowup(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	retry, err := a.SetFollowup(ctx, p)
	if err != nil || retry.ID != f.ID || !retry.NextAt.Equal(f.NextAt) {
		t.Fatalf("retry = %+v, %v", retry, err)
	}
	p.Note = "Different intent"
	if _, err = a.SetFollowup(ctx, p); !errors.Is(err, ErrConflict) {
		t.Fatalf("changed retry = %v", err)
	}
	settled, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: a.SessionRef(), State: CollaborationSessionCompleted, TurnID: "initial", Result: "Waiting for new evidence"})
	if err != nil || settled.State != CollaborationSessionWaiting {
		t.Fatalf("settlement = %+v, %v", settled, err)
	}
	advanceFollowupClock(s, f.NextAt.Add(time.Second))
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	sibling, err := b.ReceiveCollaboration(ctx, 10)
	if err != nil || len(sibling) != 0 {
		t.Fatalf("sibling = %+v, %v", sibling, err)
	}
	if err = s.Close(); err != nil {
		t.Fatal(err)
	}
	s, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer s.Close()
	a, err = s.BindAgentSession(ctx, owner.Agent.ID, "long-term")
	if err != nil {
		t.Fatal(err)
	}
	messages, err := a.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 1 || messages[0].CorrelationID != f.ID {
		t.Fatalf("replayed delivery = %+v, %v", messages, err)
	}
	if err = a.AcknowledgeCollaboration(ctx, []string{messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	messages, err = a.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 0 {
		t.Fatalf("duplicate = %+v, %v", messages, err)
	}
}

func TestFollowupCancellationAndIndependentOwnership(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	a := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "owner-session")
	local, err := a.SetFollowup(ctx, FollowupSetParams{RequestID: "local", After: "1h", Note: "Continue here"})
	if err != nil {
		t.Fatal(err)
	}
	persistent, err := a.SetFollowup(ctx, FollowupSetParams{RequestID: "weekly", Scope: "agent", After: "1h", Note: "Keep monitoring"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err = s.CancelCollaborationSessions(ctx, a.SessionRef()); err != nil {
		t.Fatal(err)
	}
	advanceFollowupClock(s, persistent.NextAt.Add(time.Second))
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	entries, err := s.ListRoomFollowups(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	states := map[string]string{}
	for _, f := range entries {
		states[f.ID] = f.State
	}
	if states[local.ID] != "cancelled" || states[persistent.ID] != "done" {
		t.Fatalf("states = %v", states)
	}
}

func TestFollowupPauseInvalidatesPendingAndChecksRevision(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	a := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "followup-owner")
	f, err := a.SetFollowup(ctx, FollowupSetParams{RequestID: "daily", Cron: "0 9 * * *", Timezone: "Asia/Shanghai", Note: "Check progress"})
	if err != nil {
		t.Fatal(err)
	}
	advanceFollowupClock(s, f.NextAt.Add(time.Second))
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = a.ControlFollowup(ctx, f.ID, "paused", f.Revision); !errors.Is(err, ErrConflict) {
		t.Fatalf("stale update = %v", err)
	}
	all, err := a.ListFollowups(ctx, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	paused, err := a.ControlFollowup(ctx, f.ID, "paused", all[0].Revision)
	if err != nil {
		t.Fatal(err)
	}
	messages, err := a.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 0 {
		t.Fatalf("paused delivery = %+v, %v", messages, err)
	}
	if _, err = s.ControlRoomFollowup(ctx, "wrong-room", f.ID, "cancelled", paused.Revision); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("room control = %v", err)
	}
}

func TestFollowupEventAlreadyFinishedAndWeeklyTimezone(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	a := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "waiter")
	b := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "finished-peer")
	if _, err := s.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: b.SessionRef(), State: CollaborationSessionCompleted, TurnID: "result", Result: "Evidence is ready"}); err != nil {
		t.Fatal(err)
	}
	if _, err := a.SetFollowup(ctx, FollowupSetParams{RequestID: "result-ready", WhenSession: b.SessionRef(), Note: "Read evidence"}); err != nil {
		t.Fatal(err)
	}
	if _, err := s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	messages, err := a.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("event = %+v, %v", messages, err)
	}
	// The local Friday time remains stable across the daylight-saving transition.
	now := time.Date(2026, 3, 6, 15, 0, 0, 0, time.UTC)
	next, err := nextFollowupTime("0 9 * * 5", "America/New_York", now)
	want := time.Date(2026, 3, 13, 13, 0, 0, 0, time.UTC)
	if err != nil || !next.Equal(want) {
		t.Fatalf("weekly = %v, %v; want %v", next, err, want)
	}
}

func TestRecurringFollowupCoalescesBacklogAndDirectMessageSkipsModel(t *testing.T) {
	ctx := context.Background()
	s := openTestService(t, nil)
	owner := createTestAgent(t, s, "Owner")
	room := createTestRoom(t, s, owner)
	a := flexibleTestSession(t, s, owner.Agent.ID, room.ID, "monitor")
	f, err := a.SetFollowup(ctx, FollowupSetParams{RequestID: "monitor", Cron: "* * * * *", Timezone: "UTC", Note: "Check changes"})
	if err != nil {
		t.Fatal(err)
	}
	advanceFollowupClock(s, f.NextAt.Add(time.Second))
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	advanceFollowupClock(s, f.NextAt.Add(time.Hour))
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	messages, err := a.ReceiveCollaboration(ctx, 10)
	if err != nil || len(messages) != 1 {
		t.Fatalf("backlog = %+v, %v", messages, err)
	}
	if err = a.AcknowledgeCollaboration(ctx, []string{messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	due := s.now().Add(time.Hour)
	p := FollowupSetParams{RequestID: "remind-human", Scope: "agent", Mode: "message", FireAt: &due, Note: "Time for the weekly review"}
	direct, err := a.SetFollowup(ctx, p)
	if err != nil {
		t.Fatal(err)
	}
	advanceFollowupClock(s, direct.NextAt.Add(time.Second))
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	if _, err = s.FireDueFollowups(ctx); err != nil {
		t.Fatal(err)
	}
	retry, err := a.SetFollowup(ctx, p)
	if err != nil || retry.ID != direct.ID {
		t.Fatalf("retry after due = %+v, %v", retry, err)
	}
	timeline, err := s.ListMessages(ctx, room.ID, 0, 100)
	if err != nil {
		t.Fatal(err)
	}
	count := 0
	for _, m := range timeline {
		if m.Body == p.Note {
			count++
			if m.AuthorID != owner.Agent.ID {
				t.Fatal("reminder lost sender")
			}
		}
	}
	if count != 1 {
		t.Fatalf("direct reminders = %d", count)
	}
	deliveries, err := s.PendingCollaborationDispatches(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	for _, delivery := range deliveries {
		if delivery.CorrelationID == direct.ID {
			t.Fatal("direct message invoked a model")
		}
	}
}
