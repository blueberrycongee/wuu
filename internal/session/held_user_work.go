package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
)

const (
	HeldUserWorkOriginQueue = "queue"
	HeldUserWorkOriginSteer = "steer"
)

type HeldUserWork struct {
	SessionID   string
	Position    int
	ID          string
	Origin      string
	MessageJSON []byte
	RuntimeJSON []byte
}

func ReplaceHeldUserWork(sessDir, sessionID string, entries []HeldUserWork) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
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
		return fmt.Errorf("begin held user work replacement: %w", err)
	}
	defer tx.Rollback()
	if _, err := tx.Exec(`DELETE FROM held_user_work WHERE session_id = ?`, sessionID); err != nil {
		return fmt.Errorf("clear held user work: %w", err)
	}
	for position, entry := range entries {
		id := strings.TrimSpace(entry.ID)
		origin := strings.TrimSpace(entry.Origin)
		if id == "" {
			return fmt.Errorf("held user work %d: id is required", position)
		}
		if origin != HeldUserWorkOriginQueue && origin != HeldUserWorkOriginSteer {
			return fmt.Errorf("held user work %q: invalid origin %q", id, origin)
		}
		if len(entry.MessageJSON) == 0 || len(entry.RuntimeJSON) == 0 {
			return fmt.Errorf("held user work %q: message and runtime are required", id)
		}
		if _, err := tx.Exec(`
INSERT INTO held_user_work(session_id, position, id, origin, message_json, runtime_json)
VALUES (?, ?, ?, ?, ?, ?)`, sessionID, position, id, origin, string(entry.MessageJSON), string(entry.RuntimeJSON)); err != nil {
			return fmt.Errorf("insert held user work %q: %w", id, err)
		}
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit held user work replacement: %w", err)
	}
	return nil
}

func LoadHeldUserWork(sessDir, sessionID string) ([]HeldUserWork, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("session_id is required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return nil, err
	}
	defer db.Close()
	rows, err := db.Query(`
SELECT position, id, origin, message_json, runtime_json
FROM held_user_work
WHERE session_id = ? AND state = 'pending'
ORDER BY CASE origin WHEN 'steer' THEN 0 ELSE 1 END, position`, sessionID)
	if err != nil {
		return nil, fmt.Errorf("load held user work: %w", err)
	}
	defer rows.Close()
	var entries []HeldUserWork
	for rows.Next() {
		var entry HeldUserWork
		var messageJSON, runtimeJSON string
		entry.SessionID = sessionID
		if err := rows.Scan(&entry.Position, &entry.ID, &entry.Origin, &messageJSON, &runtimeJSON); err != nil {
			return nil, fmt.Errorf("scan held user work: %w", err)
		}
		entry.MessageJSON = []byte(messageJSON)
		entry.RuntimeJSON = []byte(runtimeJSON)
		entries = append(entries, entry)
	}
	if err := rows.Err(); err != nil {
		return nil, fmt.Errorf("iterate held user work: %w", err)
	}
	return entries, nil
}

