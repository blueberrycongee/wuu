package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

func (s *Service) ProposeRoomAgent(ctx context.Context, actorID, token, roomID, name, role string) (AgentCreationProposal, error) {
	if _, err := s.requireRoomMember(ctx, actorID, token, roomID); err != nil {
		return AgentCreationProposal{}, err
	}
	name, role = strings.TrimSpace(name), strings.TrimSpace(role)
	if name == "" {
		return AgentCreationProposal{}, errors.New("proposed agent name is required")
	}

	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentCreationProposal{}, fmt.Errorf("begin agent creation proposal: %w", err)
	}
	defer tx.Rollback()
	var createdBy string
	if err := tx.QueryRowContext(ctx, `SELECT created_by FROM rooms WHERE id = ? AND kind = 'channel'`, roomID).Scan(&createdBy); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return AgentCreationProposal{}, fmt.Errorf("%w: channel room %q", ErrNotFound, roomID)
		}
		return AgentCreationProposal{}, err
	}
	var existing AgentCreationProposal
	row := tx.QueryRowContext(ctx, `
		SELECT id, message_id, room_id, name, role, state, provider, model, created_agent_id, created_at, resolved_at
		FROM agent_creation_proposals WHERE room_id = ? AND name = ? COLLATE NOCASE AND state IN ('pending', 'processing')
		ORDER BY created_at DESC LIMIT 1`, roomID, name)
	if scanErr := scanAgentCreationProposal(row, &existing); scanErr == nil {
		return existing, nil
	} else if !errors.Is(scanErr, ErrNotFound) {
		return AgentCreationProposal{}, scanErr
	}
	proposalID, err := randomID("agent-proposal", 12)
	if err != nil {
		return AgentCreationProposal{}, err
	}
	messageID, err := randomID("msg", 12)
	if err != nil {
		return AgentCreationProposal{}, err
	}
	var seq int64
	if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM room_messages WHERE room_id = ?`, roomID).Scan(&seq); err != nil {
		return AgentCreationProposal{}, err
	}
	now := s.now()
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO room_messages (id, room_id, seq, author_type, author_id, kind, body, mentions_json, created_at)
		VALUES (?, ?, ?, 'human', ?, 'system', ?, '[]', ?)`,
		messageID, roomID, seq, createdBy, "建议创建新角色："+name, toMillis(now)); err != nil {
		return AgentCreationProposal{}, fmt.Errorf("insert agent proposal message: %w", err)
	}
	proposal := AgentCreationProposal{ID: proposalID, MessageID: messageID, RoomID: roomID, Name: name, Role: role, State: AgentCreationPending, CreatedAt: now}
	if _, err := tx.ExecContext(ctx, `
		INSERT INTO agent_creation_proposals (id, message_id, room_id, name, role, state, created_at)
		VALUES (?, ?, ?, ?, ?, 'pending', ?)`, proposal.ID, proposal.MessageID, proposal.RoomID, proposal.Name, proposal.Role, toMillis(now)); err != nil {
		return AgentCreationProposal{}, fmt.Errorf("insert agent creation proposal: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return AgentCreationProposal{}, err
	}
	return proposal, nil
}

