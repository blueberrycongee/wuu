package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *Store) RewriteHistoryRecordsAtBaseline(id string, records []HistoryRecord, baselineSeq int) error {
	_, err := s.StoreHistoryCheckpointAtBaseline(id, HistoryCheckpointKindProviderRewrite, records, baselineSeq)
	return err
}

func (s *Store) RewriteHistoryRecordsForEdit(id string, records []HistoryRecord, fromSeq, baselineSeq int) error {
	if fromSeq <= 0 || fromSeq > baselineSeq {
		return fmt.Errorf("invalid history edit range %d..%d", fromSeq, baselineSeq)
	}
	_, err := s.storeHistoryCheckpointAtBaseline(id, HistoryCheckpointKindProviderRewrite, records, baselineSeq, nil, fromSeq, nil)
	return err
}

func (s *Store) StoreHistoryCheckpointAtBaseline(id, kind string, replacement []HistoryRecord, baselineSeq int) (HistoryCheckpoint, error) {
	return s.storeHistoryCheckpointAtBaseline(id, kind, replacement, baselineSeq, nil, 0, nil)
}

func (s *Store) StoreContextWindow(id string, replacement []HistoryRecord, baselineSeq int, note CompactionNote) (HistoryCheckpoint, error) {
	if strings.TrimSpace(note.ProviderKey) == "" {
		return HistoryCheckpoint{}, errors.New("context window requires a note provider key")
	}
	return s.storeHistoryCheckpointAtBaseline(id, "context_window", replacement, baselineSeq, &note, 0, nil)
}

func (s *Store) RewriteHistoryRecordsForEngineEdit(id string, records []HistoryRecord, fromSeq, baselineSeq int, oldRef, newRef string) error {
	if fromSeq <= 0 || fromSeq > baselineSeq || oldRef == "" || newRef == "" || oldRef == newRef {
		return errors.New("invalid native history edit")
	}
	_, err := s.storeHistoryCheckpointAtBaseline(id, HistoryCheckpointKindProviderRewrite, records, baselineSeq, nil, fromSeq, &[2]string{oldRef, newRef})
	return err
}

func (s *Store) storeHistoryCheckpointAtBaseline(id, kind string, replacement []HistoryRecord, baselineSeq int, note *CompactionNote, retractFromSeq int, engineRefs *[2]string) (HistoryCheckpoint, error) {
	id, kind = strings.TrimSpace(id), strings.TrimSpace(kind)
	if baselineSeq < 0 {
		return HistoryCheckpoint{}, fmt.Errorf("history checkpoint baseline must be non-negative: %d", baselineSeq)
	}
	if kind == "" {
		return HistoryCheckpoint{}, errors.New("history checkpoint kind is required")
	}
	now := time.Now().UTC()
	var result HistoryCheckpoint
	err := s.transaction(func(tx *persistence.Tx) error {
		sess, err := requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		head, err := historyHeadDocument(tx, id)
		if err != nil {
			return err
		}
		if baselineSeq > head {
			return fmt.Errorf("history checkpoint baseline %d exceeds head %d", baselineSeq, head)
		}
		if engineRefs != nil {
			if head != baselineSeq {
				return errors.New("native history changed during edit; refresh and try again")
			}
			if sess.EngineRef != engineRefs[0] {
				return errors.New("native session changed during edit; refresh and try again")
			}
			sess.EngineRef = engineRefs[1]
			if err := putSessionDocument(tx, sess); err != nil {
				return err
			}
		}
		records := append([]HistoryRecord(nil), replacement...)
		seen := make(map[int]bool, len(records))
		for _, record := range records {
			if record.Seq < 0 {
				return fmt.Errorf("history checkpoint record has negative seq %d", record.Seq)
			}
			if record.Seq > head {
				return fmt.Errorf("history checkpoint record seq %d exceeds head %d", record.Seq, head)
			}
			if record.Seq != 0 {
				seen[record.Seq] = true
			}
		}
		physical, err := loadHistoryDocuments(tx, id, true, false)
		if err != nil {
			return err
		}
		var tail []HistoryRecord
		for _, record := range physical {
			if record.Seq > baselineSeq && record.Seq <= head && !seen[record.Seq] {
				tail = append(tail, record)
			}
		}
		for i := range records {
			if records[i].Seq != 0 {
				continue
			}
			head++
			records[i].Seq = head
			if err := insertHistoryDocument(tx, id, head, records[i], now); err != nil {
				return err
			}
		}
		records = append(records, tail...)
		prior, _, err := latestCheckpointDocument(tx, id)
		if err != nil {
			return err
		}
		result = HistoryCheckpoint{SessionID: id, Version: prior.Version + 1, Kind: kind, ThroughSeq: head, Replacement: records, CreatedAt: now}
		stored := result
		stored.Replacement = make([]HistoryRecord, len(records))
		for i, record := range records {
			stored.Replacement[i] = compactHistoryRecordForStorage(record)
		}
		if err := tx.Put(documentNamespace("session_history_checkpoints"), id, stored); err != nil {
			return err
		}
		if err := tx.Put(documentNamespace("session_history_heads"), id, head); err != nil {
			return err
		}
		if retractFromSeq > 0 {
			var retractions []historyRetraction
			if _, err := tx.Get(documentNamespace("session_history_retractions"), id, &retractions); err != nil {
				return err
			}
			retractions = append(retractions, historyRetraction{FromSeq: retractFromSeq, ThroughSeq: baselineSeq})
			if err := tx.Put(documentNamespace("session_history_retractions"), id, retractions); err != nil {
				return err
			}
		}
		if note != nil {
			current := *note
			current.SessionID, current.UpdatedAt = id, now
			if strings.TrimSpace(current.Markdown) == "" {
				current.CoveredHash = fmt.Sprintf("checkpoint:%d", result.Version)
			}
			if err := tx.Put(documentNamespace("session_compaction_notes"), documentPrefix(id)+current.ProviderKey, current); err != nil {
				return err
			}
		}
		return nil
	})
	return result, err
}

