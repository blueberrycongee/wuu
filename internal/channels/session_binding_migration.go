package channels

import (
	"fmt"
	"strings"
)

func (s *Service) migrateCollaborationSessions() error {
	if err := s.migrateCollaborationSessionStates(); err != nil {
		return err
	}
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin collaboration session migration: %w", err)
	}
	defer tx.Rollback()
	for _, statement := range []string{
		`CREATE TABLE IF NOT EXISTS named_agent_conversations (
            agent_id TEXT PRIMARY KEY REFERENCES named_agents(id) ON DELETE CASCADE,
            session_ref TEXT NOT NULL UNIQUE
        )`,
		`DROP INDEX IF EXISTS idx_work_runs_session`,
		`CREATE UNIQUE INDEX IF NOT EXISTS idx_work_runs_active_session
			ON work_runs(session_ref)
			WHERE session_ref IS NOT NULL AND state IN ('queued', 'running')`,
		`CREATE INDEX IF NOT EXISTS idx_collaboration_inbox_session
			ON collaboration_messages(to_agent_id, target_session_ref, pulled_at, created_at)`,
		`CREATE INDEX IF NOT EXISTS idx_drafts_agent_session_state
			ON drafts(agent_id, session_ref, state, updated_at)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migrate collaboration session indexes: %w", err)
		}
	}
	if _, err := tx.Exec(`
		UPDATE work_runs AS run
		SET named_agent_id = CASE
			WHEN EXISTS (SELECT 1 FROM named_agents agent WHERE agent.id = run.profile) THEN run.profile
			WHEN run.kind = 'producer' THEN (
				SELECT work.owner_named_agent_id FROM works work WHERE work.id = run.work_id
			)
			ELSE NULL
		END
		WHERE run.named_agent_id IS NULL`); err != nil {
		return fmt.Errorf("backfill work run named agents: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO collaboration_session_bindings(
			session_ref, principal_id, named_agent_id, room_id, work_id, run_id,
			purpose, state, created_at, updated_at
		)
		SELECT run.session_ref, run.named_agent_id, run.named_agent_id, work.room_id,
			run.work_id, run.id,
			CASE WHEN run.kind = 'verifier' THEN 'verification' ELSE 'work' END,
			CASE run.state
				WHEN 'queued' THEN 'running'
				WHEN 'running' THEN 'running'
				WHEN 'interrupted' THEN 'interrupted'
				ELSE 'idle'
			END,
			run.created_at, run.updated_at
		FROM work_runs run
		JOIN works work ON work.id = run.work_id
		WHERE run.session_ref IS NOT NULL AND run.named_agent_id IS NOT NULL`); err != nil {
		return fmt.Errorf("backfill collaboration session bindings: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collaboration session migration: %w", err)
	}
	return nil
}

// SQLite cannot extend a CHECK constraint in place. Copy the existing table
// atomically so installed work bindings retain their scopes and run references.
func (s *Service) migrateCollaborationSessionStates() error {
	var schema string
	if err := s.db.QueryRow(`SELECT sql FROM sqlite_master WHERE type = 'table' AND name = 'collaboration_session_bindings'`).Scan(&schema); err != nil {
		return err
	}
	if strings.Contains(schema, "'completed'") && strings.Contains(schema, "'queued'") && strings.Contains(schema, "'waiting'") {
		return nil
	}
	tx, err := s.db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	updated := strings.Replace(schema, "collaboration_session_bindings", "collaboration_session_bindings_next", 1)
	if !strings.Contains(updated, "'completed'") {
		updated = strings.Replace(updated, "'missing'", "'missing', 'completed', 'cancelled', 'failed'", 1)
	}
	if !strings.Contains(updated, "'waiting'") {
		updated = strings.Replace(updated, "'idle'", "'idle', 'waiting'", 1)
	}
	if !strings.Contains(updated, "'queued'") {
		updated = strings.Replace(updated, "'idle'", "'idle', 'queued'", 1)
	}
	if !strings.Contains(updated, "'starting'") {
		updated = strings.Replace(updated, "'idle'", "'idle', 'starting'", 1)
	}
	for _, statement := range []string{
		updated,
		`INSERT INTO collaboration_session_bindings_next SELECT * FROM collaboration_session_bindings`,
		`DROP TABLE collaboration_session_bindings`,
		`ALTER TABLE collaboration_session_bindings_next RENAME TO collaboration_session_bindings`,
		`CREATE INDEX idx_collaboration_sessions_principal ON collaboration_session_bindings(principal_id, state, updated_at)`,
		`CREATE INDEX idx_collaboration_sessions_scope ON collaboration_session_bindings(room_id, work_id, principal_id)`,
	} {
		if _, err := tx.Exec(statement); err != nil {
			return fmt.Errorf("migrate collaboration session states: %w", err)
		}
	}
	return tx.Commit()
}
