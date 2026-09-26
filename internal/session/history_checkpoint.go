package session

import (
	"context"
	"database/sql"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"
)

const HistoryCheckpointKindProviderRewrite = "provider_rewrite"

// HistoryCheckpoint is the current provider-history replacement. ThroughSeq is
// the physical message head covered by Replacement. Versions remain monotonic,
// but superseded payloads are discarded because no recovery path reads them.
type HistoryCheckpoint struct {
	SessionID   string          `json:"session_id"`
	Version     int             `json:"version"`
	Kind        string          `json:"kind"`
	ThroughSeq  int             `json:"through_seq"`
	Replacement []HistoryRecord `json:"replacement"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ProviderHistorySnapshot is the latest exact replacement followed by any
// physical messages committed after that checkpoint. Checkpoint is nil for a
// legacy session that has never stored one.
type ProviderHistorySnapshot struct {
	Records    []HistoryRecord
	HeadSeq    int
	Checkpoint *HistoryCheckpoint
}

// RewriteHistoryRecordsAtBaseline stores a provider-history checkpoint. The
// durable transcript is never deleted, renumbered, or reused.
func RewriteHistoryRecordsAtBaseline(sessDir, id string, records []HistoryRecord, baselineSeq int) error {
	_, err := StoreHistoryCheckpointAtBaseline(sessDir, id, HistoryCheckpointKindProviderRewrite, records, baselineSeq)
	return err
}

// RewriteHistoryRecordsForEdit atomically replaces provider context and retracts
// the edited suffix from the active transcript. Physical audit rows and messages
// committed after baselineSeq remain intact, including pre-compaction history.
func RewriteHistoryRecordsForEdit(sessDir, id string, records []HistoryRecord, fromSeq, baselineSeq int) error {
	if fromSeq <= 0 || fromSeq > baselineSeq {
		return fmt.Errorf("invalid history edit range %d..%d", fromSeq, baselineSeq)
	}
	_, err := storeHistoryCheckpointAtBaseline(sessDir, id, HistoryCheckpointKindProviderRewrite, records, baselineSeq, nil, fromSeq)
	return err
}

// StoreHistoryCheckpointAtBaseline atomically stores a replacement for history
// read through baselineSeq. Messages already committed after the baseline are
// appended to the exact replacement inside the same write transaction.
func StoreHistoryCheckpointAtBaseline(sessDir, id, kind string, replacement []HistoryRecord, baselineSeq int) (HistoryCheckpoint, error) {
	return storeHistoryCheckpointAtBaseline(sessDir, id, kind, replacement, baselineSeq, nil, 0)
}

// StoreContextWindow commits the provider checkpoint and its note anchor in one
// transaction. An empty note is a tombstone that invalidates older fork writes.
func StoreContextWindow(sessDir, id string, replacement []HistoryRecord, baselineSeq int, note CompactionNote) (HistoryCheckpoint, error) {
	if strings.TrimSpace(note.ProviderKey) == "" {
		return HistoryCheckpoint{}, errors.New("context window requires a note provider key")
	}
	return storeHistoryCheckpointAtBaseline(sessDir, id, "context_window", replacement, baselineSeq, &note, 0)
}

func storeHistoryCheckpointAtBaseline(sessDir, id, kind string, replacement []HistoryRecord, baselineSeq int, note *CompactionNote, retractFromSeq int) (HistoryCheckpoint, error) {
	id = strings.TrimSpace(id)
	kind = strings.TrimSpace(kind)
	if baselineSeq < 0 {
		return HistoryCheckpoint{}, fmt.Errorf("history checkpoint baseline must be non-negative: %d", baselineSeq)
	}
	if kind == "" {
		return HistoryCheckpoint{}, errors.New("history checkpoint kind is required")
	}

	db, err := openStore(sessDir)
	if err != nil {
		return HistoryCheckpoint{}, err
	}
	defer db.Close()

	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()

	// sqliteDSN configures BEGIN IMMEDIATE. The database write lock makes the
	// head check, tail capture, sequence allocation, and checkpoint insert one
	// cross-process serialization point.
	tx, err := db.Begin()
	if err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("begin history checkpoint: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, id); err != nil {
		return HistoryCheckpoint{}, err
	} else if !ok {
		return HistoryCheckpoint{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}

	var headSeq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM session_messages WHERE session_id = ?`, id).Scan(&headSeq); err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("load history head for checkpoint: %w", err)
	}
	if baselineSeq > headSeq {
		return HistoryCheckpoint{}, fmt.Errorf("history checkpoint baseline %d exceeds head %d", baselineSeq, headSeq)
	}

	records := make([]HistoryRecord, len(replacement))
	copy(records, replacement)
	seenSeqs := make(map[int]struct{}, len(records))
	for _, rec := range records {
		if rec.Seq < 0 {
			return HistoryCheckpoint{}, fmt.Errorf("history checkpoint record has negative seq %d", rec.Seq)
		}
		if rec.Seq == 0 {
			continue
		}
		if rec.Seq > headSeq {
			return HistoryCheckpoint{}, fmt.Errorf("history checkpoint record seq %d exceeds head %d", rec.Seq, headSeq)
		}
		seenSeqs[rec.Seq] = struct{}{}
	}

	capturedTail, err := loadHistoryRecordsRangeTx(tx, id, baselineSeq, headSeq)
	if err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("capture history checkpoint tail: %w", err)
	}
	if len(seenSeqs) > 0 {
		unreferenced := capturedTail[:0]
		for _, rec := range capturedTail {
			if _, exists := seenSeqs[rec.Seq]; !exists {
				unreferenced = append(unreferenced, rec)
			}
		}
		capturedTail = unreferenced
	}
	for i := range records {
		if records[i].Seq != 0 {
			continue
		}
		headSeq++
		records[i].Seq = headSeq
		if err := insertHistoryRecordTx(tx, id, headSeq, records[i]); err != nil {
			return HistoryCheckpoint{}, fmt.Errorf("append checkpoint record at seq %d: %w", headSeq, err)
		}
	}
	records = append(records, capturedTail...)

	storedRecords := make([]HistoryRecord, len(records))
	for i, record := range records {
		storedRecords[i] = compactHistoryRecordForStorage(record)
	}
	payload, err := json.Marshal(storedRecords)
	if err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("encode history checkpoint replacement: %w", err)
	}
	var version int
	if err := tx.QueryRow(`
		SELECT COALESCE(MAX(version), 0) + 1
		FROM session_history_checkpoints
		WHERE session_id = ?`, id).Scan(&version); err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("allocate history checkpoint version: %w", err)
	}
	createdAt := time.Now().UTC()
	if _, err := tx.Exec(`
		INSERT INTO session_history_checkpoints (
			session_id, version, kind, through_seq, replacement_json, created_at
		) VALUES (?, ?, ?, ?, ?, ?)`,
		id, version, kind, headSeq, string(payload), timeText(createdAt),
	); err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("store history checkpoint: %w", err)
	}
	if _, err := tx.Exec(`
		DELETE FROM session_history_checkpoints
		WHERE session_id = ? AND version < ?`, id, version); err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("remove superseded history checkpoint: %w", err)
	}
	if retractFromSeq > 0 {
		if _, err := tx.Exec(`INSERT INTO session_history_retractions (session_id, from_seq, through_seq)
			VALUES (?, ?, ?)`, id, retractFromSeq, baselineSeq); err != nil {
			return HistoryCheckpoint{}, fmt.Errorf("retract edited history: %w", err)
		}
	}

	if note != nil {
		anchor := note.CoveredHash
		if strings.TrimSpace(note.Markdown) == "" {
			anchor = fmt.Sprintf("checkpoint:%d", version)
		}
		if _, err := tx.Exec(`INSERT INTO session_compaction_notes
			(session_id, provider_key, markdown, covered_messages, covered_hash, updated_at)
			VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, provider_key) DO UPDATE SET
			markdown=excluded.markdown, covered_messages=excluded.covered_messages,
			covered_hash=excluded.covered_hash, updated_at=excluded.updated_at`,
			id, note.ProviderKey, note.Markdown, note.CoveredMessages, anchor, timeText(createdAt)); err != nil {
			return HistoryCheckpoint{}, fmt.Errorf("commit context note anchor: %w", err)
		}
	}
	if err := tx.Commit(); err != nil {
		return HistoryCheckpoint{}, fmt.Errorf("commit history checkpoint: %w", err)
	}
	return HistoryCheckpoint{
		SessionID:   id,
		Version:     version,
		Kind:        kind,
		ThroughSeq:  headSeq,
		Replacement: records,
		CreatedAt:   createdAt,
	}, nil
}

