package channels

import (
	"context"
	"testing"
)

func bindTestRoomLead(t *testing.T, ctx context.Context, service *Service, roomID string) (*AgentClient, error) {
	t.Helper()
	agents, err := service.ListNamedAgents(ctx)
	if err != nil {
		return nil, err
	}
	name := "Test lead " + roomID
	for _, agent := range agents {
		if agent.Name == name {
			return service.BindAgent(ctx, agent.ID)
		}
	}
	credential, err := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: name})
	if err != nil {
		return nil, err
	}
	now := toMillis(service.now())
	if _, err := service.db.ExecContext(ctx, `INSERT INTO room_members(room_id, member_type, member_id, joined_at) VALUES (?, 'agent', ?, ?)`, roomID, credential.Agent.ID, now); err != nil {
		return nil, err
	}
	if _, err := service.db.ExecContext(ctx, `INSERT INTO room_cursors(room_id, member_type, member_id, last_read_seq) VALUES (?, 'agent', ?, 0)`, roomID, credential.Agent.ID); err != nil {
		return nil, err
	}
	return service.BindAgent(ctx, credential.Agent.ID)
}

func TestRoomMessagesWakeVisibleRecipientsAndAcknowledgeInTheirConversation(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha, beta)
	alphaClient, _ := service.BindAgent(ctx, alpha.Agent.ID)
	betaClient, _ := service.BindAgent(ctx, beta.Agent.ID)
	for _, entry := range []struct {
		client *AgentClient
		ref    string
	}{{alphaClient, "alpha-room"}, {betaClient, "beta-room"}} {
		if _, err := entry.client.BindCollaborationSession(ctx, CollaborationSessionBindParams{RoomID: room.ID, SessionRef: entry.ref, Purpose: CollaborationSessionConversation}); err != nil {
			t.Fatal(err)
		}
	}
	alphaSession, _ := service.BindAgentSession(ctx, alpha.Agent.ID, "alpha-room")
	betaSession, _ := service.BindAgentSession(ctx, beta.Agent.ID, "beta-room")
	sent, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@Alpha inspect the failure"})
	if err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 1 || wakes[0] != alpha.Agent.ID {
		t.Fatalf("addressed wake = %v", wakes)
	}
	messages, err := alphaSession.ReceiveCollaboration(ctx, 50)
	if err != nil || len(messages) != 1 || messages[0].SourceMessageID != sent.Message.ID || messages[0].FromType != MemberHuman || messages[0].FromID != "human-1" {
		t.Fatalf("human envelope = %#v, %v", messages, err)
	}
	if err := alphaSession.AcknowledgeCollaboration(ctx, []string{messages[0].ID}); err != nil {
		t.Fatal(err)
	}
	check, err := alphaSession.Check(ctx)
	if err != nil || len(check.Items) != 0 || len(check.Collaboration) != 0 {
		t.Fatalf("ack left duplicate input: %#v, %v", check, err)
	}
	if messages, err := betaSession.ReceiveCollaboration(ctx, 50); err != nil || len(messages) != 0 {
		t.Fatalf("unaddressed beta received active input: %#v, %v", messages, err)
	}
	for _, recipient := range []string{"Alpha", "Beta"} {
		if _, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@" + recipient + " compare hypotheses"}); err != nil {
			t.Fatal(err)
		}
		if wakes := sink.take(); len(wakes) != 1 {
			t.Fatalf("addressed wake = %v", wakes)
		}
		target := alphaSession
		if recipient == "Beta" {
			target = betaSession
		}
		received, err := target.ReceiveCollaboration(ctx, 50)
		if err != nil || len(received) != 1 {
			t.Fatalf("receive = %+v %v", received, err)
		}
		if err := target.AcknowledgeCollaboration(ctx, []string{received[0].ID}); err != nil {
			t.Fatal(err)
		}
	}
	roomMessages, _ := service.ListMessages(ctx, room.ID, 0, 50)
	plain, err := alphaSession.Send(ctx, AgentSendParams{RoomID: room.ID, Body: "I found a possible cause", BasisSeq: roomMessages[len(roomMessages)-1].Seq})
	if err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 0 {
		t.Fatalf("passive agent message woke models: %v", wakes)
	}
	checked, err := betaSession.Check(ctx)
	if err != nil || len(checked.Items) == 0 {
		t.Fatalf("passive history unavailable: %#v, %v", checked, err)
	}
	if again, err := service.FinishWakeAttempt(ctx, beta.Agent.ID); err != nil || again {
		t.Fatalf("passive message scheduled another turn: %v, %v", again, err)
	}
	if _, err := betaSession.Send(ctx, AgentSendParams{RoomID: room.ID, Body: "Please explain the cause", ReplyTo: plain.Message.ID, BasisSeq: plain.Message.Seq}); err != nil {
		t.Fatal(err)
	}
	if wakes := sink.take(); len(wakes) != 1 || wakes[0] != alpha.Agent.ID {
		t.Fatalf("agent reply wake = %v", wakes)
	}
}

func TestLegacyUnreadInboxRecoveryIsScopedAndIdempotent(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha, beta)
	direct, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: "@Alpha inspect this"})
	if err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.ExecContext(ctx, `DELETE FROM collaboration_messages WHERE source_message_id = ?`, direct.Message.ID); err != nil {
		t.Fatal(err)
	}
	if _, err := service.db.Exec(`DELETE FROM channel_metadata WHERE key='room_round_robin_version'`); err != nil {
		t.Fatal(err)
	}
	if err := service.retireRoomRuntimes(ctx); err != nil {
		t.Fatal(err)
	}
	if err := service.retireRoomRuntimes(ctx); err != nil {
		t.Fatal(err)
	}
	var alphaCount, betaCount int
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collaboration_messages WHERE source_message_id = ? AND to_agent_id = ?`, direct.Message.ID, alpha.Agent.ID).Scan(&alphaCount); err != nil {
		t.Fatal(err)
	}
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM collaboration_messages WHERE source_message_id = ? AND to_agent_id = ?`, direct.Message.ID, beta.Agent.ID).Scan(&betaCount); err != nil {
		t.Fatal(err)
	}
	if alphaCount != 1 || betaCount != 0 {
		t.Fatalf("restored deliveries alpha=%d beta=%d", alphaCount, betaCount)
	}
}
