package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
)

// CollaborationTurnScope records immutable provenance and settled turn usage.
// A conversation may move between rooms; its previous turns never do.
type CollaborationTurnScope struct {
	RoomID, WorkID, RunID string
	OwnerNamedAgentID     string
	GoalRevision          int
	InputTokens           int64
	OutputTokens          int64
}

// LookupCollaborationTurnScope reads historical scope for a trusted host caller.
// It never infers a missing turn's scope from the conversation's current room.
func (s *Service) LookupCollaborationTurnScope(ctx context.Context, sessionRef, turnID string) (CollaborationTurnScope, error) {
	var scope CollaborationTurnScope
	err := s.db.QueryRowContext(ctx, `SELECT room_id,work_id,run_id,goal_revision,work_owner_id,input_tokens,output_tokens FROM collaboration_turn_scopes WHERE session_ref=? AND turn_id=?`, sessionRef, turnID).
		Scan(&scope.RoomID, &scope.WorkID, &scope.RunID, &scope.GoalRevision, &scope.OwnerNamedAgentID, &scope.InputTokens, &scope.OutputTokens)
	if errors.Is(err, sql.ErrNoRows) {
		err = ErrNotFound
	}
	return scope, err
}

func (s *Service) migrateCollaborationTurnScopes() error {
	_, err := s.db.Exec(`CREATE TABLE IF NOT EXISTS collaboration_turn_scopes (
		session_ref TEXT NOT NULL REFERENCES collaboration_session_bindings(session_ref) ON DELETE CASCADE,
		turn_id TEXT NOT NULL, room_id TEXT NOT NULL, work_id TEXT NOT NULL DEFAULT '',
		run_id TEXT NOT NULL DEFAULT '', goal_revision INTEGER NOT NULL DEFAULT 0,
		work_owner_id TEXT NOT NULL DEFAULT '',
		input_tokens INTEGER NOT NULL DEFAULT 0, output_tokens INTEGER NOT NULL DEFAULT 0,
		PRIMARY KEY(session_ref, turn_id));
		INSERT OR IGNORE INTO collaboration_turn_scopes(session_ref,turn_id,room_id,work_id,run_id,goal_revision)
		SELECT run.session_ref,run.turn_id,work.room_id,work.id,run.id,run.goal_revision
		FROM work_runs run JOIN works work ON work.id=run.work_id
		JOIN collaboration_session_bindings binding ON binding.session_ref=run.session_ref
		WHERE run.turn_id!='';
		INSERT OR IGNORE INTO collaboration_turn_scopes(session_ref,turn_id,room_id)
		SELECT message.source_session_ref,message.source_turn_id,message.room_id FROM room_messages message
		JOIN collaboration_session_bindings binding ON binding.session_ref=message.source_session_ref
		WHERE COALESCE(message.source_turn_id,'')!='';`)
	// Results without historical scope evidence remain available to their owner.
	// Guessing from a conversation's latest room would disclose older results.
	if err != nil {
		return err
	}
	for _, column := range []struct{ name, definition string }{
		{"work_owner_id", "TEXT NOT NULL DEFAULT ''"},
		{"input_tokens", "INTEGER NOT NULL DEFAULT 0"},
		{"output_tokens", "INTEGER NOT NULL DEFAULT 0"},
	} {
		hasColumn, err := s.tableHasColumn("collaboration_turn_scopes", column.name)
		if err != nil {
			return err
		}
		if !hasColumn {
			if _, err := s.db.Exec(`ALTER TABLE collaboration_turn_scopes ADD COLUMN ` + column.name + ` ` + column.definition); err != nil {
				return err
			}
		}
	}
	return nil
}

func recordCollaborationTurnScopeTx(ctx context.Context, tx *sql.Tx, binding CollaborationSessionBinding, turnID string) (CollaborationTurnScope, error) {
	var scope CollaborationTurnScope
	err := tx.QueryRowContext(ctx, `SELECT room_id,work_id,run_id,goal_revision,work_owner_id FROM collaboration_turn_scopes WHERE session_ref=? AND turn_id=?`, binding.SessionRef, turnID).
		Scan(&scope.RoomID, &scope.WorkID, &scope.RunID, &scope.GoalRevision, &scope.OwnerNamedAgentID)
	if err == nil {
		return scope, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return scope, err
	}
	scope.RoomID, scope.WorkID, scope.RunID = binding.RoomID, binding.WorkID, binding.RunID
	if scope.WorkID != "" {
		if err := tx.QueryRowContext(ctx, `SELECT goal_revision,owner_named_agent_id FROM works WHERE id=?`, scope.WorkID).Scan(&scope.GoalRevision, &scope.OwnerNamedAgentID); err != nil {
			return scope, err
		}
	}
	_, err = tx.ExecContext(ctx, `INSERT INTO collaboration_turn_scopes(session_ref,turn_id,room_id,work_id,run_id,goal_revision,work_owner_id) VALUES(?,?,?,?,?,?,?)`, binding.SessionRef, turnID, scope.RoomID, scope.WorkID, scope.RunID, scope.GoalRevision, scope.OwnerNamedAgentID)
	return scope, err
}

func validateCollaborationTurnScopeTx(ctx context.Context, tx *sql.Tx, binding CollaborationSessionBinding, targetWorkID string, allowCompleted bool) error {
	var scope CollaborationTurnScope
	err := tx.QueryRowContext(ctx, `SELECT room_id,work_id,run_id,goal_revision,work_owner_id FROM collaboration_turn_scopes WHERE session_ref=? AND turn_id=?`, binding.SessionRef, binding.TurnID).
		Scan(&scope.RoomID, &scope.WorkID, &scope.RunID, &scope.GoalRevision, &scope.OwnerNamedAgentID)
	if errors.Is(err, sql.ErrNoRows) {
		return nil // Legacy sessions acquire a scope when their next turn starts.
	}
	if err != nil {
		return err
	}
	if scope.RoomID != binding.RoomID || targetWorkID != "" && scope.WorkID != "" && targetWorkID != scope.WorkID {
		return fmt.Errorf("%w: turn belongs to another room or work", ErrConflict)
	}
	if scope.WorkID == "" {
		return nil
	}
	var revision int
	var state WorkState
	var owner string
	if err := tx.QueryRowContext(ctx, `SELECT goal_revision,state,owner_named_agent_id FROM works WHERE id=?`, scope.WorkID).Scan(&revision, &state, &owner); err != nil {
		return err
	}
	if revision != scope.GoalRevision || scope.OwnerNamedAgentID != "" && owner != scope.OwnerNamedAgentID || terminalWorkState(state) && !(allowCompleted && state == WorkCompleted) {
		return fmt.Errorf("%w: turn's task was revised or ended", ErrConflict)
	}
	return nil
}
