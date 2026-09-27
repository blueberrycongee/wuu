package session

import (
	"database/sql"
	"errors"
	"fmt"
)

// Control is an execution fence, independent of session ownership and ancestry.
// Revision changes invalidate previously admitted automatic instructions.
type Control struct {
	SessionID string `json:"session_id"`
	ManagerID string `json:"manager_id"`
	Revision  int64  `json:"revision"`
	State     string `json:"state"`
}

const (
	ControlActive    = "active"
	ControlPaused    = "paused"
	ControlTakenOver = "taken_over"
	ControlReleased  = "released"
)

var ErrControlChanged = errors.New("session control changed; inspect before continuing")

func ReadControl(dir, id string) (Control, bool, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return Control{}, false, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_controls"); err != nil || !exists {
		return Control{}, false, err
	}
	var c Control
	err = db.QueryRow(`SELECT session_id,manager_id,revision,state FROM session_controls WHERE session_id=?`, id).Scan(&c.SessionID, &c.ManagerID, &c.Revision, &c.State)
	if errors.Is(err, sql.ErrNoRows) {
		return c, false, nil
	}
	return c, err == nil, err
}

// ListControls reads the persisted execution fences with one store open, so
// cross-workspace conversation lists do not need a database lookup per session.
func ListControls(dir string) (map[string]Control, error) {
	db, ok, err := openStoreForScan(dir)
	if err != nil || !ok {
		return nil, err
	}
	defer db.Close()
	if exists, err := storeTableExists(db, "session_controls"); err != nil || !exists {
		return nil, err
	}
	rows, err := db.Query(`SELECT session_id,manager_id,revision,state FROM session_controls`)
	if err != nil {
		return nil, err
	}
	defer rows.Close()
	controls := make(map[string]Control)
	for rows.Next() {
		var c Control
		if err := rows.Scan(&c.SessionID, &c.ManagerID, &c.Revision, &c.State); err != nil {
			return nil, err
		}
		controls[c.SessionID] = c
	}
	return controls, rows.Err()
}

// ChangeControl uses compare-and-swap so a stale process cannot undo a human
// takeover. A released relationship retains its revision to fence old work.
func ChangeControl(dir, id, manager, state string, expected int64) (Control, error) {
	if id == "" || manager == "" {
		return Control{}, errors.New("session and manager are required")
	}
	switch state {
	case ControlActive, ControlPaused, ControlTakenOver, ControlReleased:
	default:
		return Control{}, errors.New("invalid control state")
	}
	db, err := openStore(dir)
	if err != nil {
		return Control{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return Control{}, err
	}
	defer tx.Rollback()
	var old Control
	err = tx.QueryRow(`SELECT session_id,manager_id,revision,state FROM session_controls WHERE session_id=?`, id).Scan(&old.SessionID, &old.ManagerID, &old.Revision, &old.State)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return Control{}, err
	}
	if old.Revision != expected || old.ManagerID != "" && old.ManagerID != manager && old.State != ControlReleased {
		return Control{}, ErrControlChanged
	}
	c := Control{SessionID: id, ManagerID: manager, Revision: expected + 1, State: state}
	_, err = tx.Exec(`INSERT INTO session_controls(session_id,manager_id,revision,state) VALUES(?,?,?,?) ON CONFLICT(session_id) DO UPDATE SET manager_id=excluded.manager_id,revision=excluded.revision,state=excluded.state`, id, manager, c.Revision, state)
	if err != nil {
		return Control{}, err
	}
	return c, tx.Commit()
}

func ValidateControl(dir string, expected Control) error {
	c, ok, err := ReadControl(dir, expected.SessionID)
	if err != nil {
		return err
	}
	if !ok || c.ManagerID != expected.ManagerID || c.Revision != expected.Revision || c.State != ControlActive {
		return fmt.Errorf("%w: automatic execution is no longer authorized", ErrControlChanged)
	}
	return nil
}
