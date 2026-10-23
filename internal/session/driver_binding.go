package session

import (
	"context"
	"database/sql"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
)

// LoadDriverBinding returns the identity retained by this conversation. A
// missing binding on a new conversation is distinct from a missing session.
func LoadDriverBinding(sessDir, sessionID string) (pluginprotocol.DriverBinding, bool, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return pluginprotocol.DriverBinding{}, false, err
	}
	defer db.Close()
	binding, ok, err := loadDriverBinding(db, sessionID)
	if err == nil && !ok {
		var exists bool
		exists, err = sessionExistsDB(db, sessionID)
		if err == nil && !exists {
			err = fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
		}
	}
	return binding, ok, err
}

func loadDriverBinding(db interface {
	QueryRow(string, ...any) *sql.Row
}, sessionID string) (pluginprotocol.DriverBinding, bool, error) {
	var binding pluginprotocol.DriverBinding
	err := db.QueryRow(`SELECT profile, provider_id, contract_version, driver_id, driver_version
FROM session_driver_bindings WHERE session_id = ?`, sessionID).Scan(
		&binding.Profile, &binding.ProviderID, &binding.ContractVersion, &binding.DriverID, &binding.DriverVersion,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return binding, false, nil
	}
	return binding, err == nil, err
}

// BindDriver durably admits an implementation before it can create an instance
// or issue effects. The transaction compares both the existing binding and the
// latest checkpoint, so concurrent admissions cannot switch implementations.
// Cancellation after commit does not undo the durable binding.
func BindDriver(ctx context.Context, sessDir, sessionID string, binding pluginprotocol.DriverBinding) error {
	if err := ctx.Err(); err != nil {
		return err
	}
	if strings.TrimSpace(sessionID) == "" || strings.TrimSpace(binding.Profile) == "" || strings.TrimSpace(binding.ProviderID) == "" || strings.TrimSpace(binding.DriverID) == "" || strings.TrimSpace(binding.DriverVersion) == "" || binding.ContractVersion < 1 {
		return errors.New("complete loop driver binding is required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.BeginTx(ctx, nil)
	if err != nil {
		return err
	}
	defer tx.Rollback()
	if exists, err := sessionExistsTx(tx, sessionID); err != nil {
		return err
	} else if !exists {
		return fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	stored, ok, err := loadDriverBinding(tx, sessionID)
	if err != nil {
		return err
	}
	if ok && stored != binding {
		return fmt.Errorf("loop driver binding requires %+v; requested %+v", stored, binding)
	}
	var contract int
	var id, version string
	err = tx.QueryRow(`SELECT contract_version, driver_id, driver_version FROM session_driver_checkpoints WHERE session_id = ?`, sessionID).Scan(&contract, &id, &version)
	if err != nil && !errors.Is(err, sql.ErrNoRows) {
		return err
	}
	if err == nil && (contract != binding.ContractVersion || id != binding.DriverID || version != binding.DriverVersion) {
		return fmt.Errorf("loop driver checkpoint requires %s@%s contract %d; requested binding %+v", id, version, contract, binding)
	}
	if !ok {
		if _, err := tx.ExecContext(ctx, `INSERT INTO session_driver_bindings
(session_id, profile, provider_id, contract_version, driver_id, driver_version) VALUES (?, ?, ?, ?, ?, ?)`,
			sessionID, binding.Profile, binding.ProviderID, binding.ContractVersion, binding.DriverID, binding.DriverVersion); err != nil {
			return err
		}
	}
	return tx.Commit()
}

func migrateDriverBindings(db *sql.DB) error {
	tx, err := db.Begin()
	if err != nil {
		return err
	}
	defer tx.Rollback()
	var exists int
	if err := tx.QueryRow(`SELECT count(*) FROM sqlite_master WHERE type = 'table' AND name = 'session_driver_bindings'`).Scan(&exists); err != nil {
		return err
	}
	if exists != 0 {
		return nil
	}
	if _, err := tx.Exec(`CREATE TABLE session_driver_bindings (
    session_id TEXT PRIMARY KEY,
    profile TEXT NOT NULL,
    provider_id TEXT NOT NULL,
    contract_version INTEGER NOT NULL,
    driver_id TEXT NOT NULL,
    driver_version TEXT NOT NULL,
    FOREIGN KEY(session_id) REFERENCES sessions(id) ON DELETE CASCADE
)`); err != nil {
		return err
	}
	// These are historical identities, not current defaults. Old external
	// checkpoints did not record a provider or profile; leave those unknown
	// rather than inventing authority. Their original binding needs migration.
	if _, err := tx.Exec(`INSERT INTO session_driver_bindings
(session_id, profile, provider_id, contract_version, driver_id, driver_version)
SELECT s.id,
    CASE WHEN c.driver_id IS NULL OR c.driver_id = 'wuu.default-loop' THEN 'default' ELSE '' END,
    CASE WHEN c.driver_id IS NULL OR c.driver_id = 'wuu.default-loop' THEN 'wuu.default-loop' ELSE '' END,
    coalesce(c.contract_version, 1), coalesce(c.driver_id, 'wuu.default-loop'), coalesce(c.driver_version, '1.0.0')
FROM sessions s LEFT JOIN session_driver_checkpoints c ON c.session_id = s.id
WHERE s.engine_id IN ('', 'wuu')`); err != nil {
		return err
	}
	return tx.Commit()
}
