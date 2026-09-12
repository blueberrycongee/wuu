package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/memdir"
)

func (s *Service) RoomRoster(ctx context.Context, runtimeID, token, roomID string) (RoomRoster, error) {
	if _, err := s.requireRoomRuntime(ctx, runtimeID, token, roomID); err != nil {
		return RoomRoster{}, err
	}
	room, err := s.GetRoom(ctx, roomID)
	if err != nil {
		return RoomRoster{}, err
	}
	agents, err := s.ListNamedAgents(ctx)
	if err != nil {
		return RoomRoster{}, err
	}
	sessions, err := s.ListCollaborationSessions(ctx, CollaborationSessionListParams{
		RoomID: room.ID, AgentID: runtimeID, Token: token,
	})
	if err != nil {
		return RoomRoster{}, err
	}
	sessionsByAgent := make(map[string][]AgentSessionSummary)
	for _, session := range sessions {
		if session.NamedAgentID == "" {
			continue
		}
		sessionsByAgent[session.NamedAgentID] = append(sessionsByAgent[session.NamedAgentID], AgentSessionSummary{
			SessionRef: session.SessionRef, RoomID: session.RoomID, WorkID: session.WorkID,
			RunID: session.RunID, Purpose: session.Purpose, State: session.State, UpdatedAt: session.UpdatedAt,
		})
	}
	memberIDs := make(map[string]struct{}, len(room.Members))
	for _, member := range room.Members {
		if member.MemberType == MemberAgent {
			memberIDs[member.MemberID] = struct{}{}
		}
	}
	result := RoomRoster{RoomID: room.ID, RoomName: room.Name, MembershipRevision: room.MembershipRevision}
	for _, agent := range agents {
		summary := agentCapabilitySummary(agent)
		if _, ok := memberIDs[agent.ID]; ok {
			memoryIndex, readErr := memdir.ReadIndex(agent.MemoryDir)
			if readErr != nil {
				return RoomRoster{}, fmt.Errorf("read named agent %q memory index: %w", agent.ID, readErr)
			}
			summary.MemoryIndex = memoryIndex.Content
			summary.Sessions = sessionsByAgent[agent.ID]
			result.Members = append(result.Members, summary)
		} else {
			result.AvailableAgents = append(result.AvailableAgents, summary)
		}
	}
	return result, nil
}

func (s *Service) InviteRoomAgent(ctx context.Context, runtimeID, token, roomID, agentID string) (Room, error) {
	if _, err := s.requireRoomRuntime(ctx, runtimeID, token, roomID); err != nil {
		return Room{}, err
	}
	return s.inviteRoomAgent(ctx, runtimeID, roomID, agentID)
}

func (s *Service) inviteRoomAgent(ctx context.Context, runtimeID, roomID, agentID string) (Room, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return Room{}, errors.New("named agent id is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return Room{}, fmt.Errorf("begin room agent invite: %w", err)
	}
	defer tx.Rollback()

	var createdBy, agentName string
	if err := tx.QueryRowContext(ctx, `SELECT created_by FROM rooms WHERE id = ? AND kind = 'channel'`, roomID).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Room{}, fmt.Errorf("%w: channel room %q", ErrNotFound, roomID)
		}
		return Room{}, fmt.Errorf("read invited room: %w", err)
	}
	if err := tx.QueryRowContext(ctx, `SELECT name FROM named_agents WHERE id = ? AND kind = 'named'`, agentID).Scan(&agentName); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return Room{}, fmt.Errorf("%w: named agent %q", ErrNotFound, agentID)
		}
		return Room{}, fmt.Errorf("read invited named agent: %w", err)
	}
	var exists int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_type = 'agent' AND member_id = ?`, roomID, agentID).Scan(&exists); err != nil {
		return Room{}, fmt.Errorf("check room agent membership: %w", err)
	}
	if exists != 0 {
		if err := tx.Commit(); err != nil {
			return Room{}, fmt.Errorf("commit unchanged room agent invite: %w", err)
		}
		return s.GetRoom(ctx, roomID)
	}
	var count int
	if err := tx.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_type = 'agent'`, roomID).Scan(&count); err != nil {
		return Room{}, fmt.Errorf("count room agents: %w", err)
	}
	if count >= MaxRoomAgents {
		return Room{}, fmt.Errorf("room agent limit is %d", MaxRoomAgents)
	}
	now := toMillis(s.now())
	if _, err := tx.ExecContext(ctx, `INSERT INTO room_members (room_id, member_type, member_id, joined_at) VALUES (?, 'agent', ?, ?)`, roomID, agentID, now); err != nil {
		return Room{}, fmt.Errorf("invite room agent: %w", err)
	}
	if _, err := tx.ExecContext(ctx, `INSERT INTO room_cursors (room_id, member_type, member_id, last_read_seq) VALUES (?, 'agent', ?, 0)`, roomID, agentID); err != nil {
		return Room{}, fmt.Errorf("initialize invited room agent cursor: %w", err)
	}
	if err := recordMembershipChangeTx(ctx, tx, roomID, createdBy, []string{agentName}, nil, now); err != nil {
		return Room{}, err
	}
	shouldWake, err := requestWakeTx(ctx, tx, runtimeID, now)
	if err != nil {
		return Room{}, err
	}
	if err := tx.Commit(); err != nil {
		return Room{}, fmt.Errorf("commit room agent invite: %w", err)
	}
	if shouldWake && s.wake != nil {
		s.wake.Deliver(runtimeID)
	}
	return s.GetRoom(ctx, roomID)
}