func (s *Service) ResolveAgentCreationProposal(ctx context.Context, params ResolveAgentCreationProposalParams) (AgentCreationProposal, error) {
	params.ProposalID, params.HumanID = strings.TrimSpace(params.ProposalID), strings.TrimSpace(params.HumanID)
	params.Provider, params.Model = strings.TrimSpace(params.Provider), strings.TrimSpace(params.Model)
	if params.ProposalID == "" || params.HumanID == "" {
		return AgentCreationProposal{}, errors.New("proposal and human are required")
	}
	proposal, err := s.getAgentCreationProposal(ctx, params.ProposalID)
	if err != nil {
		return AgentCreationProposal{}, err
	}
	var memberExists int
	if err := s.db.QueryRowContext(ctx, `SELECT COUNT(*) FROM room_members WHERE room_id = ? AND member_type = 'human' AND member_id = ?`, proposal.RoomID, params.HumanID).Scan(&memberExists); err != nil {
		return AgentCreationProposal{}, err
	}
	if memberExists == 0 {
		return AgentCreationProposal{}, ErrUnauthorized
	}
	if proposal.State != AgentCreationPending {
		return proposal, fmt.Errorf("%w: agent creation proposal is %s", ErrConflict, proposal.State)
	}
	now := s.now()
	if !params.Approve {
		return s.finishAgentCreationProposal(ctx, proposal, params.HumanID, "", now)
	}
	claimed, err := s.db.ExecContext(ctx, `UPDATE agent_creation_proposals SET state = 'processing', provider = ?, model = ? WHERE id = ? AND state = 'pending'`, params.Provider, params.Model, proposal.ID)
	if err != nil {
		return AgentCreationProposal{}, err
	}
	if rows, _ := claimed.RowsAffected(); rows != 1 {
		return AgentCreationProposal{}, fmt.Errorf("%w: agent creation proposal is already resolved", ErrConflict)
	}
	credential, createErr := s.CreateNamedAgent(ctx, CreateNamedAgentParams{
		Name: proposal.Name, Role: proposal.Role, ProviderOverride: params.Provider, ModelOverride: params.Model, Autostart: true,
	})
	if createErr == nil {
		_, createErr = s.inviteRoomAgent(ctx, proposal.RoomID, credential.Agent.ID)
	}
	if createErr != nil {
		_, _ = s.db.ExecContext(ctx, `UPDATE agent_creation_proposals SET state = 'pending', provider = '', model = '' WHERE id = ? AND state = 'processing'`, proposal.ID)
		if credential.Agent.ID != "" {
			_ = s.DeleteNamedAgent(ctx, credential.Agent.ID)
		}
		return AgentCreationProposal{}, createErr
	}
	return s.finishAgentCreationProposal(ctx, proposal, params.HumanID, credential.Agent.ID, now)
}

func (s *Service) finishAgentCreationProposal(ctx context.Context, proposal AgentCreationProposal, humanID, createdAgentID string, now time.Time) (AgentCreationProposal, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return AgentCreationProposal{}, err
	}
	defer tx.Rollback()
	state, expectedState := AgentCreationCancelled, AgentCreationPending
	body := "用户取消了创建新角色：" + proposal.Name
	if createdAgentID != "" {
		state, expectedState = AgentCreationApproved, AgentCreationProcessing
		body = "用户批准了创建新角色：" + proposal.Name
	}
	result, err := tx.ExecContext(ctx, `UPDATE agent_creation_proposals SET state = ?, created_agent_id = ?, resolved_at = ? WHERE id = ? AND state = ?`, state, createdAgentID, toMillis(now), proposal.ID, expectedState)
	if err != nil {
		return AgentCreationProposal{}, err
	}
	if rows, _ := result.RowsAffected(); rows != 1 {
		return AgentCreationProposal{}, fmt.Errorf("%w: agent creation proposal is already resolved", ErrConflict)
	}
	if state == AgentCreationCancelled {
		var seq int64
		if err := tx.QueryRowContext(ctx, `SELECT COALESCE(MAX(seq), 0) + 1 FROM room_messages WHERE room_id = ?`, proposal.RoomID).Scan(&seq); err != nil {
			return AgentCreationProposal{}, err
		}
		messageID, err := randomID("msg", 12)
		if err != nil {
			return AgentCreationProposal{}, err
		}
		if _, err := tx.ExecContext(ctx, `INSERT INTO room_messages (id, room_id, seq, author_type, author_id, kind, body, mentions_json, created_at) VALUES (?, ?, ?, 'human', ?, 'system', ?, '[]', ?)`,
			messageID, proposal.RoomID, seq, humanID, body, toMillis(now)); err != nil {
			return AgentCreationProposal{}, err
		}
	}
	// A decision is actionable human input. Commit its receipts with the state
	// change so a resolved proposal cannot lose the members' continuation.
	wakeIDs, err := enqueueAgentCreationDecisionTx(ctx, tx, proposal, humanID, body, now)
	if err != nil {
		return AgentCreationProposal{}, err
	}
	if err := tx.Commit(); err != nil {
		return AgentCreationProposal{}, err
	}
	for _, agentID := range wakeIDs {
		if s.wake != nil {
			s.wake.Deliver(agentID)
		}
	}
	return s.getAgentCreationProposal(ctx, proposal.ID)
}

