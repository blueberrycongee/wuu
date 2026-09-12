package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"net/url"
	"os"
	"path/filepath"
	"reflect"
	"runtime"
	"sort"
	"strings"
	"sync"
	"testing"
	"unicode/utf8"
)

type recordingWakeSink struct {
	mu                sync.Mutex
	delivered         []string
	interrupts        []string
	sessionInterrupts []recordedSessionInterrupt
}

type recordedSessionInterrupt struct {
	agentID    string
	sessionRef string
}

func (s *recordingWakeSink) Deliver(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.delivered = append(s.delivered, agentID)
}

func (s *recordingWakeSink) Interrupt(agentID string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interrupts = append(s.interrupts, agentID)
}

func (s *recordingWakeSink) InterruptSession(agentID, sessionRef string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.interrupts = append(s.interrupts, agentID)
	s.sessionInterrupts = append(s.sessionInterrupts, recordedSessionInterrupt{agentID: agentID, sessionRef: sessionRef})
}

func (s *recordingWakeSink) InterruptRunSession(sessionRef string) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.sessionInterrupts = append(s.sessionInterrupts, recordedSessionInterrupt{sessionRef: sessionRef})
}

func (s *recordingWakeSink) take() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := append([]string(nil), s.delivered...)
	s.delivered = nil
	return result
}

func (s *recordingWakeSink) takeInterrupts() []string {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := append([]string(nil), s.interrupts...)
	s.interrupts = nil
	return result
}

func (s *recordingWakeSink) takeSessionInterrupts() []recordedSessionInterrupt {
	s.mu.Lock()
	defer s.mu.Unlock()
	result := append([]recordedSessionInterrupt(nil), s.sessionInterrupts...)
	s.sessionInterrupts = nil
	return result
}

type recordingTelemetrySink struct {
	mu     sync.Mutex
	events []TelemetryEvent
}

func (s *recordingTelemetrySink) RecordChannelEvent(event TelemetryEvent) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.events = append(s.events, event)
}

func (s *recordingTelemetrySink) snapshot() []TelemetryEvent {
	s.mu.Lock()
	defer s.mu.Unlock()
	return append([]TelemetryEvent(nil), s.events...)
}

func openTestService(t *testing.T, wake WakeSink) *Service {
	t.Helper()
	service, err := Open(t.TempDir(), wake)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	t.Cleanup(func() {
		if err := service.Close(); err != nil {
			t.Errorf("Close() error = %v", err)
		}
	})
	return service
}

func bindTestWorkSession(t *testing.T, service *Service, client *AgentClient, roomID, workID, sessionRef string) *AgentClient {
	t.Helper()
	ctx := context.Background()
	if _, err := client.BindCollaborationSession(ctx, CollaborationSessionBindParams{
		SessionRef: sessionRef,
		RoomID:     roomID,
		WorkID:     workID,
		Purpose:    CollaborationSessionWork,
	}); err != nil {
		t.Fatalf("BindCollaborationSession(%s) error = %v", sessionRef, err)
	}
	sessionClient, err := service.BindAgentSession(ctx, client.AgentID(), sessionRef)
	if err != nil {
		t.Fatalf("BindAgentSession(%s) error = %v", sessionRef, err)
	}
	return sessionClient
}

func promoteTestCandidate(t *testing.T, service *Service, client *AgentClient, workID string) Message {
	t.Helper()
	ctx := context.Background()
	work, err := client.GetWork(ctx, workID)
	if err != nil {
		t.Fatalf("GetWork(%s) error = %v", workID, err)
	}
	requestSuffix := fmt.Sprintf("%d", work.CandidateRevision+1)
	run, err := client.StartWorkRun(ctx, WorkRunStartParams{
		WorkID: workID, Kind: WorkRunProducer, RequestID: "test-producer-" + requestSuffix,
	})
	if err != nil {
		t.Fatalf("StartWorkRun(test candidate) error = %v", err)
	}
	artifact, err := client.AddWorkArtifact(ctx, WorkArtifactAddParams{
		WorkID: workID, RunID: run.ID, Kind: WorkArtifactCandidate,
		URI: "artifact://candidate-" + requestSuffix, WorkspaceRevision: "git:test-" + requestSuffix,
	})
	if err != nil {
		t.Fatalf("AddWorkArtifact(test candidate) error = %v", err)
	}
	if _, err := client.PromoteWorkCandidate(ctx, WorkCandidatePromoteParams{
		WorkID: workID, RunID: run.ID, ArtifactRef: artifact.ID,
		RequestID: "test-promotion-" + requestSuffix, SelectionReason: "single test candidate",
	}); err != nil {
		t.Fatalf("PromoteWorkCandidate(test candidate) error = %v", err)
	}
	if _, err := client.FinishWorkRun(ctx, WorkRunFinishParams{
		WorkID: workID, RunID: run.ID, State: WorkRunCompleted,
		Outcome: "test candidate completed", Qualified: true,
	}); err != nil {
		t.Fatalf("FinishWorkRun(test candidate) error = %v", err)
	}
	messages, err := service.ListMessages(ctx, work.RoomID, 0, 200)
	if err != nil {
		t.Fatalf("ListMessages(test candidate) error = %v", err)
	}
	for _, message := range messages {
		if message.ID == workID {
			return message
		}
	}
	t.Fatalf("promoted task %s was not found", workID)
	return Message{}
}

func TestSQLiteDSNNormalizesPaths(t *testing.T) {
	const suffix = "?_pragma=busy_timeout%285000%29&_pragma=foreign_keys%281%29&_txlock=immediate"

	t.Run("unix absolute", func(t *testing.T) {
		got := sqliteDSN("/Users/name/.wuu/workspaces/abc/channels.sqlite3")
		want := "file:///Users/name/.wuu/workspaces/abc/channels.sqlite3" + suffix
		if got != want {
			t.Fatalf("sqliteDSN() = %q, want %q", got, want)
		}
	})

	t.Run("windows drive absolute", func(t *testing.T) {
		if runtime.GOOS != "windows" {
			t.Skip("filepath.ToSlash only normalizes Windows separators on Windows")
		}
		got := sqliteDSN(`C:\Users\name\.wuu\workspaces\abc\channels.sqlite3`)
		want := "file:///C:/Users/name/.wuu/workspaces/abc/channels.sqlite3" + suffix
		if got != want {
			t.Fatalf("sqliteDSN() = %q, want %q", got, want)
		}
	})

	t.Run("url parse round trip", func(t *testing.T) {
		// Whatever platform we are on, url.Parse must succeed and the path
		// component must not be reinterpreted as authority — this is the exact
		// failure that triggered "invalid uri authority" in production.
		dsn := sqliteDSN(`C:\Users\wsmdm\.wuu\workspaces\abc\channels.sqlite3`)
		parsed, err := url.Parse(dsn)
		if err != nil {
			t.Fatalf("url.Parse(%q) error = %v", dsn, err)
		}
		if parsed.Host != "" {
			t.Fatalf("DSN %q has non-empty host/authority %q", dsn, parsed.Host)
		}
	})
}