// LoadProviderHistorySnapshot reconstructs the provider history from the
// latest checkpoint and the physical tail. Legacy sessions return their raw
// append-only history unchanged.
func LoadProviderHistorySnapshot(sessDir, id string) (ProviderHistorySnapshot, error) {
	id = strings.TrimSpace(id)
	db, err := openStore(sessDir)
	if err != nil {
		return ProviderHistorySnapshot{}, err
	}
	defer db.Close()
	return LoadProviderHistorySnapshotFromStore(db, id)
}

// LoadProviderHistorySnapshotFromStore reads a consistent provider history using
// an existing store. Batch readers can reuse a connection without repeating
// database configuration and schema migration for every session.
func LoadProviderHistorySnapshotFromStore(db *sql.DB, id string) (ProviderHistorySnapshot, error) {
	id = strings.TrimSpace(id)
	tx, err := db.BeginTx(context.Background(), &sql.TxOptions{ReadOnly: true})
	if err != nil {
		return ProviderHistorySnapshot{}, fmt.Errorf("begin provider history snapshot: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, id); err != nil {
		return ProviderHistorySnapshot{}, err
	} else if !ok {
		return ProviderHistorySnapshot{}, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}

	var headSeq int
	if err := tx.QueryRow(`SELECT COALESCE(MAX(seq), 0) FROM session_messages WHERE session_id = ?`, id).Scan(&headSeq); err != nil {
		return ProviderHistorySnapshot{}, fmt.Errorf("load provider history head: %w", err)
	}
	checkpoint, ok, err := latestHistoryCheckpointTx(tx, id)
	if err != nil {
		return ProviderHistorySnapshot{}, err
	}
	if !ok {
		records, err := loadHistoryRecordsRangeTx(tx, id, 0, headSeq)
		if err != nil {
			return ProviderHistorySnapshot{}, fmt.Errorf("load legacy provider history: %w", err)
		}
		if err := tx.Commit(); err != nil {
			return ProviderHistorySnapshot{}, fmt.Errorf("commit provider history snapshot: %w", err)
		}
		return ProviderHistorySnapshot{Records: records, HeadSeq: headSeq}, nil
	}

	tail, err := loadHistoryRecordsRangeTx(tx, id, checkpoint.ThroughSeq, headSeq)
	if err != nil {
		return ProviderHistorySnapshot{}, fmt.Errorf("load provider history checkpoint tail: %w", err)
	}
	records := make([]HistoryRecord, 0, len(checkpoint.Replacement)+len(tail))
	records = append(records, checkpoint.Replacement...)
	records = append(records, tail...)
	if err := tx.Commit(); err != nil {
		return ProviderHistorySnapshot{}, fmt.Errorf("commit provider history snapshot: %w", err)
	}
	return ProviderHistorySnapshot{Records: records, HeadSeq: headSeq, Checkpoint: &checkpoint}, nil
}

// LatestHistoryCheckpoint returns the latest checkpoint without reconstructing
// its physical tail.
func LatestHistoryCheckpoint(sessDir, id string) (HistoryCheckpoint, bool, error) {
	id = strings.TrimSpace(id)
	db, err := openStore(sessDir)
	if err != nil {
		return HistoryCheckpoint{}, false, err
	}
	defer db.Close()
	if ok, err := sessionExistsDB(db, id); err != nil {
		return HistoryCheckpoint{}, false, err
	} else if !ok {
		return HistoryCheckpoint{}, false, fmt.Errorf("%w: %q", ErrSessionNotFound, id)
	}
	return latestHistoryCheckpointDB(db, id)
}

type historyCheckpointScanner interface {
	Scan(dest ...any) error
}

func latestHistoryCheckpointDB(db *sql.DB, id string) (HistoryCheckpoint, bool, error) {
	return scanHistoryCheckpoint(db.QueryRow(`
		SELECT version, kind, through_seq, replacement_json, created_at
		FROM session_history_checkpoints
		WHERE session_id = ?
		ORDER BY version DESC
		LIMIT 1`, id), id)
}

func latestHistoryCheckpointTx(tx *sql.Tx, id string) (HistoryCheckpoint, bool, error) {
	return scanHistoryCheckpoint(tx.QueryRow(`
		SELECT version, kind, through_seq, replacement_json, created_at
		FROM session_history_checkpoints
		WHERE session_id = ?
		ORDER BY version DESC
		LIMIT 1`, id), id)
}

func scanHistoryCheckpoint(row historyCheckpointScanner, id string) (HistoryCheckpoint, bool, error) {
	var checkpoint HistoryCheckpoint
	var payload, createdAt string
	if err := row.Scan(&checkpoint.Version, &checkpoint.Kind, &checkpoint.ThroughSeq, &payload, &createdAt); errors.Is(err, sql.ErrNoRows) {
		return HistoryCheckpoint{}, false, nil
	} else if err != nil {
		return HistoryCheckpoint{}, false, fmt.Errorf("load latest history checkpoint: %w", err)
	}
	if err := json.Unmarshal([]byte(payload), &checkpoint.Replacement); err != nil {
		return HistoryCheckpoint{}, false, fmt.Errorf("decode history checkpoint %s@%d: %w", id, checkpoint.Version, err)
	}
	for i := range checkpoint.Replacement {
		hydrateHistoryRecordFromStorage(&checkpoint.Replacement[i])
	}
	checkpoint.SessionID = id
	checkpoint.CreatedAt = parseTime(createdAt)
	return checkpoint, true, nil
}

func loadHistoryRecordsRangeTx(tx *sql.Tx, id string, afterSeq, throughSeq int) ([]HistoryRecord, error) {
	if throughSeq <= afterSeq {
		return nil, nil
	}
	rows, err := tx.Query(historyRecordsSelect+`
		WHERE session_id = ? AND seq > ? AND seq <= ?
		ORDER BY seq ASC`, id, afterSeq, throughSeq)
	if err != nil {
		return nil, err
	}
	return scanHistoryRecords(rows)
}