func DeleteHeldUserWork(sessDir, sessionID, id string) (bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	id = strings.TrimSpace(id)
	if sessionID == "" || id == "" {
		return false, errors.New("session_id and id are required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return false, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return false, fmt.Errorf("begin held user work deletion: %w", err)
	}
	defer tx.Rollback()
	var position int
	if err := tx.QueryRow(`SELECT position FROM held_user_work WHERE session_id = ? AND id = ?`, sessionID, id).Scan(&position); err != nil {
		if errors.Is(err, sql.ErrNoRows) {
			return false, nil
		}
		return false, fmt.Errorf("find held user work: %w", err)
	}
	res, err := tx.Exec(`DELETE FROM held_user_work WHERE session_id = ? AND id = ?`, sessionID, id)
	if err != nil {
		return false, fmt.Errorf("delete held user work: %w", err)
	}
	affected, err := res.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("count deleted held user work: %w", err)
	}
	if affected > 0 {
		// Negate first so the UNIQUE(session_id, position) constraint cannot
		// collide while adjacent positions shift down by one.
		if _, err := tx.Exec(`UPDATE held_user_work SET position = -position - 1 WHERE session_id = ? AND position > ?`, sessionID, position); err != nil {
			return false, fmt.Errorf("stage held user work position compaction: %w", err)
		}
		if _, err := tx.Exec(`UPDATE held_user_work SET position = -position - 2 WHERE session_id = ? AND position < 0`, sessionID); err != nil {
			return false, fmt.Errorf("compact held user work positions: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit held user work deletion: %w", err)
	}
	return affected > 0, nil
}

// ErrHeldUserWorkSettled prevents an old client submission from becoming new
// work after cancellation or admission, including after history is compacted.
var ErrHeldUserWorkSettled = errors.New("accepted user input was already consumed or cancelled")

// AcceptHeldUserWork records a human submission before acknowledging it. A
// duplicate stable ID never replaces the original payload or starts it again.
func AcceptHeldUserWork(sessDir, sessionID string, entry HeldUserWork) (bool, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return false, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	if err := validateHeldUserWork(entry); err != nil {
		return false, err
	}
	var state, origin string
	err = tx.QueryRow(`SELECT state, origin FROM held_user_work WHERE session_id = ? AND id = ?`, sessionID, entry.ID).Scan(&state, &origin)
	if err == nil {
		if state == "cancelled" && origin != entry.Origin {
			// Queue and steer cancellation address distinct delivery modes. An
			// explicit change of mode supersedes only the old mode's receipt.
			if _, err := tx.Exec(`UPDATE held_user_work SET state = 'pending', origin = ?, message_json = ?, runtime_json = ? WHERE session_id = ? AND id = ?`, entry.Origin, string(entry.MessageJSON), string(entry.RuntimeJSON), sessionID, entry.ID); err != nil {
				return false, err
			}
			if err := tx.Commit(); err != nil {
				return false, err
			}
			return true, nil
		}
		if state != "pending" {
			return false, ErrHeldUserWorkSettled
		}
		return false, nil
	}
	if !errors.Is(err, sql.ErrNoRows) {
		return false, err
	}
	var delivered bool
	if err := tx.QueryRow(`SELECT EXISTS(SELECT 1 FROM session_messages WHERE session_id = ? AND role = 'user' AND client_id = ?)`, sessionID, entry.ID).Scan(&delivered); err != nil {
		return false, err
	}
	if delivered {
		return false, ErrHeldUserWorkSettled
	}
	if _, err := tx.Exec(`INSERT INTO held_user_work(session_id, position, id, origin, message_json, runtime_json)
SELECT ?, COALESCE(MAX(position), -1) + 1, ?, ?, ?, ? FROM held_user_work WHERE session_id = ?`, sessionID, entry.ID, entry.Origin, string(entry.MessageJSON), string(entry.RuntimeJSON), sessionID); err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return true, nil
}

func validateHeldUserWork(entry HeldUserWork) error {
	if strings.TrimSpace(entry.ID) == "" || len(entry.MessageJSON) == 0 || len(entry.RuntimeJSON) == 0 {
		return errors.New("held user work requires id, message and runtime")
	}
	if entry.Origin != HeldUserWorkOriginQueue && entry.Origin != HeldUserWorkOriginSteer {
		return errors.New("invalid held user work origin")
	}
	return nil
}

// UpdateHeldUserWork preserves queue order and compares the complete accepted
// payload so a stale edit or mode transition cannot overwrite another process.
func UpdateHeldUserWork(sessDir, sessionID string, entry, expected HeldUserWork) (bool, error) {
	if err := validateHeldUserWork(entry); err != nil {
		return false, err
	}
	db, err := openStore(sessDir)
	if err != nil {
		return false, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	result, err := db.Exec(`UPDATE held_user_work SET origin = ?, message_json = ?, runtime_json = ? WHERE session_id = ? AND id = ? AND state = 'pending' AND origin = ? AND message_json = ? AND runtime_json = ?`, entry.Origin, string(entry.MessageJSON), string(entry.RuntimeJSON), sessionID, entry.ID, expected.Origin, string(expected.MessageJSON), string(expected.RuntimeJSON))
	if err != nil {
		return false, err
	}
	n, err := result.RowsAffected()
	return n > 0, err
}

// CancelHeldUserWork retains a payload-free receipt, including cancellation
// arriving before acceptance. A stale queue cancellation cannot withdraw a steer.
func CancelHeldUserWork(sessDir, sessionID, id, origin string) (bool, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return false, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return false, err
	}
	defer tx.Rollback()
	var state, storedOrigin string
	err = tx.QueryRow(`SELECT state, origin FROM held_user_work WHERE session_id = ? AND id = ?`, sessionID, id).Scan(&state, &storedOrigin)
	removed := err == nil && state == "pending" && (origin == "" || origin == storedOrigin)
	if err == nil {
		if removed {
			_, err = tx.Exec(`UPDATE held_user_work SET state = 'cancelled', message_json = '', runtime_json = '' WHERE session_id = ? AND id = ?`, sessionID, id)
		}
	} else if errors.Is(err, sql.ErrNoRows) {
		_, err = tx.Exec(`INSERT INTO held_user_work(session_id, position, id, origin, message_json, runtime_json, state)
SELECT ?, COALESCE(MAX(position), -1) + 1, ?, ?, '', '', 'cancelled' FROM held_user_work WHERE session_id = ?`, sessionID, id, origin, sessionID)
	}
	if err != nil {
		return false, err
	}
	if err := tx.Commit(); err != nil {
		return false, err
	}
	return removed, nil
}

// Settlement and the user history append share one SQLite transaction. A crash
// exposes either recoverable held input or admitted history, never both. Tool
// effects are not replayed, and assistant/model-generated messages cannot settle
// human input merely by carrying a matching ID.
func consumeHeldUserWorkTx(tx *sql.Tx, sessionID string, rec HistoryRecord) error {
	id := rec.ClientID
	var state, messageJSON string
	err := tx.QueryRow(`SELECT state, message_json FROM held_user_work WHERE session_id = ? AND id = ?`, sessionID, id).Scan(&state, &messageJSON)
	if errors.Is(err, sql.ErrNoRows) {
		return nil
	}
	if err != nil {
		return err
	}
	if state != "pending" || rec.Hidden || rec.ReadOnly || (rec.Origin != "" && rec.Origin != "user") {
		return ErrHeldUserWorkSettled
	}
	if len(rec.ExpectedHeldMessage) == 0 || string(rec.ExpectedHeldMessage) != messageJSON {
		return errors.New("accepted steering changed before dispatch; review the held message")
	}
	_, err = tx.Exec(`UPDATE held_user_work SET state = 'consumed', message_json = '', runtime_json = '' WHERE session_id = ? AND id = ?`, sessionID, id)
	return err
}

// HasHeldUserWorkReceipt includes terminal receipts so changing RPC methods
// cannot turn a cancelled or already-dispatched client ID into a new request.
func HasHeldUserWorkReceipt(sessDir, sessionID, id string) (bool, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return false, err
	}
	defer db.Close()
	var found bool
	err = db.QueryRow(`SELECT EXISTS(SELECT 1 FROM held_user_work WHERE session_id = ? AND id = ?)`, sessionID, id).Scan(&found)
	return found, err
}
