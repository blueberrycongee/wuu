package session

import (
	"errors"
	"fmt"
	"strings"
)

// SetHistoryEngineTurnID records the native turn that accepted one durable user
// prompt and whether its outcome is still pending. Admission first stores an
// empty ID with pending=true, before sending input. A retry may confirm the same
// identity, but cannot rebind the prompt to a replaced turn or native session.
func SetHistoryEngineTurnID(sessDir, id string, seq int, ref, turnID string, pending bool) error {
	turnID = strings.TrimSpace(turnID)
	if strings.TrimSpace(ref) == "" || seq <= 0 {
		return errors.New("native turn boundary requires a persisted prompt and session")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	result, err := db.Exec(`UPDATE session_messages SET engine_turn_id = ?, engine_turn_pending = ?
  WHERE session_id = ? AND seq = ? AND role = 'user'
  AND (engine_turn_id = '' OR engine_turn_id = ?)
  AND EXISTS (SELECT 1 FROM sessions WHERE id = ? AND engine_ref = ?)`, turnID, boolInt(pending), id, seq, turnID, id, ref)
	if err != nil {
		return fmt.Errorf("store native turn boundary: %w", err)
	}
	n, err := result.RowsAffected()
	if err != nil {
		return err
	}
	if n != 1 {
		return errors.New("native turn boundary no longer matches the admitted prompt")
	}
	return nil
}
