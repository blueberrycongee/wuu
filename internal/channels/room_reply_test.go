package channels

import (
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"strings"
	"testing"
)

func TestConversationReplyPreservesFullAnswerAcrossRestartAndReplay(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	sink := &recordingWakeSink{}
	service, err := Open(dir, sink)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() { _ = service.Close() })
	owner, peer := createTestAgent(t, service, "Owner"), createTestAgent(t, service, "Peer")
	room := createTestRoom(t, service, owner, peer)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "conversation")
	if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionRunning, TurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	body := "    Preserve the indented example.\n\n```text\n" + strings.Repeat("完整答案\n", MaxMessageRunes) + "```\n"
	params := CollaborationSessionSettleParams{SessionRef: session.SessionRef(), State: CollaborationSessionIdle, TurnID: "turn-1", Result: "Private summary", PublicReply: body}
	sink.take()
	settled, err := service.SettleCollaborationSession(ctx, params)
	if err != nil || settled.State != CollaborationSessionIdle {
		t.Fatalf("settlement = %#v, %v", settled, err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, sink)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.AdmitCollaborationSession(ctx, session.SessionRef()); err != nil {
		t.Fatal(err)
	}
	session, err = service.BindAgentSession(ctx, owner.Agent.ID, session.SessionRef())
	if err != nil {
		t.Fatal(err)
	}
	if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionRunning, TurnID: "turn-2"}); err != nil {
		t.Fatal(err)
	}
	replay, err := service.SettleCollaborationSession(ctx, params)
	if err != nil || replay.State != CollaborationSessionRunning || replay.TurnID != "turn-2" {
		t.Fatalf("old reply replay changed active turn = %#v, %v", replay, err)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || len(messages) != 1 {
		t.Fatalf("public replies = %#v, %v", messages, err)
	}
	message := messages[0]
	if message.ID != ConversationReplyID(session.SessionRef(), params.TurnID) || message.Kind != MessageText || message.AuthorType != MemberAgent || message.AuthorID != owner.Agent.ID || message.Body != body {
		t.Fatalf("public answer lost its stable address, author or full body: %#v", message)
	}
	inbox, err := service.ListInbox(ctx, peer.Agent.ID, false)
	if err != nil || len(inbox) != 1 || inbox[0].MessageID != message.ID || inbox[0].Kind != InboxThreadUpdate {
		t.Fatalf("peer passive update = %#v, %v", inbox, err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("ordinary public reply started extra model work: %#v", wakes)
	}
	changed := params
	changed.PublicReply = "A conflicting answer for the same turn"
	if _, err := service.SettleCollaborationSession(ctx, changed); !errors.Is(err, ErrConflict) {
		t.Fatalf("conflicting answer replay = %v", err)
	}
}

func TestConversationReplyAndMentionDeliveryCommitWithSettlement(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	owner, peer := createTestAgent(t, service, "Owner"), createTestAgent(t, service, "Peer")
	room := createTestRoom(t, service, owner, peer)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "conversation")
	if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionRunning, TurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	// Fail after the message and its inbox entries have been inserted, so the
	// test exercises the transaction boundary rather than input validation.
	if _, err := service.db.ExecContext(ctx, `CREATE TRIGGER fail_settlement BEFORE INSERT ON collaboration_session_settlements BEGIN SELECT RAISE(ABORT, 'storage failure'); END`); err != nil {
		t.Fatal(err)
	}
	params := CollaborationSessionSettleParams{SessionRef: session.SessionRef(), State: CollaborationSessionIdle, TurnID: "turn-1", PublicReply: "The answer is ready. @Peer please check the boundary condition."}
	sink.take()
	if _, err := service.SettleCollaborationSession(ctx, params); err == nil {
		t.Fatal("settlement unexpectedly survived storage failure")
	}
	current, err := service.LookupCollaborationSession(ctx, session.SessionRef())
	if err != nil || current.State != CollaborationSessionRunning {
		t.Fatalf("failed transaction released the turn = %#v, %v", current, err)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || len(messages) != 0 {
		t.Fatalf("unsettled answer leaked into room = %#v, %v", messages, err)
	}
	inbox, err := service.ListInbox(ctx, peer.Agent.ID, false)
	if err != nil || len(inbox) != 0 {
		t.Fatalf("unsettled answer reached peer inbox = %#v, %v", inbox, err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("peer was woken before commit: %#v", wakes)
	}
	if _, err := service.db.ExecContext(ctx, `DROP TRIGGER fail_settlement`); err != nil {
		t.Fatal(err)
	}
	for range 2 {
		if _, err := service.SettleCollaborationSession(ctx, params); err != nil {
			t.Fatal(err)
		}
	}
	if wakes := sink.take(); len(wakes) != 1 || wakes[0] != peer.Agent.ID {
		t.Fatalf("committed mention wake = %#v", wakes)
	}
	peerClient := flexibleTestSession(t, service, peer.Agent.ID, room.ID, "peer-conversation")
	deliveries, err := peerClient.ReceiveCollaboration(ctx, 100)
	if err != nil || len(deliveries) != 1 || deliveries[0].Body != params.PublicReply || deliveries[0].SourceMessageID != ConversationReplyID(session.SessionRef(), params.TurnID) {
		t.Fatalf("committed mention delivery = %#v, %v", deliveries, err)
	}
	inbox, err = service.ListInbox(ctx, peer.Agent.ID, false)
	if err != nil || len(inbox) != 1 || inbox[0].Kind != InboxMention {
		t.Fatalf("committed mention inbox = %#v, %v", inbox, err)
	}
}

func TestConversationReplyRejectsPrivateSessionScopes(t *testing.T) {
	for _, scope := range []string{"work", "verification", "child", "roomless"} {
		t.Run(scope, func(t *testing.T) {
			ctx := context.Background()
			service := openTestService(t, nil)
			owner := createTestAgent(t, service, "Owner")
			room := createTestRoom(t, service, owner)
			parent := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "parent")
			client, err := service.BindAgent(ctx, owner.Agent.ID)
			if err != nil {
				t.Fatal(err)
			}
			binding := CollaborationSessionBindParams{SessionRef: scope, RoomID: room.ID, Purpose: CollaborationSessionConversation}
			switch scope {
			case "work":
				binding.Purpose = CollaborationSessionWork
			case "verification":
				binding.Purpose = CollaborationSessionVerification
			case "child":
				binding.ParentSessionRef = parent.SessionRef()
			case "roomless":
				binding.RoomID = ""
			}
			if _, err := client.BindCollaborationSession(ctx, binding); err != nil {
				t.Fatal(err)
			}
			if _, err := client.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: binding.SessionRef, State: CollaborationSessionRunning, TurnID: "turn-1"}); err != nil {
				t.Fatal(err)
			}
			if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: binding.SessionRef, State: CollaborationSessionIdle, TurnID: "turn-1", PublicReply: "Private findings"}); !errors.Is(err, ErrUnauthorized) {
				t.Fatalf("private session publication = %v", err)
			}
			messages, err := service.ListMessages(ctx, room.ID, 0, 100)
			if err != nil || len(messages) != 0 {
				t.Fatalf("private output leaked into room = %#v, %v", messages, err)
			}
		})
	}
}

