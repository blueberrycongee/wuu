package session

import (
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

type DriverCheckpointRecord struct {
	ContractVersion int
	DriverID        string
	DriverVersion   string
	State           json.RawMessage
	UpdatedAt       time.Time
}

func LoadDriverCheckpoint(sessDir, sessionID string) (DriverCheckpointRecord, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return DriverCheckpointRecord{}, false, errors.New("driver checkpoint session id is required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return DriverCheckpointRecord{}, false, err
	}
	defer db.Close()

	var record DriverCheckpointRecord
	var stateJSON, updatedAt string
	err = db.QueryRow(`
SELECT contract_version, driver_id, driver_version, state_json, updated_at
FROM session_driver_checkpoints
WHERE session_id = ?`, sessionID).Scan(
		&record.ContractVersion,
		&record.DriverID,
		&record.DriverVersion,
		&stateJSON,
		&updatedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		if ok, findErr := sessionExistsDB(db, sessionID); findErr != nil {
			return DriverCheckpointRecord{}, false, findErr
		} else if !ok {
			return DriverCheckpointRecord{}, false, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
		}
		return DriverCheckpointRecord{}, false, nil
	}
	if err != nil {
		return DriverCheckpointRecord{}, false, fmt.Errorf("load driver checkpoint: %w", err)
	}
	record.State = json.RawMessage(stateJSON)
	record.UpdatedAt = parseTime(updatedAt)
	return record, true, nil
}

func SaveDriverCheckpoint(sessDir, sessionID string, record DriverCheckpointRecord) error {
	sessionID = strings.TrimSpace(sessionID)
	record.DriverID = strings.TrimSpace(record.DriverID)
	record.DriverVersion = strings.TrimSpace(record.DriverVersion)
	if sessionID == "" || record.DriverID == "" || record.DriverVersion == "" || record.ContractVersion < 1 {
		return errors.New("complete driver checkpoint identity is required")
	}
	if len(record.State) == 0 || !json.Valid(record.State) {
		return errors.New("driver checkpoint state must be valid JSON")
	}
	if record.UpdatedAt.IsZero() {
		record.UpdatedAt = time.Now().UTC()
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
		return fmt.Errorf("begin driver checkpoint write: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, sessionID); err != nil {
		return err
	} else if !ok {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	binding, bound, err := loadDriverBinding(tx, sessionID)
	if err != nil {
		return err
	}
	if bound && (binding.ContractVersion != record.ContractVersion || binding.DriverID != record.DriverID || binding.DriverVersion != record.DriverVersion) {
		return fmt.Errorf("driver checkpoint does not match session binding %+v", binding)
	}
	if _, err := tx.Exec(`
INSERT INTO session_driver_checkpoints (
    session_id, contract_version, driver_id, driver_version, state_json, updated_at
) VALUES (?, ?, ?, ?, ?, ?)
ON CONFLICT(session_id) DO UPDATE SET
    contract_version = excluded.contract_version,
    driver_id = excluded.driver_id,
    driver_version = excluded.driver_version,
    state_json = excluded.state_json,
    updated_at = excluded.updated_at`,
		sessionID,
		record.ContractVersion,
		record.DriverID,
		record.DriverVersion,
		string(record.State),
		timeText(record.UpdatedAt),
	); err != nil {
		return fmt.Errorf("write driver checkpoint: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return fmt.Errorf("commit driver checkpoint: %w", err)
	}
	return nil
}