func (s *Store) LoadCompactionNote(sessionID, providerKey string) (CompactionNote, bool, error) {
	sessionID, providerKey = strings.TrimSpace(sessionID), strings.TrimSpace(providerKey)
	if sessionID == "" || providerKey == "" {
		return CompactionNote{}, false, errors.New("session id and compaction provider key are required")
	}
	var result CompactionNote
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		var err error
		found, err = tx.Get(documentNamespace("session_compaction_notes"), documentPrefix(sessionID)+providerKey, &result)
		return err
	})
	return result, found, err
}

func prepareCompactionNote(note CompactionNote) (CompactionNote, error) {
	note.SessionID, note.ProviderKey = strings.TrimSpace(note.SessionID), strings.TrimSpace(note.ProviderKey)
	note.Markdown, note.CoveredHash = strings.TrimSpace(note.Markdown), strings.TrimSpace(note.CoveredHash)
	if note.SessionID == "" || note.ProviderKey == "" || note.Markdown == "" || note.CoveredMessages < 0 || note.CoveredHash == "" {
		return note, errors.New("compaction note requires session, provider, markdown, and a valid history anchor")
	}
	if note.UpdatedAt.IsZero() {
		note.UpdatedAt = time.Now().UTC()
	}
	return note, nil
}

func (s *Store) StoreCompactionNote(note CompactionNote) error {
	note, err := prepareCompactionNote(note)
	if err != nil {
		return err
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, note.SessionID); err != nil {
			return err
		}
		return tx.Put(documentNamespace("session_compaction_notes"), documentPrefix(note.SessionID)+note.ProviderKey, note)
	})
}

func (s *Store) CompareAndSwapCompactionNote(expected CompactionNote, expectedExists bool, replacement CompactionNote) (bool, error) {
	replacement, err := prepareCompactionNote(replacement)
	if err != nil {
		return false, err
	}
	if expectedExists && (strings.TrimSpace(expected.CoveredHash) == "" || expected.CoveredMessages < 0) {
		return false, errors.New("expected compaction note has an invalid history anchor")
	}
	var changed bool
	err = s.transaction(func(tx *persistence.Tx) error {
		changed = false
		if _, err := requireSessionDocument(tx, replacement.SessionID); err != nil {
			return err
		}
		var current CompactionNote
		key := documentPrefix(replacement.SessionID) + replacement.ProviderKey
		found, err := tx.Get(documentNamespace("session_compaction_notes"), key, &current)
		if err != nil {
			return err
		}
		if found != expectedExists {
			return nil
		}
		if found && (current.Markdown != strings.TrimSpace(expected.Markdown) || current.CoveredHash != strings.TrimSpace(expected.CoveredHash) || current.CoveredMessages != expected.CoveredMessages) {
			return nil
		}
		changed = true
		return tx.Put(documentNamespace("session_compaction_notes"), key, replacement)
	})
	return changed, err
}