func enqueueAgentCreationDecisionTx(ctx context.Context, tx *sql.Tx, proposal AgentCreationProposal, humanID, body string, now time.Time) ([]string, error) {
	rows, err := tx.QueryContext(ctx, `SELECT member.member_id FROM room_members member JOIN named_agents agent ON agent.id = member.member_id AND agent.kind = 'named' WHERE member.room_id = ? AND member.member_type = 'agent' ORDER BY member.member_id`, proposal.RoomID)
	if err != nil {
		return nil, err
	}
	var members []string
	for rows.Next() {
		var id string
		if err := rows.Scan(&id); err != nil {
			rows.Close()
			return nil, err
		}
		members = append(members, id)
	}
	if err := rows.Close(); err != nil {
		return nil, err
	}
	if err := rows.Err(); err != nil {
		return nil, err
	}
	var wakeIDs []string
	for _, id := range members {
		if _, err := enqueueCollaborationTx(ctx, tx, CollaborationMessage{
			RoomID: proposal.RoomID, FromType: MemberHuman, FromID: humanID, ToAgentID: id,
			Kind: CollaborationControl, Body: body, SourceMessageID: proposal.MessageID,
			CorrelationID: proposal.ID, RequestID: "agent-proposal-decision:" + proposal.ID, CreatedAt: now,
		}); err != nil {
			return nil, err
		}
		requested, err := requestWakeTx(ctx, tx, id, toMillis(now))
		if err != nil {
			return nil, err
		}
		if requested {
			wakeIDs = append(wakeIDs, id)
		}
	}
	return wakeIDs, nil
}

func (s *Service) attachAgentCreationProposals(ctx context.Context, messages []Message) error {
	if len(messages) == 0 {
		return nil
	}
	rows, err := s.db.QueryContext(ctx, `
		SELECT id, message_id, room_id, name, role, state, provider, model, created_agent_id, created_at, resolved_at
		FROM agent_creation_proposals WHERE room_id = ?`, messages[0].RoomID)
	if err != nil {
		return err
	}
	defer rows.Close()
	byMessage := make(map[string]AgentCreationProposal)
	for rows.Next() {
		var proposal AgentCreationProposal
		if err := scanAgentCreationProposal(rows, &proposal); err != nil {
			return err
		}
		byMessage[proposal.MessageID] = proposal
	}
	if err := rows.Err(); err != nil {
		return err
	}
	for index := range messages {
		if proposal, ok := byMessage[messages[index].ID]; ok {
			messages[index].AgentCreationProposal = &proposal
		}
	}
	return nil
}

func (s *Service) getAgentCreationProposal(ctx context.Context, id string) (AgentCreationProposal, error) {
	var proposal AgentCreationProposal
	err := scanAgentCreationProposal(s.db.QueryRowContext(ctx, `
		SELECT id, message_id, room_id, name, role, state, provider, model, created_agent_id, created_at, resolved_at
		FROM agent_creation_proposals WHERE id = ?`, id), &proposal)
	return proposal, err
}

func scanAgentCreationProposal(row scanner, proposal *AgentCreationProposal) error {
	var createdAt int64
	var resolvedAt sql.NullInt64
	if err := row.Scan(&proposal.ID, &proposal.MessageID, &proposal.RoomID, &proposal.Name, &proposal.Role, &proposal.State,
		&proposal.Provider, &proposal.Model, &proposal.CreatedAgentID, &createdAt, &resolvedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return ErrNotFound
		}
		return err
	}
	proposal.CreatedAt = fromMillis(createdAt)
	if resolvedAt.Valid {
		value := fromMillis(resolvedAt.Int64)
		proposal.ResolvedAt = &value
	}
	return nil
}
