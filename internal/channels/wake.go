package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

func (s *Service) MarkWakePending(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errors.New("wake agent is required")
	}
	now := toMillis(s.now())
	result, err := s.db.ExecContext(ctx, `
		UPDATE agent_wake_state SET pending = 1, updated_at = ?
		WHERE agent_id = ? AND outstanding = 1`, now, agentID)
	if err != nil {
		return fmt.Errorf("mark agent wake pending: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count pending agent wake: %w", err)
	}
	if rows == 0 {
		if _, err := s.GetNamedAgent(ctx, agentID); err != nil {
			return err
		}
	}
	return nil
}

// FinishWakeAttempt releases the wake owned by a completed agent turn. If a
// wake-eligible event arrived in the meantime, it retains one follow-up turn.
func (s *Service) FinishWakeAttempt(ctx context.Context, agentID string) (bool, error) {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return false, errors.New("wake agent is required")
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	tx, err := s.db.BeginTx(ctx, nil)
	if err != nil {
		return false, fmt.Errorf("begin wake attempt finish: %w", err)
	}
	defer tx.Rollback()
	var pending, outstanding int
	err = tx.QueryRowContext(ctx, `
		SELECT pending, outstanding FROM agent_wake_state WHERE agent_id = ?`, agentID,
	).Scan(&pending, &outstanding)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: named agent %q", ErrNotFound, agentID)
	}
	if err != nil {
		return false, fmt.Errorf("read completed agent wake: %w", err)
	}
	followup := outstanding != 0 && pending != 0
	nextOutstanding := 0
	if followup {
		nextOutstanding = 1
	}
	if _, err := tx.ExecContext(ctx, `
		UPDATE agent_wake_state SET outstanding = ?, pending = 0, updated_at = ? WHERE agent_id = ?`,
		nextOutstanding, toMillis(s.now()), agentID); err != nil {
		return false, fmt.Errorf("finish completed agent wake: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit wake attempt finish: %w", err)
	}
	return followup, nil
}

// requestWakeTx requests a delivery when the agent is idle. If another wake
// already owns the agent, retain one pending follow-up instead of dropping the
// event. The caller delivers only when this returns true.
func requestWakeTx(ctx context.Context, tx *sql.Tx, agentID string, now int64) (bool, error) {
	var outstanding int
	err := tx.QueryRowContext(ctx, `
		SELECT wake.outstanding FROM agent_wake_state wake JOIN named_agents agent ON agent.id = wake.agent_id AND agent.kind = 'named' WHERE wake.agent_id = ?`, agentID,
	).Scan(&outstanding)
	if errors.Is(err, sql.ErrNoRows) {
		return false, fmt.Errorf("%w: named agent %q", ErrNotFound, agentID)
	}
	if err != nil {
		return false, fmt.Errorf("read agent wake request state: %w", err)
	}
	if outstanding != 0 {
		if _, err := tx.ExecContext(ctx, `
			UPDATE agent_wake_state SET pending = 1, updated_at = ? WHERE agent_id = ?`,
			now, agentID); err != nil {
			return false, fmt.Errorf("retain pending agent wake: %w", err)
		}
		return false, nil
	}
	result, err := tx.ExecContext(ctx, `
		UPDATE agent_wake_state SET outstanding = 1, pending = 0, updated_at = ?
		WHERE agent_id = ? AND outstanding = 0`, now, agentID)
	if err != nil {
		return false, fmt.Errorf("request agent wake: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count requested agent wake: %w", err)
	}
	return rows != 0, nil
}

func (s *Service) ClearWakeOnCheck(ctx context.Context, agentID string) error {
	agentID = strings.TrimSpace(agentID)
	if agentID == "" {
		return errors.New("wake agent is required")
	}
	result, err := s.db.ExecContext(ctx, `
		UPDATE agent_wake_state SET outstanding = 0, pending = 0, updated_at = ? WHERE agent_id = ?`,
		toMillis(s.now()), agentID)
	if err != nil {
		return fmt.Errorf("clear checked agent wake: %w", err)
	}
	rows, err := result.RowsAffected()
	if err != nil {
		return fmt.Errorf("count checked agent wake: %w", err)
	}
	if rows == 0 {
		return fmt.Errorf("%w: named agent %q", ErrNotFound, agentID)
	}
	return nil
}
