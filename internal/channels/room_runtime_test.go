package channels

import (
	"context"
	"encoding/json"
	"errors"
	"path/filepath"
	"strings"
	"testing"
)

func TestNewRoomsHaveOnlyVisibleMemberRuntimes(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	if room.RuntimeID != "" || room.AgentID != "" {
		t.Fatalf("new room has hidden runtime: %#v", room)
	}
	if _, err := service.EnsureBootstrap(ctx, "human-1"); err != nil {
		t.Fatal(err)
	}
	var count int
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_runtimes`).Scan(&count); err != nil || count != 0 {
		t.Fatalf("room runtimes = %d, err = %v", count, err)
	}
	runtimes, err := service.ListAgentRuntimes(ctx)
	if err != nil || len(runtimes) != 1 || runtimes[0].ID != owner.Agent.ID {
		t.Fatalf("runtimes = %#v, err = %v", runtimes, err)
	}
	raw, err := json.Marshal(room)
	if err != nil {
		t.Fatal(err)
	}
	if strings.Contains(string(raw), "runtime_id") {
		t.Fatalf("serialized hidden runtime: %s", raw)
	}
}

func TestMemberTaskProjectionUsesItsVisibleInitiator(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	lead, err := bindTestRoomLead(t, ctx, service, room.ID)
	if err != nil {
		t.Fatal(err)
	}
	task, err := lead.CreateTask(ctx, TaskCreateParams{RoomID: room.ID, Title: "Check", OwnerID: owner.Agent.ID})
	if err != nil {
		t.Fatal(err)
	}
	if task.AuthorType != MemberAgent || task.AuthorID != lead.AgentID() || task.Work == nil || task.Work.LeadNamedAgentID != lead.AgentID() {
		t.Fatalf("task lost visible initiator: %#v", task)
	}
}

func TestOpenRetiresLegacyRoomAgentAndPreservesPendingAndFutureResults(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	owner := createTestAgent(t, service, "Owner")
	room := createTestRoom(t, service, owner)
	const legacyID, legacySession, token = "legacy-runtime", "legacy-parent", "legacy-token"
	now := toMillis(service.now())
	for _, operation := range []struct {
		query string
		args  []any
	}{
		{`INSERT INTO collaboration_principals(id, kind) VALUES (?, 'room_runtime')`, []any{legacyID}},
		{`INSERT INTO named_agents(id, name, kind, room_id, memory_dir, avatar_key, avatar_image, engine_override, token_hash, autostart, created_at) VALUES (?, 'Legacy', 'room', ?, ?, '', '', 'wuu', ?, 1, ?)`, []any{legacyID, room.ID, filepath.Join(dir, "legacy", "memory"), tokenHash(token), now}},
		{`INSERT INTO room_members(room_id, member_type, member_id, joined_at) VALUES (?, 'agent', ?, ?)`, []any{room.ID, legacyID, now}},
		{`INSERT INTO room_cursors(room_id, member_type, member_id, last_read_seq) VALUES (?, 'agent', ?, 0)`, []any{room.ID, legacyID}},
		{`INSERT INTO agent_wake_state(agent_id, outstanding, pending, updated_at) VALUES (?, 1, 1, ?)`, []any{legacyID, now}},
		{`INSERT INTO collaboration_session_bindings(session_ref, principal_id, room_id, purpose, state, created_at, updated_at) VALUES (?, ?, ?, 'coordination', 'running', ?, ?)`, []any{legacySession, legacyID, room.ID, now, now}},
		{`INSERT INTO collaboration_session_bindings(session_ref, principal_id, named_agent_id, room_id, purpose, state, parent_session_ref, turn_id, created_at, updated_at) VALUES ('child', ?, ?, ?, 'work', 'running', ?, 'child-turn', ?, ?)`, []any{owner.Agent.ID, owner.Agent.ID, room.ID, legacySession, now, now}},
	} {
		if _, err := service.db.ExecContext(ctx, operation.query, operation.args...); err != nil {
			t.Fatal(err)
		}
	}
	tx, err := service.db.BeginTx(ctx, nil)
	if err != nil {
		t.Fatal(err)
	}
	original, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{RoomID: room.ID, ToAgentID: legacyID, TargetSessionRef: legacySession, Kind: CollaborationCompletion, Body: "Durable completed result", CreatedAt: service.now()})
	if err != nil {
		t.Fatal(err)
	}
	if err := tx.Commit(); err != nil {
		t.Fatal(err)
	}
	if err := service.Close(); err != nil {
		t.Fatal(err)
	}
	service, err = Open(dir, nil)
	if err != nil {
		t.Fatal(err)
	}
	defer service.Close()
	legacy, err := service.GetRoomRuntime(ctx, legacyID)
	if err != nil || legacy.Autostart {
		t.Fatalf("retired metadata = %#v, err = %v", legacy, err)
	}
	if _, err := service.AuthenticatePrincipal(ctx, legacyID, token); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retired authentication = %v", err)
	}
	if _, err := service.BindRuntime(ctx, legacyID); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("retired bind = %v", err)
	}
	if _, err := service.GetAgentRuntime(ctx, legacyID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("retired executable = %v", err)
	}
	binding, err := service.LookupCollaborationSession(ctx, legacySession)
	if err != nil || binding.State != CollaborationSessionCancelled {
		t.Fatalf("retired session = %#v, err = %v", binding, err)
	}
	runtimes, err := service.ListAgentRuntimes(ctx)
	if err != nil || len(runtimes) != 1 || runtimes[0].ID != owner.Agent.ID {
		t.Fatalf("runtimes = %#v, err = %v", runtimes, err)
	}
	var outstanding, pending int
	if err := service.db.QueryRowContext(ctx, `SELECT outstanding, pending FROM agent_wake_state WHERE agent_id = ?`, legacyID).Scan(&outstanding, &pending); err != nil || outstanding+pending != 0 {
		t.Fatalf("retired wake = %d/%d, err = %v", outstanding, pending, err)
	}
	for _, table := range []string{"named_agents", "room_members", "room_cursors"} {
		column := "member_id"
		if table == "named_agents" {
			column = "id"
		}
		var count int
		if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM `+table+` WHERE `+column+` = ?`, legacyID).Scan(&count); err != nil || count != 0 {
			t.Fatalf("retired participant %s = %d, err = %v", table, count, err)
		}
	}
	var body string
	if err := service.db.QueryRowContext(ctx, `SELECT body FROM collaboration_messages WHERE id = ?`, original.ID).Scan(&body); err != nil || body != original.Body {
		t.Fatalf("original result was lost: %q %v", body, err)
	}
	client, err := service.BindAgent(ctx, owner.Agent.ID)
	if err != nil {
		t.Fatal(err)
	}
	received, err := client.Check(ctx)
	if err != nil || len(received.Collaboration) != 1 || received.Collaboration[0].Body != original.Body {
		t.Fatalf("transferred result = %#v, err = %v", received, err)
	}
	if _, err := service.SettleCollaborationSession(ctx, CollaborationSessionSettleParams{SessionRef: "child", TurnID: "child-turn", State: CollaborationSessionCompleted, Result: "Result completed after migration"}); err != nil {
		t.Fatal(err)
	}
	received, err = client.Check(ctx)
	if err != nil || len(received.Collaboration) != 1 || received.Collaboration[0].Body != "Result completed after migration" {
		t.Fatalf("future result = %#v, err = %v", received, err)
	}
	if err := service.retireRoomRuntimes(ctx); err != nil {
		t.Fatal(err)
	}
	received, err = client.Check(ctx)
	if err != nil || len(received.Collaboration) != 0 {
		t.Fatalf("migration replayed results = %#v, err = %v", received, err)
	}
}