func agentCapabilitySummary(agent NamedAgent) AgentCapabilitySummary {
	return AgentCapabilitySummary{
		ID: agent.ID, Name: agent.Name, Role: agent.Role,
		EngineOverride: agent.EngineOverride, ProviderOverride: agent.ProviderOverride,
		ModelOverride: agent.ModelOverride, EffortOverride: agent.EffortOverride,
		Sessions: []AgentSessionSummary{},
	}
}

func (s *Service) requireRoomRuntime(ctx context.Context, runtimeID, token, roomID string) (AgentRuntime, error) {
	runtime, err := s.AuthenticatePrincipal(ctx, strings.TrimSpace(runtimeID), strings.TrimSpace(token))
	if err != nil {
		return AgentRuntime{}, err
	}
	roomID = strings.TrimSpace(roomID)
	if !runtime.IsRoomRuntime() || roomID == "" || runtime.RoomID != roomID {
		return AgentRuntime{}, ErrUnauthorized
	}
	return runtime, nil
}

// RoomPeers exposes shared identity capabilities and session activity to room
// members without sharing identity memory or private conversation contents.
func (s *Service) RoomPeers(ctx context.Context, actorID, token, roomID string) ([]AgentCapabilitySummary, error) {
	actor, err := s.AuthenticatePrincipal(ctx, actorID, token)
	if err != nil {
		return nil, err
	}
	roomID = strings.TrimSpace(roomID)
	if err := s.requireRoomPrincipalAccess(ctx, roomID, actor.ID); err != nil {
		return nil, err
	}
	room, err := s.GetRoom(ctx, roomID)
	if err != nil {
		return nil, err
	}
	sessions, err := s.ListCollaborationSessions(ctx, CollaborationSessionListParams{RoomID: roomID, AgentID: actorID, Token: token})
	if err != nil {
		return nil, err
	}
	byAgent := make(map[string][]AgentSessionSummary)
	for _, session := range sessions {
		if session.NamedAgentID == "" {
			continue
		}
		byAgent[session.NamedAgentID] = append(byAgent[session.NamedAgentID], AgentSessionSummary{SessionRef: session.SessionRef, RoomID: session.RoomID, WorkID: session.WorkID, RunID: session.RunID, Purpose: session.Purpose, State: session.State, Title: session.Title, Objective: session.Objective, ParentSessionRef: session.ParentSessionRef, Provider: session.Provider, Model: session.Model, Effort: session.Effort, RuntimeVersion: session.RuntimeVersion, UpdatedAt: session.UpdatedAt})
	}
	peers := make([]AgentCapabilitySummary, 0)
	for _, member := range room.Members {
		if member.MemberType != MemberAgent {
			continue
		}
		agent, err := s.GetNamedAgent(ctx, member.MemberID)
		if err != nil {
			return nil, err
		}
		peer := agentCapabilitySummary(agent)
		if owned, ok := byAgent[agent.ID]; ok {
			peer.Sessions = owned
		}
		peers = append(peers, peer)
	}
	return peers, nil
}