func createTestAgent(t *testing.T, service *Service, name string) AgentCredential {
	t.Helper()
	credential, err := service.CreateNamedAgent(context.Background(), CreateNamedAgentParams{
		Name:      name,
		Autostart: true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent(%q) error = %v", name, err)
	}
	return credential
}

func TestCreateNamedAgentInitializesMemoryIndex(t *testing.T) {
	service := openTestService(t, nil)
	credential := createTestAgent(t, service, "Alpha")

	data, err := os.ReadFile(filepath.Join(credential.Agent.MemoryDir, agentMemoryIndexFile))
	if err != nil {
		t.Fatalf("ReadFile(MEMORY.md) error = %v", err)
	}
	if len(data) != 0 {
		t.Fatalf("MEMORY.md = %q, want empty index", data)
	}
}

func TestOpenBackfillsMissingNamedAgentMemoryIndex(t *testing.T) {
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open() error = %v", err)
	}
	credential := createTestAgent(t, service, "Alpha")
	indexPath := filepath.Join(credential.Agent.MemoryDir, agentMemoryIndexFile)
	if err := os.Remove(indexPath); err != nil {
		t.Fatalf("Remove(MEMORY.md) error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close() error = %v", err)
	}

	reopened, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("reopen service error = %v", err)
	}
	t.Cleanup(func() { _ = reopened.Close() })
	if _, err := os.Stat(indexPath); err != nil {
		t.Fatalf("Stat(backfilled MEMORY.md) error = %v", err)
	}
}

func createTestRoom(t *testing.T, service *Service, agents ...AgentCredential) Room {
	t.Helper()
	members := make([]RoomMember, 0, len(agents))
	for _, credential := range agents {
		members = append(members, RoomMember{MemberType: MemberAgent, MemberID: credential.Agent.ID})
	}
	room, err := service.CreateRoom(context.Background(), CreateRoomParams{
		Kind:      RoomChannel,
		Name:      "test-room",
		CreatedBy: "human-1",
		Members:   members,
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	return room
}

func createTestDirectMessage(t *testing.T, service *Service, agent AgentCredential) Room {
	t.Helper()
	room, err := service.OpenDirectMessage(context.Background(), "human-1", agent.Agent.ID)
	if err != nil {
		t.Fatalf("OpenDirectMessage(%q) error = %v", agent.Agent.Name, err)
	}
	return room
}

func TestRoomAvatarPersistsOnlyCustomImage(t *testing.T) {
	service := openTestService(t, nil)
	const avatar = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	room, err := service.CreateRoom(context.Background(), CreateRoomParams{
		Kind: RoomChannel, Name: "avatars", AvatarImage: avatar, CreatedBy: "human-1",
	})
	if err != nil {
		t.Fatalf("CreateRoom() error = %v", err)
	}
	if room.AvatarImage != avatar {
		t.Fatalf("created room avatar = %q", room.AvatarImage)
	}
	updated, err := service.UpdateRoomAvatar(context.Background(), room.ID, "")
	if err != nil {
		t.Fatalf("UpdateRoomAvatar() error = %v", err)
	}
	if updated.AvatarImage != "" {
		t.Fatalf("cleared room avatar = %q", updated.AvatarImage)
	}
}

func TestOpenCreatesIndependentChannelsSchema(t *testing.T) {
	service := openTestService(t, nil)
	rows, err := service.db.Query(`SELECT name FROM sqlite_master WHERE type = 'table' ORDER BY name`)
	if err != nil {
		t.Fatalf("list tables: %v", err)
	}
	defer rows.Close()
	var tables []string
	for rows.Next() {
		var name string
		if err := rows.Scan(&name); err != nil {
			t.Fatalf("scan table: %v", err)
		}
		tables = append(tables, name)
	}
	want := []string{
		"agent_wake_state",
		"drafts",
		"inbox_items",
		"named_agents",
		"reminders",
		"room_cursors",
		"room_members",
		"room_messages",
		"rooms",
	}
	for _, table := range want {
		if !containsString(tables, table) {
			t.Errorf("channels schema missing table %q; tables = %v", table, tables)
		}
	}
	if containsString(tables, "participants") || containsString(tables, "sessions") {
		t.Fatalf("channels database leaked legacy/session tables: %v", tables)
	}
	info, err := os.Stat(filepath.Join(service.Dir(), databaseFileName))
	if err != nil {
		t.Fatalf("stat channels database: %v", err)
	}
	if info.Mode().Perm() != 0o600 {
		t.Errorf("channels database mode = %o, want 600", info.Mode().Perm())
	}
}

func TestNamedAgentIdentityIsIndependentAndTokenIsHashed(t *testing.T) {
	service := openTestService(t, nil)
	credential, err := service.CreateNamedAgent(context.Background(), CreateNamedAgentParams{
		Name:             "Alpha",
		ProviderOverride: "provider",
		ModelOverride:    "model",
		Autostart:        true,
	})
	if err != nil {
		t.Fatalf("CreateNamedAgent() error = %v", err)
	}
	if credential.Agent.ID == "" || credential.Token == "" {
		t.Fatalf("credential = %#v, want generated id and token", credential)
	}
	if _, err := normalizeNamedAgentAvatarKey(credential.Agent.AvatarKey); err != nil || credential.Agent.AvatarKey == "" {
		t.Fatalf("generated avatar = %q, err %v", credential.Agent.AvatarKey, err)
	}
	if credential.Agent.MemoryDir != filepath.Join(service.Dir(), "agents", credential.Agent.ID, "memory") {
		t.Errorf("memory dir = %q", credential.Agent.MemoryDir)
	}
	if info, err := os.Stat(credential.Agent.MemoryDir); err != nil || !info.IsDir() {
		t.Fatalf("memory directory not created: info=%v err=%v", info, err)
	}
	var storedHash string
	if err := service.db.QueryRow(`SELECT token_hash FROM named_agents WHERE id = ?`, credential.Agent.ID).Scan(&storedHash); err != nil {
		t.Fatalf("read token hash: %v", err)
	}
	if storedHash == credential.Token || storedHash != tokenHash(credential.Token) {
		t.Fatalf("stored token hash = %q, raw token = %q", storedHash, credential.Token)
	}
	tokenPath := filepath.Join(filepath.Dir(credential.Agent.MemoryDir), agentTokenFile)
	if raw, err := os.ReadFile(tokenPath); err != nil || strings.TrimSpace(string(raw)) != credential.Token {
		t.Fatalf("persisted private token = %q, err %v", raw, err)
	}
	if info, err := os.Stat(tokenPath); err != nil || info.Mode().Perm() != 0o600 {
		t.Fatalf("private token mode = %v, err %v", info, err)
	}
	got, err := service.AuthenticateAgent(context.Background(), credential.Agent.ID, credential.Token)
	if err != nil {
		t.Fatalf("AuthenticateAgent() error = %v", err)
	}
	if !reflect.DeepEqual(got, credential.Agent) {
		t.Errorf("AuthenticateAgent() = %#v, want %#v", got, credential.Agent)
	}
	if _, err := service.AuthenticateAgent(context.Background(), credential.Agent.ID, "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("wrong token error = %v, want ErrUnauthorized", err)
	}
	client, err := service.BindAgent(context.Background(), credential.Agent.ID)
	if err != nil {
		t.Fatalf("BindAgent() error = %v", err)
	}
	client.token = "wrong"
	if _, err := client.Check(context.Background()); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("bound client did not authenticate each call: %v", err)
	}
	if _, err := service.CreateNamedAgent(context.Background(), CreateNamedAgentParams{Name: "alpha"}); !errors.Is(err, ErrConflict) {
		t.Fatalf("case-insensitive duplicate name error = %v, want ErrConflict", err)
	}
	state, err := service.WakeState(context.Background(), credential.Agent.ID)
	if err != nil {
		t.Fatalf("WakeState() error = %v", err)
	}
	if state.Outstanding || state.Pending {
		t.Errorf("initial wake state = %#v, want idle flags", state)
	}
}

func TestRoomMembershipAndDMConstraints(t *testing.T) {
	service := openTestService(t, nil)
	agent := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, agent)
	if len(room.Members) != 2 {
		t.Fatalf("room members = %#v, want creator plus agent", room.Members)
	}
	loaded, err := service.GetRoom(context.Background(), room.ID)
	if err != nil {
		t.Fatalf("GetRoom() error = %v", err)
	}
	if len(loaded.Members) != 2 {
		t.Errorf("loaded members = %#v", loaded.Members)
	}
	if _, err := service.CreateRoom(context.Background(), CreateRoomParams{
		Kind:      RoomDM,
		Name:      "invalid-dm",
		CreatedBy: "human-1",
	}); err == nil {
		t.Fatal("CreateRoom(dm with one member) succeeded")
	}
	if _, err := service.CreateRoom(context.Background(), CreateRoomParams{
		Kind:      RoomChannel,
		Name:      "missing-agent",
		CreatedBy: "human-1",
		Members:   []RoomMember{{MemberType: MemberAgent, MemberID: "agent-missing"}},
	}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("missing agent member error = %v, want ErrNotFound", err)
	}
}

