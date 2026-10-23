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

// PutPluginTurnLifecycleOutbox stores the terminal event before delivery. The
// owner/request identity is stable, so retrying the same event is idempotent.
func PutPluginTurnLifecycleOutbox(sessDir, pluginID, requestID string, payload json.RawMessage) error {
	pluginID = strings.TrimSpace(pluginID)
	requestID = strings.TrimSpace(requestID)
	if pluginID == "" || requestID == "" {
		return errors.New("plugin lifecycle outbox requires plugin_id and request_id")
	}
	if !json.Valid(payload) {
		return errors.New("plugin lifecycle outbox payload is invalid JSON")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	var identity struct {
		ThreadID string `json:"thread_id"`
		TurnID   string `json:"turn_id"`
		QueueID  string `json:"queue_id"`
	}
	if err := json.Unmarshal(payload, &identity); err != nil {
		return fmt.Errorf("decode plugin lifecycle identity: %w", err)
	}
	now := time.Now().UTC().Format(time.RFC3339Nano)
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin plugin lifecycle store: %w", err)
	}
	defer tx.Rollback()
	_, err = tx.Exec(`
INSERT INTO plugin_turn_lifecycle_outbox (plugin_id, request_id, payload_json, updated_at)
VALUES (?, ?, ?, ?)
ON CONFLICT(plugin_id, request_id) DO UPDATE SET
    payload_json = excluded.payload_json,
	updated_at = excluded.updated_at`, pluginID, requestID, string(payload), now)
	if err != nil {
		return fmt.Errorf("store plugin lifecycle outbox event: %w", err)
	}
	_, err = tx.Exec(`
INSERT INTO plugin_turn_lifecycle (plugin_id, request_id, session_id, turn_id, queue_id, payload_json, updated_at)
VALUES (?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(plugin_id, request_id) DO UPDATE SET
    session_id = excluded.session_id,
    turn_id = excluded.turn_id,
    queue_id = excluded.queue_id,
    payload_json = excluded.payload_json,
    updated_at = excluded.updated_at`, pluginID, requestID, strings.TrimSpace(identity.ThreadID), strings.TrimSpace(identity.TurnID), strings.TrimSpace(identity.QueueID), string(payload), now)
	if err != nil {
		return fmt.Errorf("store plugin lifecycle state: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit plugin lifecycle store: %w", err)
	}
	return nil
}

