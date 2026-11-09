// Legacy SQLite fixture code is test-only. Production sessions use Store;
// explicit conversion reads legacy databases through legacysqlite.
package session

import (
	"database/sql"
	"errors"
	"fmt"
	"strings"
	"time"
)

// LoadCompactionNote returns the latest document for one compaction provider.
func LoadCompactionNote(sessDir, sessionID, providerKey string) (CompactionNote, bool, error) {
	sessionID = strings.TrimSpace(sessionID)
	providerKey = strings.TrimSpace(providerKey)
	if sessionID == "" || providerKey == "" {
		return CompactionNote{}, false, errors.New("session id and compaction provider key are required")
	}
	db, err := openStore(sessDir)
	if err != nil {
		return CompactionNote{}, false, err
	}
	defer db.Close()
	if ok, err := sessionExistsDB(db, sessionID); err != nil {
		return CompactionNote{}, false, err
	} else if !ok {
		return CompactionNote{}, false, fmt.Errorf("%w: %q", ErrSessionNotFound, sessionID)
	}
	var note CompactionNote
	var updated string
	err = db.QueryRow(`
		SELECT session_id, provider_key, markdown, covered_messages, covered_hash, updated_at
		FROM session_compaction_notes
		WHERE session_id = ? AND provider_key = ?`, sessionID, providerKey).Scan(
		&note.SessionID, &note.ProviderKey, &note.Markdown, &note.CoveredMessages, &note.CoveredHash, &updated,
	)
	if errors.Is(err, sql.ErrNoRows) {
		return CompactionNote{}, false, nil
	}
	if err != nil {
		return CompactionNote{}, false, fmt.Errorf("load compaction note: %w", err)
	}
	note.UpdatedAt, _ = time.Parse(time.RFC3339Nano, updated)
	return note, true, nil
}

// StoreCompactionNote atomically replaces the active document for one
// provider. Old revisions are intentionally not accumulated.
func StoreCompactionNote(sessDir string, note CompactionNote) error {
	note.SessionID = strings.TrimSpace(note.SessionID)
	note.ProviderKey = strings.TrimSpace(note.ProviderKey)
	note.Markdown = strings.TrimSpace(note.Markdown)
	note.CoveredHash = strings.TrimSpace(note.CoveredHash)
	if note.SessionID == "" || note.ProviderKey == "" || note.Markdown == "" || note.CoveredMessages < 0 || note.CoveredHash == "" {
		return errors.New("compaction note requires session, provider, markdown, and a valid history anchor")
	}
	if note.UpdatedAt.IsZero() {
		note.UpdatedAt = time.Now().UTC()
	}
	db, err := openStore(sessDir)
	if err != nil {
		return err
	}
	defer db.Close()
	storeWriteMu.Lock()
	defer storeWriteMu.Unlock()
	_, err = db.Exec(`
		INSERT INTO session_compaction_notes (
			session_id, provider_key, markdown, covered_messages, covered_hash, updated_at
		) VALUES (?, ?, ?, ?, ?, ?)
		ON CONFLICT(session_id, provider_key) DO UPDATE SET
			markdown = excluded.markdown,
			covered_messages = excluded.covered_messages,
			covered_hash = excluded.covered_hash,
			updated_at = excluded.updated_at`,
		note.SessionID, note.ProviderKey, note.Markdown, note.CoveredMessages, note.CoveredHash, note.UpdatedAt.UTC().Format(time.RFC3339Nano),
	)
	if err != nil {
		return fmt.Errorf("store compaction note: %w", err)
	}
	return nil
}

// CompareAndSwapCompactionNote stores replacement only when the active note
// still matches the version observed before a background model fork started.
// expectedExists=false succeeds only when no active note exists.
func CompareAndSwapCompactionNote(sessDir string, expected CompactionNote, expectedExists bool, replacement CompactionNote) (bool, error) {
	replacement.SessionID = strings.TrimSpace(replacement.SessionID)
	replacement.ProviderKey = strings.TrimSpace(replacement.ProviderKey)
	replacement.Markdown = strings.TrimSpace(replacement.Markdown)
	replacement.CoveredHash = strings.TrimSpace(replacement.CoveredHash)
	if replacement.SessionID == "" || replacement.ProviderKey == "" || replacement.Markdown == "" || replacement.CoveredMessages < 0 || replacement.CoveredHash == "" {
		return false, errors.New("compaction note requires session, provider, markdown, and a valid history anchor")
	}

	if expectedExists && (strings.TrimSpace(expected.CoveredHash) == "" || expected.CoveredMessages < 0) {
		return false, errors.New("expected compaction note has an invalid history anchor")
	}
	if replacement.UpdatedAt.IsZero() {
		replacement.UpdatedAt = time.Now().UTC()
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
		return false, fmt.Errorf("begin compaction note compare-and-swap: %w", err)
	}
	defer tx.Rollback()
	if ok, err := sessionExistsTx(tx, replacement.SessionID); err != nil {
		return false, err
	} else if !ok {
		return false, fmt.Errorf("%w: %q", ErrSessionNotFound, replacement.SessionID)
	}

	var result sql.Result
	if expectedExists {
		result, err = tx.Exec(`
			UPDATE session_compaction_notes SET
				markdown = ?, covered_messages = ?, covered_hash = ?, updated_at = ?
			WHERE session_id = ? AND provider_key = ?
			  AND markdown = ? AND covered_messages = ? AND covered_hash = ?`,
			replacement.Markdown, replacement.CoveredMessages, replacement.CoveredHash, replacement.UpdatedAt.UTC().Format(time.RFC3339Nano),
			replacement.SessionID, replacement.ProviderKey,
			strings.TrimSpace(expected.Markdown), expected.CoveredMessages, strings.TrimSpace(expected.CoveredHash),
		)
	} else {
		result, err = tx.Exec(`
			INSERT INTO session_compaction_notes (
				session_id, provider_key, markdown, covered_messages, covered_hash, updated_at
			) VALUES (?, ?, ?, ?, ?, ?)
			ON CONFLICT(session_id, provider_key) DO NOTHING`,
			replacement.SessionID, replacement.ProviderKey, replacement.Markdown,
			replacement.CoveredMessages, replacement.CoveredHash, replacement.UpdatedAt.UTC().Format(time.RFC3339Nano),
		)
	}
	if err != nil {
		return false, fmt.Errorf("compare and swap compaction note: %w", err)
	}
	changed, err := result.RowsAffected()
	if err != nil {
		return false, fmt.Errorf("inspect compaction note compare-and-swap: %w", err)
	}
	if err := tx.Commit(); err != nil {
		return false, fmt.Errorf("commit compaction note compare-and-swap: %w", err)
	}
	return changed == 1, nil
}