func TestConversationReplySkipsDepartedMemberAndReleasesCapacity(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	service.agentRunLimit = 1
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "conversation")
	queued := flexibleTestSession(t, service, owner.Agent.ID, "", "another-conversation")
	if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: CollaborationSessionRunning, TurnID: "turn-1"}); err != nil {
		t.Fatal(err)
	}
	admission, err := service.AdmitCollaborationSession(ctx, queued.SessionRef())
	if err != nil || admission.State != CollaborationSessionQueued {
		t.Fatalf("concurrent session admission = %#v, %v", admission, err)
	}
	members := []RoomMember{}
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatal(err)
	}
	sink.take()
	settled, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: session.SessionRef(), State: CollaborationSessionIdle, TurnID: "turn-1", PublicReply: "A late answer"})
	if err != nil || settled.State != CollaborationSessionIdle {
		t.Fatalf("departed member settlement = %#v, %v", settled, err)
	}
	admission, err = service.AdmitCollaborationSession(ctx, queued.SessionRef())
	if err != nil || admission.State != CollaborationSessionStarting {
		t.Fatalf("departed member retained capacity = %#v, %v", admission, err)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || len(messages) != 1 || messages[0].Kind != MessageSystem {
		t.Fatalf("departed member published an answer = %#v, %v", messages, err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("departed member reply caused wakes: %#v", wakes)
	}
}

func TestConversationReplyRejectsLateCancelledTurn(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "conversation")
	for _, state := range []CollaborationSessionState{CollaborationSessionRunning, CollaborationSessionCancelled} {
		if _, err := session.UpdateCollaborationSessionState(ctx, CollaborationSessionStateParams{SessionRef: session.SessionRef(), State: state, TurnID: "turn-1"}); err != nil {
			t.Fatal(err)
		}
	}
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: session.SessionRef(), State: CollaborationSessionIdle, TurnID: "turn-1", PublicReply: "The cancelled answer arrived late"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("cancelled publication = %v", err)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || len(messages) != 0 {
		t.Fatalf("cancelled answer leaked into room = %#v, %v", messages, err)
	}
}

func TestSessionSettlementReplaysFingerprintFromBeforePublicReplies(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	session := flexibleTestSession(t, service, owner.Agent.ID, room.ID, "conversation")
	// This is the persisted input shape used before public reply projection.
	oldParams := struct {
		SessionRef    string
		State         CollaborationSessionState
		Result        string
		TurnID        string
		FailureReason string
	}{SessionRef: session.SessionRef(), State: CollaborationSessionIdle, Result: "Existing answer", TurnID: "old-turn"}
	encoded, err := json.Marshal(oldParams)
	if err != nil {
		t.Fatal(err)
	}
	digest := sha256.Sum256(encoded)
	if _, err := service.db.ExecContext(ctx, `INSERT INTO collaboration_session_settlements(session_ref, turn_id, result_hash, created_at) VALUES (?, ?, ?, ?)`, session.SessionRef(), oldParams.TurnID, hex.EncodeToString(digest[:]), toMillis(service.now())); err != nil {
		t.Fatal(err)
	}
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: oldParams.SessionRef, State: oldParams.State, Result: oldParams.Result, TurnID: oldParams.TurnID}); err != nil {
		t.Fatalf("old persisted settlement no longer replays = %v", err)
	}
}
