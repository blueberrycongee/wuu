package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
)

// LoadRequestContextCheckpoint returns the latest cache-recovery state. It is
// separate from conversation history and absent on sessions predating support.
func LoadRequestContextCheckpoint(sessDir, sessionID string) (json.RawMessage, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	var raw string
	err = db.QueryRow(`SELECT request_context_json FROM sessions WHERE id = ?`, strings.TrimSpace(sessionID)).Scan(&raw)
	if errors.Is(err, sql.ErrNoRows) {
		return nil, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	if err != nil {
		return nil, fmt.Errorf("load request context checkpoint: %w", err)
	}
	return json.RawMessage(raw), nil
}

// SaveRequestContextCheckpoint replaces only the cache checkpoint, leaving all
// conversation messages unchanged. There is one bounded-by-context row per session.
func SaveRequestContextCheckpoint(sessDir, sessionID string, payload json.RawMessage) error {
	if !json.Valid(payload) {
		return errors.New("request context checkpoint must be valid JSON")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	result, err := db.Exec(`UPDATE sessions SET request_context_json = ? WHERE id = ?`, string(payload), strings.TrimSpace(sessionID))
	if err != nil {
		return fmt.Errorf("save request context checkpoint: %w", err)
	}
	count, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if count == 0 {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	return nil
}