// DeletePluginTurnLifecycleOutbox acknowledges one delivered terminal event.
func DeletePluginTurnLifecycleOutbox(sessDir, pluginID, requestID string) error {
	pluginID = strings.TrimSpace(pluginID)
	requestID = strings.TrimSpace(requestID)
	if pluginID == "" || requestID == "" {
		return errors.New("plugin lifecycle outbox requires plugin_id and request_id")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	if _, err := db.Exec(`DELETE FROM plugin_turn_lifecycle_outbox WHERE plugin_id = ? AND request_id = ?`, pluginID, requestID); err != nil {
		return fmt.Errorf("acknowledge plugin lifecycle outbox event: %w", err)
	}
	return nil
}

// DeletePluginTurnLifecycleOutboxForPlugin removes delivery and retained
// inspection state when its owning plugin is permanently uninstalled.
func DeletePluginTurnLifecycleOutboxForPlugin(sessDir, pluginID string) error {
	pluginID = strings.TrimSpace(pluginID)
	if pluginID == "" {
		return errors.New("plugin lifecycle outbox requires plugin_id")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return fmt.Errorf("begin plugin lifecycle cleanup: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM plugin_turn_lifecycle_outbox WHERE plugin_id = ?`, pluginID); err != nil {
		return fmt.Errorf("delete plugin lifecycle outbox events: %w", err)
	}
	if _, err := tx.Exec(`DELETE FROM plugin_turn_lifecycle WHERE plugin_id = ?`, pluginID); err != nil {
		return fmt.Errorf("delete retained plugin lifecycle state: %w", err)
	}
	return tx.Commit()
}

// ListPluginTurnLifecycleOutbox returns pending events without creating a
// missing store. Startup recovery can therefore remain a read-only probe.
func ListPluginTurnLifecycleOutbox(sessDir string) ([]PluginTurnLifecycleOutboxEntry, error) {
	db, ok, err := openStoreForScan(sessDir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()
	exists, err := storeTableExists(db, "plugin_turn_lifecycle_outbox")
	if err != nil || !exists {
		return nil, err
	}
	rows, err := db.Query(`
SELECT plugin_id, request_id, payload_json, updated_at
FROM plugin_turn_lifecycle_outbox
ORDER BY updated_at, plugin_id, request_id`)
	if err != nil {
		return nil, fmt.Errorf("list plugin lifecycle outbox events: %w", err)
	}
	defer rows.Close()

	var entries []PluginTurnLifecycleOutboxEntry
	for rows.Next() {
		var entry PluginTurnLifecycleOutboxEntry
		var payload, updatedAt string
		if err := rows.Scan(&entry.PluginID, &entry.RequestID, &payload, &updatedAt); err != nil {
			return nil, fmt.Errorf("scan plugin lifecycle outbox event: %w", err)
		}
		if !json.Valid([]byte(payload)) {
			return nil, fmt.Errorf("plugin lifecycle outbox event %q/%q contains invalid JSON", entry.PluginID, entry.RequestID)
		}
		entry.Payload = json.RawMessage(payload)
		entry.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
		if err != nil {
			return nil, fmt.Errorf("parse plugin lifecycle outbox timestamp: %w", err)
		}
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate plugin lifecycle outbox events: %w", err)
	}
	return entries, nil
}

// FindPluginTurnLifecycle returns the newest durable terminal state matching
// the supplied owner and optional request/session/turn selectors.
func FindPluginTurnLifecycle(sessDir, pluginID, requestID, sessionID, turnID string) (PluginTurnLifecycleEntry, bool, error) {
	db, ok, err := openStoreForScan(sessDir)
	if err != nil || !ok {
		return PluginTurnLifecycleEntry{}, false, err
	}
	defer db.Close()
	exists, err := storeTableExists(db, "plugin_turn_lifecycle")
	if err != nil || !exists {
		return PluginTurnLifecycleEntry{}, false, err
	}
	query := `SELECT plugin_id, request_id, session_id, turn_id, queue_id, payload_json, updated_at
FROM plugin_turn_lifecycle WHERE plugin_id = ?`
	args := []any{strings.TrimSpace(pluginID)}
	if requestID = strings.TrimSpace(requestID); requestID != "" {
		query += ` AND request_id = ?`
		args = append(args, requestID)
	}
	if sessionID = strings.TrimSpace(sessionID); sessionID != "" {
		query += ` AND session_id = ?`
		args = append(args, sessionID)
	}
	if turnID = strings.TrimSpace(turnID); turnID != "" {
		query += ` AND turn_id = ?`
		args = append(args, turnID)
	}
	query += ` ORDER BY updated_at DESC LIMIT 1`
	var entry PluginTurnLifecycleEntry
	var payload, updatedAt string
	if err := db.QueryRow(query, args...).Scan(&entry.PluginID, &entry.RequestID, &entry.SessionID, &entry.TurnID, &entry.QueueID, &payload, &updatedAt); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return PluginTurnLifecycleEntry{}, false, nil
		}
		return PluginTurnLifecycleEntry{}, false, fmt.Errorf("find plugin lifecycle state: %w", err)
	}
	if !json.Valid([]byte(payload)) {
		return PluginTurnLifecycleEntry{}, false, errors.New("plugin lifecycle state contains invalid JSON")
	}
	entry.Payload = json.RawMessage(payload)
	entry.UpdatedAt, err = time.Parse(time.RFC3339Nano, updatedAt)
	if err != nil {
		return PluginTurnLifecycleEntry{}, false, fmt.Errorf("parse plugin lifecycle state timestamp: %w", err)
	}
	return entry, true, nil
}
