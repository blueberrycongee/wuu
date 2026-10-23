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

func PutContextSeed(sessDir string, seed ContextSeed) (ContextSeed, error) {
	if err := ValidateContextSeed(seed); err != nil {
		return ContextSeed{}, err
	}
	db, err := openStore(sessDir)
	if err != nil {
		return ContextSeed{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return ContextSeed{}, fmt.Errorf("begin context seed write: %w", err)
	}
	defer tx.Rollback()
	written, err := putContextSeedTx(tx, seed)
	if err != nil {
		return ContextSeed{}, err
	}
	if err := tx.Commit(); err != nil {
		return ContextSeed{}, fmt.Errorf("commit context seed write: %w", err)
	}
	return written, nil
}

func LoadContextSeed(sessDir, id string) (ContextSeed, bool, error) {
	id = strings.TrimSpace(id)
	if id == "" {
		return ContextSeed{}, false, nil
	}
	db, err := openStore(sessDir)
	if err != nil {
		return ContextSeed{}, false, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return ContextSeed{}, false, fmt.Errorf("begin context seed load: %w", err)
	}
	defer tx.Rollback()
	seed, ok, err := loadContextSeedTx(tx, id)
	if err != nil {
		return ContextSeed{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return ContextSeed{}, false, fmt.Errorf("commit context seed load: %w", err)
	}
	return seed, ok, nil
}

func PutSessionLaunch(sessDir string, record SessionLaunchRecord) (SessionLaunchRecord, error) {
	db, err := openStore(sessDir)
	if err != nil {
		return SessionLaunchRecord{}, err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	tx, err := db.Begin()
	if err != nil {
		return SessionLaunchRecord{}, fmt.Errorf("begin session launch write: %w", err)
	}
	defer tx.Rollback()
	written, err := putSessionLaunchTx(tx, record)
	if err != nil {
		return SessionLaunchRecord{}, err
	}
	if err := tx.Commit(); err != nil {
		return SessionLaunchRecord{}, fmt.Errorf("commit session launch write: %w", err)
	}
	return written, nil
}

func LoadSessionLaunch(sessDir, requestID string) (SessionLaunchRecord, bool, error) {
	requestID = strings.TrimSpace(requestID)
	if requestID == "" {
		return SessionLaunchRecord{}, false, nil
	}
	db, err := openStore(sessDir)
	if err != nil {
		return SessionLaunchRecord{}, false, err
	}
	defer db.Close()
	tx, err := db.Begin()
	if err != nil {
		return SessionLaunchRecord{}, false, fmt.Errorf("begin session launch load: %w", err)
	}
	defer tx.Rollback()
	record, ok, err := loadSessionLaunchTx(tx, requestID)
	if err != nil {
		return SessionLaunchRecord{}, false, err
	}
	if err := tx.Commit(); err != nil {
		return SessionLaunchRecord{}, false, fmt.Errorf("commit session launch load: %w", err)
	}
	return record, ok, nil
}

func putContextSeedTx(tx *sql.Tx, seed ContextSeed) (ContextSeed, error) {
	if strings.TrimSpace(seed.ID) == "" {
		seed.ID = NewID()
	}
	if strings.TrimSpace(seed.Provenance.CreatedAt) == "" {
		seed.Provenance.CreatedAt = time.Now().UTC().Format(time.RFC3339Nano)
	}
	if err := ValidateContextSeed(seed); err != nil {
		return ContextSeed{}, err
	}
	ok, err := sessionExistsTx(tx, seed.Source.SessionID)
	if err != nil {
		return ContextSeed{}, err
	}
	if !ok {
		return ContextSeed{}, fmt.Errorf("%w: %q", ErrSessionNotFound, seed.Source.SessionID)
	}
	var headSeq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM session_messages WHERE session_id = ?`, seed.Source.SessionID).Scan(&headSeq); err != nil {
		return ContextSeed{}, fmt.Errorf("load seed source head: %w", err)
	}
	if seed.Source.ThroughSeq > headSeq {
		return ContextSeed{}, fmt.Errorf("%w: source cutoff %d is beyond head %d", ErrHistorySnapshotGone, seed.Source.ThroughSeq, headSeq)
	}
	referencesJSON, err := json.Marshal(seed.References)
	if err != nil {
		return ContextSeed{}, fmt.Errorf("encode seed references: %w", err)
	}
	artifactsJSON, err := json.Marshal(seed.Artifacts)
	if err != nil {
		return ContextSeed{}, fmt.Errorf("encode seed artifacts: %w", err)
	}
	provenanceJSON, err := json.Marshal(seed.Provenance)
	if err != nil {
		return ContextSeed{}, fmt.Errorf("encode seed provenance: %w", err)
	}
	if _, err := tx.Exec(`
INSERT INTO context_seeds (
	id, version, body, source_session_id, source_through_seq, references_json, artifacts_json, provenance_json, created_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(id) DO NOTHING`,
		seed.ID, seed.Version, seed.Body, seed.Source.SessionID, seed.Source.ThroughSeq, string(referencesJSON), string(artifactsJSON), string(provenanceJSON), seed.Provenance.CreatedAt,
	); err != nil {
		return ContextSeed{}, fmt.Errorf("insert context seed: %w", err)
	}
	existing, ok, err := loadContextSeedTx(tx, seed.ID)
	if err != nil {
		return ContextSeed{}, err
	}
	if !ok {
		return ContextSeed{}, errors.New("context seed insert did not persist")
	}
	if existing.Body != seed.Body || existing.Source != seed.Source {
		return ContextSeed{}, fmt.Errorf("%w: seed %q already exists with different content", ErrSessionLaunchConflict, seed.ID)
	}
	return existing, nil
}

func loadContextSeedTx(tx *sql.Tx, id string) (ContextSeed, bool, error) {
	var seed ContextSeed
	var referencesJSON, artifactsJSON, provenanceJSON string
	err := tx.QueryRow(`
SELECT id, version, body, source_session_id, source_through_seq, references_json, artifacts_json, provenance_json
FROM context_seeds WHERE id = ?`, id).Scan(
		&seed.ID, &seed.Version, &seed.Body, &seed.Source.SessionID, &seed.Source.ThroughSeq, &referencesJSON, &artifactsJSON, &provenanceJSON,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return ContextSeed{}, false, nil
	}
	if err != nil {
		return ContextSeed{}, false, fmt.Errorf("load context seed: %w", err)
	}
	if referencesJSON != "" {
		if err := json.Unmarshal([]byte(referencesJSON), &seed.References); err != nil {
			return ContextSeed{}, false, fmt.Errorf("decode seed references: %w", err)
		}
	}
	if artifactsJSON != "" {
		if err := json.Unmarshal([]byte(artifactsJSON), &seed.Artifacts); err != nil {
			return ContextSeed{}, false, fmt.Errorf("decode seed artifacts: %w", err)
		}
	}
	if provenanceJSON != "" {
		if err := json.Unmarshal([]byte(provenanceJSON), &seed.Provenance); err != nil {
			return ContextSeed{}, false, fmt.Errorf("decode seed provenance: %w", err)
		}
	}
	return seed, true, nil
}

func putSessionLaunchTx(tx *sql.Tx, record SessionLaunchRecord) (SessionLaunchRecord, error) {
	record.RequestID = strings.TrimSpace(record.RequestID)
	if record.RequestID == "" {
		return SessionLaunchRecord{}, errors.New("launch request_id is required")
	}
	if record.Revision < 1 {
		record.Revision = 1
	}
	if strings.TrimSpace(record.Kind) == "" {
		record.Kind = SessionLaunchKindHandoff
	}
	if strings.TrimSpace(record.Status) == "" {
		record.Status = SessionLaunchDraft
	}
	now := time.Now().UTC()
	if record.CreatedAt.IsZero() {
		record.CreatedAt = now
	}
	record.UpdatedAt = now
	existing, ok, err := loadSessionLaunchTx(tx, record.RequestID)
	if err != nil {
		return SessionLaunchRecord{}, err
	}
	if ok {
		if existing.Revision != record.Revision || !existing.Runtime.equal(record.Runtime) || existing.SourceSession != record.SourceSession || existing.SourceCutoff != record.SourceCutoff {
			return SessionLaunchRecord{}, fmt.Errorf("%w: request %q", ErrSessionLaunchConflict, record.RequestID)
		}
		if existing.Status == SessionLaunchCommitted && record.Status != SessionLaunchCommitted {
			return existing, nil
		}
	}
	runtimeJSON, err := json.Marshal(record.Runtime)
	if err != nil {
		return SessionLaunchRecord{}, fmt.Errorf("encode launch runtime: %w", err)
	}
	inputJSON, err := json.Marshal(record.Input)
	if err != nil {
		return SessionLaunchRecord{}, fmt.Errorf("encode launch input: %w", err)
	}
	committedAt := sql.NullString{}
	if record.CommittedAt != nil {
		committedAt = sql.NullString{String: record.CommittedAt.UTC().Format(time.RFC3339Nano), Valid: true}
	} else if record.Status == SessionLaunchCommitted {
		nowText := now.Format(time.RFC3339Nano)
		committedAt = sql.NullString{String: nowText, Valid: true}
		committed := now
		record.CommittedAt = &committed
	}
	if _, err := tx.Exec(`
INSERT INTO session_launches (
	request_id, revision, kind, status, source_session_id, source_cutoff_seq, seed_id, target_session_id, initial_turn_id,
	owner, producer, runtime_json, input_json, error, created_at, updated_at, committed_at
) VALUES (?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)
ON CONFLICT(request_id) DO UPDATE SET
	status = excluded.status,
	seed_id = excluded.seed_id,
	target_session_id = excluded.target_session_id,
	initial_turn_id = excluded.initial_turn_id,
	error = excluded.error,
	updated_at = excluded.updated_at,
	committed_at = COALESCE(session_launches.committed_at, excluded.committed_at)`,
		record.RequestID, record.Revision, record.Kind, record.Status, record.SourceSession, record.SourceCutoff, record.SeedID, record.TargetSession, record.InitialTurnID,
		record.Owner, record.Producer, string(runtimeJSON), string(inputJSON), record.Error, record.CreatedAt.UTC().Format(time.RFC3339Nano), record.UpdatedAt.UTC().Format(time.RFC3339Nano), committedAt,
	); err != nil {
		return SessionLaunchRecord{}, fmt.Errorf("upsert session launch: %w", err)
	}
	written, ok, err := loadSessionLaunchTx(tx, record.RequestID)
	if err != nil {
		return SessionLaunchRecord{}, err
	}
	if !ok {
		return SessionLaunchRecord{}, errors.New("session launch did not persist")
	}
	return written, nil
}

func loadSessionLaunchTx(tx *sql.Tx, requestID string) (SessionLaunchRecord, bool, error) {
	var record SessionLaunchRecord
	var runtimeJSON, inputJSON, createdAt, updatedAt string
	var committedAt sql.NullString
	err := tx.QueryRow(`
SELECT request_id, revision, kind, status, source_session_id, source_cutoff_seq, seed_id, target_session_id, initial_turn_id,
	owner, producer, runtime_json, input_json, error, created_at, updated_at, committed_at
FROM session_launches WHERE request_id = ?`, requestID).Scan(
		&record.RequestID, &record.Revision, &record.Kind, &record.Status, &record.SourceSession, &record.SourceCutoff, &record.SeedID, &record.TargetSession, &record.InitialTurnID,
		&record.Owner, &record.Producer, &runtimeJSON, &inputJSON, &record.Error, &createdAt, &updatedAt, &committedAt,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return SessionLaunchRecord{}, false, nil
	}
	if err != nil {
		return SessionLaunchRecord{}, false, fmt.Errorf("load session launch: %w", err)
	}
	if runtimeJSON != "" {
		if err := json.Unmarshal([]byte(runtimeJSON), &record.Runtime); err != nil {
			return SessionLaunchRecord{}, false, fmt.Errorf("decode launch runtime: %w", err)
		}
	}
	if inputJSON != "" {
		if err := json.Unmarshal([]byte(inputJSON), &record.Input); err != nil {
			return SessionLaunchRecord{}, false, fmt.Errorf("decode launch input: %w", err)
		}
	}
	if parsed, err := parseLaunchTime(createdAt); err == nil {
		record.CreatedAt = parsed
	}
	if parsed, err := parseLaunchTime(updatedAt); err == nil {
		record.UpdatedAt = parsed
	}
	if committedAt.Valid {
		if parsed, err := parseLaunchTime(committedAt.String); err == nil {
			record.CommittedAt = &parsed
		}
	}
	return record, true, nil
}
