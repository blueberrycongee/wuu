package channels

import (
	"database/sql"
	"fmt"
)

// migrateCollaborationPrincipals moves hidden room execution out of the
// product's NamedAgent/member namespace. The principal table is intentionally
// small: it exists only for wake and private-delivery foreign keys shared by
// visible agents and hidden runtimes.
func (s *Service) migrateCollaborationPrincipals() error {
	tx, err := s.db.Begin()
	if err != nil {
		return fmt.Errorf("begin collaboration principal migration: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO collaboration_principals(id, kind)
		SELECT id, CASE kind WHEN 'room' THEN 'room_runtime' ELSE 'named_agent' END
		FROM named_agents WHERE deleted_at IS NULL`); err != nil {
		return fmt.Errorf("backfill collaboration principals: %w", err)
	}
	if _, err := tx.Exec(`
		INSERT OR IGNORE INTO room_runtimes(id, room_id, memory_dir, token_hash, autostart, created_at)
		SELECT id, room_id, memory_dir, token_hash, autostart, created_at
		FROM named_agents WHERE kind = 'room' AND room_id IS NOT NULL`); err != nil {
		return fmt.Errorf("migrate room runtimes: %w", err)
	}

	wakeTarget, err := foreignKeyTargetTx(tx, "agent_wake_state", "agent_id")
	if err != nil {
		return err
	}
	if wakeTarget == "named_agents" {
		if _, err := tx.Exec(`ALTER TABLE agent_wake_state RENAME TO agent_wake_state_legacy`); err != nil {
			return fmt.Errorf("rename legacy wake state: %w", err)
		}
		if _, err := tx.Exec(`
			CREATE TABLE agent_wake_state (
				agent_id TEXT PRIMARY KEY,
				outstanding INTEGER NOT NULL DEFAULT 0 CHECK (outstanding IN (0, 1)),
				pending INTEGER NOT NULL DEFAULT 0 CHECK (pending IN (0, 1)),
				updated_at INTEGER NOT NULL,
				FOREIGN KEY (agent_id) REFERENCES collaboration_principals(id) ON DELETE CASCADE
			)`); err != nil {
			return fmt.Errorf("create principal wake state: %w", err)
		}
		if _, err := tx.Exec(`INSERT INTO agent_wake_state SELECT * FROM agent_wake_state_legacy`); err != nil {
			return fmt.Errorf("copy principal wake state: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE agent_wake_state_legacy`); err != nil {
			return fmt.Errorf("drop legacy wake state: %w", err)
		}
	}

	collaborationTarget, err := foreignKeyTargetTx(tx, "collaboration_messages", "to_agent_id")
	if err != nil {
		return err
	}
	if collaborationTarget == "named_agents" {
		if _, err := tx.Exec(`DROP INDEX IF EXISTS idx_collaboration_inbox`); err != nil {
			return fmt.Errorf("drop legacy collaboration index: %w", err)
		}
		if _, err := tx.Exec(`ALTER TABLE collaboration_messages RENAME TO collaboration_messages_legacy`); err != nil {
			return fmt.Errorf("rename legacy collaboration messages: %w", err)
		}
		if _, err := tx.Exec(`
			CREATE TABLE collaboration_messages (
				id TEXT PRIMARY KEY,
				room_id TEXT NOT NULL,
				from_type TEXT NOT NULL CHECK (from_type IN ('human', 'agent')),
				from_id TEXT NOT NULL,
				from_session_ref TEXT,
				to_agent_id TEXT NOT NULL,
				target_session_ref TEXT,
				kind TEXT NOT NULL DEFAULT 'control' CHECK (kind IN ('control', 'candidate_ready', 'verification_feedback')),
				body TEXT NOT NULL,
				source_message_id TEXT,
				goal_revision INTEGER NOT NULL DEFAULT 0,
				candidate_revision INTEGER NOT NULL DEFAULT 0,
				reply_to TEXT,
				created_at INTEGER NOT NULL,
				pulled_at INTEGER,
				FOREIGN KEY (room_id) REFERENCES rooms(id) ON DELETE CASCADE,
				FOREIGN KEY (to_agent_id) REFERENCES collaboration_principals(id) ON DELETE CASCADE,
				FOREIGN KEY (source_message_id) REFERENCES room_messages(id) ON DELETE CASCADE,
				FOREIGN KEY (reply_to) REFERENCES collaboration_messages(id)
			)`); err != nil {
			return fmt.Errorf("create principal collaboration messages: %w", err)
		}
		if _, err := tx.Exec(`
			INSERT INTO collaboration_messages(
				id, room_id, from_type, from_id, from_session_ref, to_agent_id, target_session_ref, kind, body, source_message_id,
				goal_revision, candidate_revision, reply_to, created_at, pulled_at
			)
			SELECT id, room_id, from_type, from_id, from_session_ref, to_agent_id, target_session_ref, kind, body, source_message_id,
				goal_revision, candidate_revision, reply_to, created_at, pulled_at
			FROM collaboration_messages_legacy`); err != nil {
			return fmt.Errorf("copy principal collaboration messages: %w", err)
		}
		if _, err := tx.Exec(`DROP TABLE collaboration_messages_legacy`); err != nil {
			return fmt.Errorf("drop legacy collaboration messages: %w", err)
		}
		if _, err := tx.Exec(`CREATE INDEX idx_collaboration_inbox ON collaboration_messages(to_agent_id, pulled_at, created_at)`); err != nil {
			return fmt.Errorf("create collaboration inbox index: %w", err)
		}
	}

	if _, err := tx.Exec(`
		DELETE FROM room_members
		WHERE member_type = 'agent' AND member_id IN (SELECT id FROM room_runtimes)`); err != nil {
		return fmt.Errorf("remove runtime room memberships: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM named_agents WHERE kind = 'room'`); err != nil {
		return fmt.Errorf("remove legacy room named agents: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit collaboration principal migration: %w", err)
	}
	return nil
}

func foreignKeyTargetTx(tx *sql.Tx, table, column string) (string, error) {
	rows, err := tx.Query(`PRAGMA foreign_key_list(` + table + `)`)
	if err != nil {
		return "", fmt.Errorf("inspect %s foreign keys: %w", table, err)
	}
	defer rows.Close()
	for rows.Next() {
		var id, seq int
		var target, from, to, onUpdate, onDelete, match string
		if err := rows.Scan(&id, &seq, &target, &from, &to, &onUpdate, &onDelete, &match); err != nil {
			return "", fmt.Errorf("scan %s foreign key: %w", table, err)
		}
		if from == column {
			return target, nil
		}
	}
	if err := rows.Err(); err != nil {
		return "", fmt.Errorf("read %s foreign keys: %w", table, err)
	}
	return "", nil
}
