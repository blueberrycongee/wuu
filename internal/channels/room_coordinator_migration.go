package channels

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"os"
	"path/filepath"
	"strings"

	"github.com/blueberrycongee/wuu/internal/securefs"
)

// Upgrade the retired coordinator state once. Historical transcripts stay
// readable, but old execution bindings cannot resume with a different contract.
func (s *Service) initializeRoomCoordinators(ctx context.Context) error {
	s.mu.Lock()
	defer s.mu.Unlock()
	var version string
	err := s.db.QueryRowContext(ctx, `SELECT value FROM channel_metadata WHERE key = 'room_coordinator_version'`).Scan(&version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if version == "" {
		tx, err := s.db.BeginTx(ctx, nil)
		if err != nil {
			return err
		}
		defer tx.Rollback()
		for _, query := range []string{
			`UPDATE collaboration_session_bindings SET state = 'cancelled', failure_reason = 'Room coordinator upgraded' WHERE principal_id IN (SELECT id FROM room_runtimes)`,
			`UPDATE collaboration_messages SET target_session_ref = NULL, pulled_at = NULL WHERE to_agent_id IN (SELECT id FROM room_runtimes) AND consumed_at IS NULL AND invalidated_at IS NULL`,
			`UPDATE room_runtimes SET autostart = 1`,
			`INSERT INTO channel_metadata(key,value) VALUES ('room_coordinator_version','1')`,
		} {
			if _, err := tx.ExecContext(ctx, query); err != nil {
				return fmt.Errorf("upgrade room coordination: %w", err)
			}
		}
		if err := tx.Commit(); err != nil {
			return err
		}
	}
	if err := s.ensureRoomRuntimes(ctx); err != nil {
		return err
	}
	runtimes, err := s.ListAgentRuntimes(ctx)
	if err != nil {
		return err
	}
	for _, runtime := range runtimes {
		if !runtime.IsRoomRuntime() {
			continue
		}
		tokenPath := filepath.Join(filepath.Dir(runtime.MemoryDir), agentTokenFile)
		raw, err := os.ReadFile(tokenPath)
		if errors.Is(err, os.ErrNotExist) {
			// Earlier migrations retained metadata even when the credential file was
			// absent. Rotate that credential without discarding room state or results.
			token, tokenErr := randomID("chat", 32)
			if tokenErr != nil {
				return tokenErr
			}
			if err := securefs.Mkdir(runtime.MemoryDir); err != nil {
				return err
			}
			if err := securefs.WriteFileAtomic(tokenPath, []byte(token+"\n")); err != nil {
				return err
			}
			raw = []byte(token)
		} else if err != nil {
			return err
		}
		if _, err := s.db.ExecContext(ctx, `UPDATE room_runtimes SET token_hash = ? WHERE id = ?`, tokenHash(strings.TrimSpace(string(raw))), runtime.ID); err != nil {
			return err
		}
	}
	_, err = s.db.ExecContext(ctx, `UPDATE agent_wake_state SET outstanding = 1, pending = 1 WHERE agent_id IN (SELECT id FROM room_runtimes WHERE autostart = 1) AND EXISTS (SELECT 1 FROM collaboration_messages delivery WHERE delivery.to_agent_id = agent_wake_state.agent_id AND delivery.pulled_at IS NULL AND delivery.invalidated_at IS NULL)`)
	return err
}
