// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

// WritePluginGenerationSnapshot persists the generation snapshot a session is
// bound to. It is idempotent: the latest write for a session wins.
func WritePluginGenerationSnapshot(sessDir, sessionID string, snapshot PluginGenerationSnapshot) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	normalized := snapshot.Normalize()
	payload, err := json.Marshal(normalized)
	if err != nil {
		return fmt.Errorf("encode plugin generation snapshot: %w", err)
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	now := time.Now().UTC().Format(time.RFC3339Nano)
	if _, err := db.Exec(`
		INSERT INTO session_plugin_generation_snapshots(session_id, snapshot_json, updated_at)
		VALUES(?, ?, ?)
		ON CONFLICT(session_id) DO UPDATE SET
			snapshot_json = excluded.snapshot_json,
			updated_at = excluded.updated_at`,
		sessionID, string(payload), now); err != nil {
		return fmt.Errorf("write plugin generation snapshot: %w", err)
	}
	return nil
}

// ReadPluginGenerationSnapshot returns the snapshot bound to a session. found
// is false when the session has no recorded snapshot.
func ReadPluginGenerationSnapshot(sessDir, sessionID string) (PluginGenerationSnapshot, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return PluginGenerationSnapshot{}, false, errors.New("session_id is required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return PluginGenerationSnapshot{}, false, err
	}
	defer db.Close()
	var payload string
	if err := db.QueryRow(`
		SELECT snapshot_json
		FROM session_plugin_generation_snapshots
		WHERE session_id = ?`, sessionID).Scan(&payload); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PluginGenerationSnapshot{}, false, nil
		}
		return PluginGenerationSnapshot{}, false, fmt.Errorf("read plugin generation snapshot: %w", err)
	}
	var snapshot PluginGenerationSnapshot
	if err := json.Unmarshal([]byte(payload), &snapshot); err != nil {
		return PluginGenerationSnapshot{}, false, fmt.Errorf("decode plugin generation snapshot: %w", err)
	}
	return snapshot.Normalize(), true, nil
}