func TestUpdateRoomPersistsTrimmedNameAndValidatesInput(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	room := createTestRoom(t, service)

	name := "  Delivery  "
	updated, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Name: &name})
	if err != nil {
		t.Fatalf("UpdateRoom() error = %v", err)
	}
	if updated.Name != "Delivery" || updated.ID != room.ID || len(updated.Members) != len(room.Members) {
		t.Fatalf("updated room = %#v", updated)
	}
	loaded, err := service.GetRoom(ctx, room.ID)
	if err != nil {
		t.Fatalf("GetRoom() error = %v", err)
	}
	if loaded.Name != "Delivery" {
		t.Fatalf("persisted room name = %q, want Delivery", loaded.Name)
	}

	emptyName := " \t\n "
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Name: &emptyName}); err == nil {
		t.Fatal("UpdateRoom(empty name) succeeded")
	}
	missingName := "Missing"
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: "room-missing", Name: &missingName}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("UpdateRoom(missing room) error = %v, want ErrNotFound", err)
	}
}

func TestUpdateRoomReplacesAgentMembersWithoutResettingUnchangedCursors(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha)
	if _, err := service.db.ExecContext(ctx, `UPDATE room_cursors SET last_read_seq = 7 WHERE room_id = ? AND member_id = ?`, room.ID, alpha.Agent.ID); err != nil {
		t.Fatal(err)
	}

	members := []RoomMember{
		{MemberType: MemberAgent, MemberID: alpha.Agent.ID},
		{MemberType: MemberAgent, MemberID: beta.Agent.ID},
	}
	updated, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members})
	if err != nil {
		t.Fatalf("UpdateRoom(add member) error = %v", err)
	}
	if len(updated.Members) != 3 {
		t.Fatalf("updated members = %#v, want local human plus two agents", updated.Members)
	}
	if updated.MembershipRevision != room.MembershipRevision+1 {
		t.Fatalf("membership revision after add = %d, want %d", updated.MembershipRevision, room.MembershipRevision+1)
	}
	for agentID, want := range map[string]int64{alpha.Agent.ID: 7, beta.Agent.ID: 0} {
		var cursor int64
		if err := service.db.QueryRowContext(ctx, `SELECT last_read_seq FROM room_cursors WHERE room_id = ? AND member_id = ?`, room.ID, agentID).Scan(&cursor); err != nil {
			t.Fatalf("read cursor for %s: %v", agentID, err)
		}
		if cursor != want {
			t.Fatalf("cursor for %s = %d, want %d", agentID, cursor, want)
		}
	}

	members = []RoomMember{{MemberType: MemberAgent, MemberID: beta.Agent.ID}}
	updated, err = service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members})
	if err != nil {
		t.Fatalf("UpdateRoom(remove member) error = %v", err)
	}
	if len(updated.Members) != 2 {
		t.Fatalf("members after removal = %#v", updated.Members)
	}
	if updated.MembershipRevision != room.MembershipRevision+2 {
		t.Fatalf("membership revision after removal = %d, want %d", updated.MembershipRevision, room.MembershipRevision+2)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 2 || messages[0].Kind != MessageSystem || !strings.Contains(messages[0].Body, "Beta") || !strings.Contains(messages[1].Body, "Alpha") {
		t.Fatalf("membership messages = %#v", messages)
	}
	var hasHuman, hasBeta bool
	for _, member := range updated.Members {
		hasHuman = hasHuman || member.MemberType == MemberHuman
		hasBeta = hasBeta || (member.MemberType == MemberAgent && member.MemberID == beta.Agent.ID)
	}
	if !hasHuman || !hasBeta {
		t.Fatalf("members after removal = %#v, want local human and Beta", updated.Members)
	}
	var alphaMemberships int
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, room.ID, alpha.Agent.ID).Scan(&alphaMemberships); err != nil {
		t.Fatal(err)
	}
	if alphaMemberships != 0 {
		t.Fatalf("removed Alpha memberships = %d, want 0", alphaMemberships)
	}
}

func TestUpdateRoomRejectsRemovingAgentWithOwnedTasks(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha, beta)
	task, err := service.CreateTaskHuman(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Investigate", OwnerID: alpha.Agent.ID, HumanID: "human-1",
	})
	if err != nil {
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}

	members := []RoomMember{{MemberType: MemberAgent, MemberID: beta.Agent.ID}}
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); !errors.Is(err, ErrConflict) {
		t.Fatalf("UpdateRoom(remove task owner) error = %v, want ErrConflict", err)
	}

	var memberships int
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, room.ID, alpha.Agent.ID).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 1 {
		t.Fatalf("Alpha memberships = %d, want 1 after rejected update", memberships)
	}
	var owner string
	if err := service.db.QueryRowContext(ctx, `SELECT task_owner FROM room_messages WHERE id = ?`, task.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != alpha.Agent.ID {
		t.Fatalf("task owner = %q, want %q after rejected update", owner, alpha.Agent.ID)
	}

	if _, err := service.UpdateTask(ctx, TaskUpdateParams{
		TaskID: task.ID, State: TaskStateDone, AgentID: alpha.Agent.ID, Token: alpha.Token,
	}); err != nil {
		t.Fatalf("UpdateTask(done) error = %v", err)
	}
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatalf("UpdateRoom(remove completed task owner) error = %v", err)
	}
	if err := service.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_id = ?`, room.ID, alpha.Agent.ID).Scan(&memberships); err != nil {
		t.Fatal(err)
	}
	if memberships != 0 {
		t.Fatalf("Alpha memberships = %d, want 0 after task completed", memberships)
	}
	if err := service.db.QueryRowContext(ctx, `SELECT task_owner FROM room_messages WHERE id = ?`, task.ID).Scan(&owner); err != nil {
		t.Fatal(err)
	}
	if owner != alpha.Agent.ID {
		t.Fatalf("completed task owner = %q, want historical owner %q", owner, alpha.Agent.ID)
	}
}

func TestDeleteRoomCascadesRoomDataAndPreservesNamedAgent(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	agent := createTestAgent(t, service, "Alpha")
	room := createTestDirectMessage(t, service, agent)
	message, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha review this",
	})
	if err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	now := toMillis(service.now())
	if _, err := service.db.ExecContext(ctx, `
		INSERT INTO drafts (id, agent_id, room_id, body, basis_seq, hold_count, state, created_at, updated_at)
		VALUES ('draft-delete-test', ?, ?, 'draft', ?, 1, 'held', ?, ?)`,
		agent.Agent.ID, room.ID, message.Message.Seq, now, now); err != nil {
		t.Fatalf("insert draft: %v", err)
	}
	if _, err := service.db.ExecContext(ctx, `
		INSERT INTO reminders (id, agent_id, fire_at, note, room_id, state, created_at)
		VALUES ('reminder-delete-test', ?, ?, 'follow up', ?, 'pending', ?)`,
		agent.Agent.ID, now+60000, room.ID, now); err != nil {
		t.Fatalf("insert reminder: %v", err)
	}

	for _, table := range []string{"room_members", "room_messages", "room_cursors", "inbox_items", "drafts", "reminders"} {
		var count int
		if err := service.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE room_id = ?", room.ID).Scan(&count); err != nil {
			t.Fatalf("count %s before delete: %v", table, err)
		}
		if count == 0 {
			t.Fatalf("%s has no room-associated fixture before delete", table)
		}
	}

	if err := service.DeleteRoom(ctx, room.ID); err != nil {
		t.Fatalf("DeleteRoom() error = %v", err)
	}
	if _, err := service.GetRoom(ctx, room.ID); !errors.Is(err, ErrNotFound) {
		t.Fatalf("GetRoom(deleted) error = %v, want ErrNotFound", err)
	}
	for _, table := range []string{"room_members", "room_messages", "room_cursors", "inbox_items", "drafts", "reminders"} {
		var count int
		if err := service.db.QueryRowContext(ctx, "SELECT COUNT(*) FROM "+table+" WHERE room_id = ?", room.ID).Scan(&count); err != nil {
			t.Fatalf("count %s after delete: %v", table, err)
		}
		if count != 0 {
			t.Fatalf("%s room-associated rows after delete = %d, want 0", table, count)
		}
	}
	if kept, err := service.GetNamedAgent(ctx, agent.Agent.ID); err != nil || kept.ID != agent.Agent.ID {
		t.Fatalf("named agent after room delete = %#v, err = %v", kept, err)
	}
}

func TestBootstrapLeavesFirstUseEmptyUntilUserCreatesIdentity(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	for range 2 {
		result, err := service.EnsureBootstrap(ctx, "local-user")
		if err != nil {
			t.Fatal(err)
		}
		if len(result.Agents) != 0 || len(result.Rooms) != 0 {
			t.Fatalf("opening collaboration created unconfigured records: %#v", result)
		}
	}
}

func TestDeleteNamedAgentPreservesTaskHistoryAndUpdatesRoomMembership(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, alpha)
	if err := service.DeleteNamedAgent(ctx, alpha.Agent.ID); err != nil {
		t.Fatalf("DeleteNamedAgent() error = %v", err)
	}
	updated, err := service.GetRoom(ctx, room.ID)
	if err != nil {
		t.Fatalf("GetRoom() error = %v", err)
	}
	if updated.MembershipRevision != room.MembershipRevision+1 || len(updated.Members) != 1 {
		t.Fatalf("room after named agent delete = %#v", updated)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 10)
	if err != nil || len(messages) != 1 || messages[0].Kind != MessageSystem || !strings.Contains(messages[0].Body, "Alpha") {
		t.Fatalf("delete membership event = %#v, err = %v", messages, err)
	}

	beta := createTestAgent(t, service, "Beta")
	members := []RoomMember{{MemberType: MemberAgent, MemberID: beta.Agent.ID}}
	if _, err := service.UpdateRoom(ctx, UpdateRoomParams{RoomID: room.ID, Members: &members}); err != nil {
		t.Fatalf("add Beta: %v", err)
	}
	if _, err := service.CreateTaskHuman(ctx, TaskCreateParams{
		RoomID: room.ID, Title: "Keep audit", OwnerID: beta.Agent.ID, HumanID: "human-1",
	}); err != nil {
		t.Fatalf("CreateTaskHuman() error = %v", err)
	}
	if err := service.DeleteNamedAgent(ctx, beta.Agent.ID); !errors.Is(err, ErrConflict) {
		t.Fatalf("DeleteNamedAgent(with task history) error = %v, want ErrConflict", err)
	}
}

func TestBootstrapPreservesExistingIdentityAndRoomWithoutCreatingDefaults(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	andy, err := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Andy", Autostart: true})
	if err != nil {
		t.Fatalf("CreateNamedAgent(Andy) error = %v", err)
	}

	result, err := service.EnsureBootstrap(ctx, "local-user")
	if err != nil {
		t.Fatalf("EnsureBootstrap() error = %v", err)
	}
	if len(result.Agents) != 1 || result.Agents[0].ID != andy.Agent.ID {
		t.Fatalf("bootstrap agents = %#v, want existing Andy", result.Agents)
	}
	if len(result.Rooms) != 0 {
		t.Fatalf("bootstrap created a room for an existing identity: %#v", result.Rooms)
	}
	room, err := service.CreateRoom(ctx, CreateRoomParams{Name: "General", Kind: RoomChannel, CreatedBy: "local-user", Members: []RoomMember{{MemberType: MemberAgent, MemberID: andy.Agent.ID}}})
	if err != nil {
		t.Fatal(err)
	}
	message, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "local-user", Body: "Keep our existing conversation"})
	if err != nil {
		t.Fatal(err)
	}
	result, err = service.EnsureBootstrap(ctx, "local-user")
	if err != nil || len(result.Agents) != 1 || result.Agents[0].ID != andy.Agent.ID || len(result.Rooms) != 1 || result.Rooms[0].ID != room.ID || len(result.Rooms[0].Members) != 2 {
		t.Fatalf("bootstrap changed existing records: %#v, %v", result, err)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 100)
	if err != nil || len(messages) != 1 || messages[0].ID != message.Message.ID {
		t.Fatalf("bootstrap changed conversation history: %#v, %v", messages, err)
	}
	if err := service.DeleteRoom(ctx, room.ID); err != nil {
		t.Fatal(err)
	}
	if err := service.DeleteNamedAgent(ctx, andy.Agent.ID); err != nil {
		t.Fatal(err)
	}
	result, err = service.EnsureBootstrap(ctx, "local-user")
	if err != nil || len(result.Agents) != 0 || len(result.Rooms) != 0 {
		t.Fatalf("bootstrap recreated deleted records: %#v, %v", result, err)
	}
}

func TestNamedAgentModelCanFollowWorkspaceOrUseExplicitProvider(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	created := createTestAgent(t, service, "Andy")
	updated, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{
		ID: created.Agent.ID, Name: "Andy", AvatarKey: "mascot-v1:cloud:headset:202", ProviderOverride: "anthropic", ModelOverride: "claude-sonnet", EffortOverride: "high",
	})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AvatarKey != "mascot-v1:cloud:headset:202" || updated.ProviderOverride != "anthropic" || updated.ModelOverride != "claude-sonnet" || updated.EffortOverride != "high" {
		t.Fatalf("updated = %#v", updated)
	}
	inherited, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: created.Agent.ID, Name: "Andy"})
	if err != nil {
		t.Fatal(err)
	}
	if inherited.ProviderOverride != "" || inherited.ModelOverride != "" || inherited.EffortOverride != "" {
		t.Fatalf("inherited = %#v", inherited)
	}
	if inherited.AvatarKey != "mascot-v1:cloud:headset:202" {
		t.Fatalf("avatar was not preserved: %#v", inherited)
	}
}

func TestNamedAgentCustomAvatarImagePersistsAndCanBeCleared(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	const image = "data:image/png;base64,iVBORw0KGgoAAAANSUhEUgAAAAEAAAABCAQAAAC1HAwCAAAAC0lEQVR42mNk+A8AAQUBAScY42YAAAAASUVORK5CYII="
	created, err := service.CreateNamedAgent(ctx, CreateNamedAgentParams{Name: "Andy", AvatarKey: "abstract-2", AvatarImage: image})
	if err != nil {
		t.Fatal(err)
	}
	if created.Agent.AvatarImage != image {
		t.Fatalf("created avatar image = %q", created.Agent.AvatarImage)
	}
	listed, err := service.ListNamedAgents(ctx)
	if err != nil || len(listed) != 1 || listed[0].AvatarImage != image {
		t.Fatalf("listed agents = %#v, err = %v", listed, err)
	}
	empty := ""
	updated, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: created.Agent.ID, Name: "Andy", AvatarImage: &empty})
	if err != nil {
		t.Fatal(err)
	}
	if updated.AvatarImage != "" || updated.AvatarKey != "abstract-2" {
		t.Fatalf("cleared avatar = %#v", updated)
	}
	invalid := "data:image/svg+xml;base64,PHN2Zz48L3N2Zz4="
	if _, err := service.UpdateNamedAgent(ctx, UpdateNamedAgentParams{ID: created.Agent.ID, Name: "Andy", AvatarImage: &invalid}); err == nil {
		t.Fatal("UpdateNamedAgent() accepted an SVG avatar")
	}
}

func TestMessageTriggersCoalesceWakeAndPersistInbox(t *testing.T) {
	ctx := context.Background()
	wake := &recordingWakeSink{}
	service := openTestService(t, wake)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	alphaRoom := createTestDirectMessage(t, service, alpha)
	betaRoom := createTestDirectMessage(t, service, beta)

	plain, err := service.SendHuman(ctx, HumanSendParams{RoomID: alphaRoom.ID, HumanID: "human-1", Body: "hello everyone"})
	if err != nil {
		t.Fatalf("plain SendHuman() error = %v", err)
	}
	if plain.Message.Seq != 1 || len(plain.WakeAgentIDs) != 1 || len(wake.take()) != 1 {
		t.Fatalf("plain send = %#v", plain)
	}
	for _, agent := range []AgentCredential{alpha, beta} {
		if err := service.ClearWakeOnCheck(ctx, agent.Agent.ID); err != nil {
			t.Fatal(err)
		}
	}
	mentioned, err := service.SendHuman(ctx, HumanSendParams{RoomID: alphaRoom.ID, HumanID: "human-1", Body: "@Alpha, please review"})
	if err != nil {
		t.Fatalf("mention SendHuman() error = %v", err)
	}
	if got, want := mentioned.Message.Mentions, []string{alpha.Agent.ID}; !equalStrings(got, want) {
		t.Fatalf("mentions = %v, want %v", got, want)
	}
	if got, want := mentioned.WakeAgentIDs, []string{alpha.Agent.ID}; !equalStrings(got, want) {
		t.Fatalf("wake targets = %v, want %v", got, want)
	}
	if got := wake.take(); !equalStrings(got, []string{alpha.Agent.ID}) {
		t.Fatalf("delivered wake = %v", got)
	}

	coalesced, err := service.SendHuman(ctx, HumanSendParams{RoomID: alphaRoom.ID, HumanID: "human-1", Body: "@Alpha another detail"})
	if err != nil {
		t.Fatalf("coalesced SendHuman() error = %v", err)
	}
	if len(coalesced.WakeAgentIDs) != 0 || len(wake.take()) != 0 {
		t.Fatalf("duplicate outstanding wake was delivered: %#v", coalesced)
	}
	items, err := service.ListInbox(ctx, alpha.Agent.ID, true)
	if err != nil {
		t.Fatalf("ListInbox() error = %v", err)
	}
	kindCounts := make(map[InboxKind]int)
	for _, item := range items {
		kindCounts[item.Kind]++
	}
	if len(items) != 3 || kindCounts[InboxThreadUpdate] != 1 || kindCounts[InboxMention] != 2 {
		t.Fatalf("alpha inbox = %#v", items)
	}

	_, err = service.SendAgent(ctx, AgentSendParams{
		RoomID: alphaRoom.ID, AgentID: alpha.Agent.ID, Token: alpha.Token,
		Body: "@Alpha self note", BasisSeq: coalesced.Message.Seq,
	})
	if err != nil {
		t.Fatalf("self mention SendAgent() error = %v", err)
	}
	if got := wake.take(); len(got) != 0 {
		t.Fatalf("agent woke itself: %v", got)
	}

	if _, err := service.db.Exec(`UPDATE agent_wake_state SET outstanding = 0 WHERE agent_id = ?`, beta.Agent.ID); err != nil {
		t.Fatalf("clear beta wake: %v", err)
	}
	root, err := service.SendAgent(ctx, AgentSendParams{
		RoomID: betaRoom.ID, AgentID: beta.Agent.ID, Token: beta.Token,
		Body: "proposal", BasisSeq: 0,
	})
	if err != nil {
		t.Fatalf("root SendAgent() error = %v", err)
	}
	reply, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: betaRoom.ID, HumanID: "human-1", ReplyTo: root.Message.ID, Body: "please expand",
	})
	if err != nil {
		t.Fatalf("reply SendHuman() error = %v", err)
	}
	if reply.Message.ThreadID != root.Message.ID {
		t.Fatalf("reply thread = %q, want root %q", reply.Message.ThreadID, root.Message.ID)
	}
	if got, want := reply.WakeAgentIDs, []string{beta.Agent.ID}; !equalStrings(got, want) {
		t.Fatalf("human reply wake = %v, want %v", got, want)
	}
	if got := wake.take(); !equalStrings(got, []string{beta.Agent.ID}) {
		t.Fatalf("human reply delivered wake = %v", got)
	}
	betaInbox, err := service.ListInbox(ctx, beta.Agent.ID, true)
	if err != nil {
		t.Fatalf("ListInbox(beta) error = %v", err)
	}
	kinds := make([]InboxKind, 0, len(betaInbox))
	for _, item := range betaInbox {
		if item.MessageID == reply.Message.ID {
			kinds = append(kinds, item.Kind)
		}
	}
	sort.Slice(kinds, func(i, j int) bool { return kinds[i] < kinds[j] })
	if len(kinds) != 1 || kinds[0] != InboxReply {
		t.Fatalf("reply inbox kinds = %v, want one reply signal", kinds)
	}
}

func TestM2SameBasisHoldsCollidingAnswer(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	beta := createTestAgent(t, service, "Beta")
	room := createTestRoom(t, service, alpha, beta)

	first, err := service.SendAgent(ctx, AgentSendParams{
		RoomID: room.ID, AgentID: alpha.Agent.ID, Token: alpha.Token, Body: "1", BasisSeq: 0,
	})
	if err != nil {
		t.Fatalf("first SendAgent() error = %v", err)
	}
	second, err := service.SendAgent(ctx, AgentSendParams{
		RoomID: room.ID, AgentID: beta.Agent.ID, Token: beta.Token, Body: "1", BasisSeq: 0,
	})
	if err != nil {
		t.Fatalf("second SendAgent() error = %v", err)
	}
	if first.Status != SendCommitted || first.Message.Seq != 1 {
		t.Fatalf("first send = %#v, want committed seq 1", first)
	}
	if second.Status != SendHeld || second.Draft == nil || second.Draft.HoldCount != 1 {
		t.Fatalf("second same-basis send = %#v, want held draft", second)
	}
	if second.Delta == nil || second.Delta.Count != 1 || len(second.Delta.Items) != 1 || second.Delta.Items[0].Preview != "1" {
		t.Fatalf("held delta = %#v, want first answer summary", second.Delta)
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, 10)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != 1 || messages[0].Body != "1" {
		t.Fatalf("same-basis M2 messages = %#v", messages)
	}
}

func TestMentionPresentRequiresTokenBoundaries(t *testing.T) {
	tests := []struct {
		body string
		want bool
	}{
		{body: "@Alpha", want: true},
		{body: "please ask (@Alpha), thanks", want: true},
		{body: "Hello@Alpha", want: false},
		{body: "@Alphabet", want: false},
	}
	for _, test := range tests {
		if got := mentionPresent(test.body, "Alpha"); got != test.want {
			t.Errorf("mentionPresent(%q, Alpha) = %v, want %v", test.body, got, test.want)
		}
	}
}

func TestWakeStateRetainsFollowupAndReleasesCompletedAttempt(t *testing.T) {
	ctx := context.Background()
	sink := &recordingWakeSink{}
	service := openTestService(t, sink)
	agent := createTestAgent(t, service, "Alpha")
	room := createTestDirectMessage(t, service, agent)

	if _, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "first query",
	}); err != nil {
		t.Fatalf("first SendHuman() error = %v", err)
	}
	if _, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "second query",
	}); err != nil {
		t.Fatalf("second SendHuman() error = %v", err)
	}
	state, err := service.WakeState(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("WakeState() error = %v", err)
	}
	if !state.Outstanding || !state.Pending {
		t.Fatalf("repeated query wake state = %#v, want outstanding and pending", state)
	}
	if got := sink.take(); len(got) != 1 || got[0] != agent.Agent.ID {
		t.Fatalf("wake deliveries = %#v, want one coalesced delivery", got)
	}

	followup, err := service.FinishWakeAttempt(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("first FinishWakeAttempt() error = %v", err)
	}
	if !followup {
		t.Fatal("first FinishWakeAttempt() = false, want retained follow-up")
	}
	state, err = service.WakeState(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("WakeState() after first finish error = %v", err)
	}
	if !state.Outstanding || state.Pending {
		t.Fatalf("follow-up wake state = %#v, want only outstanding", state)
	}

	followup, err = service.FinishWakeAttempt(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("second FinishWakeAttempt() error = %v", err)
	}
	if followup {
		t.Fatal("second FinishWakeAttempt() retained a nonexistent query")
	}
	state, err = service.WakeState(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("final WakeState() error = %v", err)
	}
	if state.Outstanding || state.Pending {
		t.Fatalf("completed wake state = %#v, want clear", state)
	}
}

func TestWakeCompletionAfterCheckDoesNotCreateFollowup(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	agent := createTestAgent(t, service, "Alpha")
	room := createTestDirectMessage(t, service, agent)

	if _, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "query",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	if _, err := service.Check(ctx, agent.Agent.ID, agent.Token); err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	followup, err := service.FinishWakeAttempt(ctx, agent.Agent.ID)
	if err != nil {
		t.Fatalf("FinishWakeAttempt() error = %v", err)
	}
	if followup {
		t.Fatal("checked wake created a follow-up")
	}
	state, err := service.WakeState(ctx, agent.Agent.ID)
	if err != nil || state.Outstanding || state.Pending {
		t.Fatalf("checked completion wake state = %#v, err %v", state, err)
	}
}

func TestCheckAndReadAuthenticateAdvanceAndClearWake(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	outsider := createTestAgent(t, service, "Outsider")
	room := createTestDirectMessage(t, service, alpha)
	body := "@Alpha " + strings.Repeat("细节", 50)
	sent, err := service.SendHuman(ctx, HumanSendParams{RoomID: room.ID, HumanID: "human-1", Body: body})
	if err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	if _, err := service.Check(ctx, alpha.Agent.ID, "wrong"); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("Check(wrong token) error = %v, want ErrUnauthorized", err)
	}
	checked, err := service.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil {
		t.Fatalf("Check() error = %v", err)
	}
	if len(checked.Items) != 1 || checked.Items[0].MessageID != sent.Message.ID || checked.HasMore {
		t.Fatalf("Check() = %#v", checked)
	}
	if got := utf8.RuneCountInString(checked.Items[0].Preview); got != checkPreviewRunes+1 {
		t.Fatalf("preview runes = %d, want %d including ellipsis", got, checkPreviewRunes+1)
	}
	if len(checked.Scopes) != 1 || checked.Scopes[0].RoomID != room.ID || checked.Scopes[0].Seq != sent.Message.Seq {
		t.Fatalf("check scopes = %#v", checked.Scopes)
	}
	state, err := service.WakeState(ctx, alpha.Agent.ID)
	if err != nil || state.Outstanding || state.Pending {
		t.Fatalf("checked wake state = %#v, err %v", state, err)
	}
	unpulled, err := service.ListInbox(ctx, alpha.Agent.ID, true)
	if err != nil || len(unpulled) != 0 {
		t.Fatalf("unpulled inbox = %#v, err %v", unpulled, err)
	}
	messages, err := service.ReadInboxMessages(ctx, alpha.Agent.ID, alpha.Token, []string{checked.Items[0].ID})
	if err != nil || len(messages) != 1 || messages[0].Body != body {
		t.Fatalf("ReadInboxMessages() = %#v, err %v", messages, err)
	}
	if _, err := service.ReadInboxMessages(ctx, outsider.Agent.ID, outsider.Token, []string{checked.Items[0].ID}); !errors.Is(err, ErrNotFound) {
		t.Fatalf("outsider ReadInboxMessages() error = %v, want ErrNotFound", err)
	}
	if _, err := service.ReadAgentMessages(ctx, outsider.Agent.ID, outsider.Token, room.ID, 0, 10); !errors.Is(err, ErrUnauthorized) {
		t.Fatalf("outsider ReadAgentMessages() error = %v, want ErrUnauthorized", err)
	}
}

func TestConcurrentMessageSequencesAreContiguous(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	agent := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, agent)

	const count = 20
	var wg sync.WaitGroup
	errorsCh := make(chan error, count)
	for range count {
		wg.Add(1)
		go func() {
			defer wg.Done()
			_, err := service.SendHuman(ctx, HumanSendParams{
				RoomID: room.ID, HumanID: "human-1", Body: "message",
			})
			errorsCh <- err
		}()
	}
	wg.Wait()
	close(errorsCh)
	for err := range errorsCh {
		if err != nil {
			t.Fatalf("concurrent SendHuman() error = %v", err)
		}
	}
	messages, err := service.ListMessages(ctx, room.ID, 0, count)
	if err != nil {
		t.Fatalf("ListMessages() error = %v", err)
	}
	if len(messages) != count {
		t.Fatalf("message count = %d, want %d", len(messages), count)
	}
	for index, message := range messages {
		if message.Seq != int64(index+1) {
			t.Fatalf("message[%d].Seq = %d, want %d", index, message.Seq, index+1)
		}
	}
}

func TestCheckPaginationDoesNotConsumeOverflowItem(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestDirectMessage(t, service, alpha)
	for index := 0; index < checkLimit+1; index++ {
		if _, err := service.SendHuman(ctx, HumanSendParams{
			RoomID: room.ID, HumanID: "human-1", Body: fmt.Sprintf("@Alpha item %d", index+1),
		}); err != nil {
			t.Fatalf("SendHuman(%d) error = %v", index+1, err)
		}
	}
	first, err := service.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil {
		t.Fatalf("first Check() error = %v", err)
	}
	if len(first.Items) != checkLimit || !first.HasMore {
		t.Fatalf("first Check() items = %d, has_more = %v", len(first.Items), first.HasMore)
	}
	second, err := service.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil {
		t.Fatalf("second Check() error = %v", err)
	}
	if len(second.Items) != 1 || second.HasMore || second.Items[0].Preview != "@Alpha item 51" {
		t.Fatalf("second Check() = %#v", second)
	}
}

func TestOpenMigratesLegacyAgentInboxAndPreservesItems(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(current) error = %v", err)
	}
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestDirectMessage(t, service, alpha)
	if _, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "@Alpha legacy",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close(current) error = %v", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(dir, databaseFileName)))
	if err != nil {
		t.Fatalf("open legacy fixture database: %v", err)
	}
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`DROP INDEX idx_inbox_items_agent_pull`,
		`DROP INDEX idx_inbox_items_unique`,
		`ALTER TABLE inbox_items RENAME TO inbox_items_current`,
		`CREATE TABLE inbox_items (
			id TEXT PRIMARY KEY,
			agent_id TEXT NOT NULL,
			room_id TEXT NOT NULL,
			message_id TEXT NOT NULL,
			kind TEXT NOT NULL,
			created_at INTEGER NOT NULL,
			pulled_at INTEGER
		)`,
		`INSERT INTO inbox_items(id, agent_id, room_id, message_id, kind, created_at, pulled_at)
			SELECT id, member_id, room_id, message_id, kind, created_at, pulled_at FROM inbox_items_current`,
		`DROP TABLE inbox_items_current`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("prepare legacy inbox with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy fixture database: %v", err)
	}

	upgraded, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(legacy) error = %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	check, err := upgraded.Check(ctx, alpha.Agent.ID, alpha.Token)
	if err != nil {
		t.Fatalf("Check(upgraded) error = %v", err)
	}
	if len(check.Items) != 1 || check.Items[0].Preview != "@Alpha legacy" {
		t.Fatalf("upgraded inbox = %#v", check)
	}
	reminder, err := upgraded.SetReminderAfter(ctx, ReminderSetParams{
		AgentID: alpha.Agent.ID, Token: alpha.Token, Note: "works after migration",
	}, MinReminderDur)
	if err != nil || reminder.RoomID != "" {
		t.Fatalf("SetReminderAfter(upgraded) = %#v, %v", reminder, err)
	}
}

func TestOpenMigratesLegacyAgentRoomCursorsBeforeBootstrap(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(current) error = %v", err)
	}
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, alpha)
	if _, err := service.db.Exec(`UPDATE room_cursors SET last_read_seq = 7 WHERE room_id = ? AND member_type = 'agent' AND member_id = ?`, room.ID, alpha.Agent.ID); err != nil {
		t.Fatalf("seed current room cursor: %v", err)
	}
	if err := service.Close(); err != nil {
		t.Fatalf("Close(current) error = %v", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(dir, databaseFileName)))
	if err != nil {
		t.Fatalf("open legacy fixture database: %v", err)
	}
	for _, statement := range []string{
		`PRAGMA foreign_keys = OFF`,
		`ALTER TABLE room_cursors RENAME TO room_cursors_current`,
		`CREATE TABLE room_cursors (
			room_id TEXT NOT NULL,
			agent_id TEXT NOT NULL,
			last_read_seq INTEGER NOT NULL DEFAULT 0,
			PRIMARY KEY (room_id, agent_id),
			FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
			FOREIGN KEY (agent_id) REFERENCES named_agents(id) ON DELETE CASCADE
		)`,
		`INSERT INTO room_cursors(room_id, agent_id, last_read_seq)
			SELECT room_id, member_id, last_read_seq FROM room_cursors_current WHERE member_type = 'agent'`,
		`DROP TABLE room_cursors_current`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("prepare legacy room cursors with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy fixture database: %v", err)
	}

	upgraded, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(legacy) error = %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	var memberType string
	var lastReadSeq int64
	if err := upgraded.db.QueryRow(`SELECT member_type, last_read_seq FROM room_cursors WHERE room_id = ? AND member_id = ?`, room.ID, alpha.Agent.ID).Scan(&memberType, &lastReadSeq); err != nil {
		t.Fatalf("read upgraded room cursor: %v", err)
	}
	if memberType != string(MemberAgent) || lastReadSeq != 7 {
		t.Fatalf("upgraded room cursor = (%q, %d), want (agent, 7)", memberType, lastReadSeq)
	}
	if _, err := upgraded.EnsureBootstrap(ctx, "local-user"); err != nil {
		t.Fatalf("EnsureBootstrap(upgraded) error = %v", err)
	}
}

func TestOpenAddsColumnsMissingFromLegacySchema(t *testing.T) {
	ctx := context.Background()
	dir := t.TempDir()
	service, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(current) error = %v", err)
	}
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, alpha)
	if err := service.Close(); err != nil {
		t.Fatalf("Close(current) error = %v", err)
	}

	db, err := sql.Open("sqlite", sqliteDSN(filepath.Join(dir, databaseFileName)))
	if err != nil {
		t.Fatalf("open legacy fixture database: %v", err)
	}
	for _, statement := range []string{
		`DROP INDEX IF EXISTS idx_drafts_agent_session_state`,
		`ALTER TABLE drafts DROP COLUMN session_ref`,
		`ALTER TABLE room_messages DROP COLUMN task_title`,
		`ALTER TABLE reminders DROP COLUMN created_at`,
		`ALTER TABLE named_agents DROP COLUMN avatar_key`,
		`ALTER TABLE named_agents DROP COLUMN effort_override`,
	} {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatalf("prepare legacy schema with %q: %v", statement, err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatalf("close legacy fixture database: %v", err)
	}

	upgraded, err := Open(dir, nil)
	if err != nil {
		t.Fatalf("Open(legacy) error = %v", err)
	}
	t.Cleanup(func() { _ = upgraded.Close() })
	if exists, err := upgraded.tableHasColumn("drafts", "session_ref"); err != nil || !exists {
		t.Fatalf("upgraded drafts.session_ref exists = %v, err = %v", exists, err)
	}
	upgradedAgent, err := upgraded.GetNamedAgent(ctx, alpha.Agent.ID)
	if err != nil || upgradedAgent.AvatarKey == "" || upgradedAgent.EffortOverride != "" {
		t.Fatalf("GetNamedAgent(upgraded) = %#v, err %v", upgradedAgent, err)
	}
	if _, err := upgraded.ListMessages(ctx, room.ID, 0, 50); err != nil {
		t.Fatalf("ListMessages(upgraded) error = %v", err)
	}
	if _, err := upgraded.SetReminderAfter(ctx, ReminderSetParams{
		AgentID: alpha.Agent.ID, Token: alpha.Token, Note: "after schema upgrade",
	}, MinReminderDur); err != nil {
		t.Fatalf("SetReminderAfter(upgraded) error = %v", err)
	}
}

func TestChannelTelemetrySinkRecordsCommittedAndHeldSends(t *testing.T) {
	ctx := context.Background()
	service := openTestService(t, nil)
	telemetry := &recordingTelemetrySink{}
	service.SetTelemetrySink(telemetry)
	alpha := createTestAgent(t, service, "Alpha")
	room := createTestRoom(t, service, alpha)
	if _, err := service.SendHuman(ctx, HumanSendParams{
		RoomID: room.ID, HumanID: "human-1", Body: "first",
	}); err != nil {
		t.Fatalf("SendHuman() error = %v", err)
	}
	result, err := service.SendAgent(ctx, AgentSendParams{
		RoomID: room.ID, AgentID: alpha.Agent.ID, Token: alpha.Token,
		Body: "stale", BasisSeq: 0,
	})
	if err != nil || result.Status != SendHeld {
		t.Fatalf("SendAgent(stale) = %#v, %v", result, err)
	}
	events := telemetry.snapshot()
	if len(events) != 2 || events[0].Name != "message_committed" || events[0].MemberType != MemberHuman {
		t.Fatalf("committed telemetry = %#v", events)
	}
	if events[1].Name != "draft_held" || events[1].MemberID != alpha.Agent.ID || events[1].HoldCount != 1 {
		t.Fatalf("held telemetry = %#v", events[1])
	}
}

func containsString(values []string, target string) bool {
	for _, value := range values {
		if value == target {
			return true
		}
	}
	return false
}

func equalStrings(left, right []string) bool {
	if len(left) != len(right) {
		return false
	}
	for index := range left {
		if left[index] != right[index] {
			return false
		}
	}
	return true
}
