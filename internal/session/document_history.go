package session

import (
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/toolstate"
)

func historyDocumentKey(id string, seq int) string {
	return fmt.Sprintf("%s%020d", documentPrefix(id), seq)
}

func deleteDocumentPrefix(tx *persistence.Tx, table, prefix string) error {
	query := persistence.Query{Namespace: documentNamespace(table), Prefix: prefix, Limit: 256}
	var keys []string
	for {
		view, err := tx.ReadView(query)
		if err != nil {
			return err
		}
		for _, document := range view.Documents {
			keys = append(keys, document.Key)
		}
		if view.NextCursor == "" {
			break
		}
		query.Cursor, query.AtRevision = view.NextCursor, &view.Revision
	}
	for _, key := range keys {
		if err := tx.Delete(query.Namespace, key); err != nil {
			return err
		}
	}

	return nil
}

func historyHeadDocument(tx *persistence.Tx, id string) (int, error) {
	var head int
	_, err := tx.Get(documentNamespace("session_history_heads"), id, &head)
	return head, err
}

func insertHistoryDocument(tx *persistence.Tx, id string, seq int, record HistoryRecord, now time.Time) error {
	record = compactHistoryRecordForStorage(record)
	record.Seq = seq
	record.Role = strings.ToLower(strings.TrimSpace(record.Role))
	for _, field := range []*string{&record.Origin, &record.OriginID, &record.Cause, &record.PresentationKind, &record.RelatedSessionID, &record.Phase, &record.ProviderItemID, &record.EngineTurnID, &record.ProviderItemModel, &record.Provider, &record.Model} {
		*field = strings.TrimSpace(*field)
	}
	if invocationID := strings.TrimSpace(record.ToolInvocationID); invocationID != "" {
		if err := toolstate.Project(tx, id, invocationID, now.UnixMilli()); err != nil {
			return err
		}
	}
	if record.Role == "meta" && record.Content == "turn_terminal" && record.ClientID != "" {
		sess, err := requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		sess.LatestCompletedTurnID = record.ClientID
		if err := putSessionDocument(tx, sess); err != nil {
			return err
		}
	}
	if record.Role == "user" || record.Role == "meta" && (record.Content == tokenUsageContent || record.Content == turnTerminalContent) {
		activity := HistoryRecord{Seq: seq, Role: record.Role, Steered: record.Steered, ClientID: record.ClientID, StopReason: record.StopReason, DisplayContent: record.DisplayContent, At: record.At, Provider: record.Provider, Model: record.Model, InputTokens: record.InputTokens, OutputTokens: record.OutputTokens, CacheCreationTokens: record.CacheCreationTokens, CacheReadTokens: record.CacheReadTokens}
		if record.Role == "meta" {
			activity.Content = record.Content
		}
		if err := tx.Put(documentNamespace("session_activity"), historyDocumentKey(id, seq), activityDocument{SessionID: id, Record: activity}); err != nil {
			return err
		}
	}
	return tx.Put(documentNamespace("session_messages"), historyDocumentKey(id, seq), record)
}

func appendHistoryDocument(tx *persistence.Tx, id string, record HistoryRecord, now time.Time) (int, error) {
	if record.Role == "user" && record.ClientID != "" {
		if err := consumeHeldWorkDocument(tx, id, record); err != nil {
			return 0, err
		}
	}
	head, err := historyHeadDocument(tx, id)
	if err != nil {
		return 0, err
	}
	head++
	if err := insertHistoryDocument(tx, id, head, record, now); err != nil {
		return 0, err
	}
	return head, tx.Put(documentNamespace("session_history_heads"), id, head)
}

func (s *documentStore) AppendHistoryRecord(id string, record HistoryRecord) error {
	_, err := s.AppendHistoryRecordReturningSeq(id, record)
	return err
}

func (s *documentStore) AppendHistoryRecordReturningSeq(id string, record HistoryRecord) (int, error) {
	return s.appendFencedHistoryRecord(id, record, nil, nil, nil)
}

func (s *documentStore) AppendControlledHistoryRecord(id string, record HistoryRecord, control *Control) (int, error) {
	return s.appendFencedHistoryRecord(id, record, control, nil, nil)
}

func (s *documentStore) AppendContinuationHistoryRecord(id string, record HistoryRecord, control *Control, baseline Control) (int, error) {
	return s.appendFencedHistoryRecord(id, record, control, &baseline, nil)
}

func (s *documentStore) AppendAcceptedUserHistoryRecord(id string, record HistoryRecord, accepted HeldUserWork) (int, error) {
	record.ExpectedHeldMessage = accepted.MessageJSON
	return s.appendFencedHistoryRecord(id, record, nil, nil, &accepted)
}

func (s *documentStore) appendFencedHistoryRecord(id string, record HistoryRecord, control, baseline *Control, accepted *HeldUserWork) (int, error) {
	now := time.Now().UTC()
	var seq int
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, id); err != nil {
			return err
		}
		if control != nil {
			if control.SessionID != id {
				return ErrControlChanged
			}
			if err := validateControlDocument(tx, *control); err != nil {
				return err
			}
		}
		if baseline != nil {
			if baseline.SessionID != id {
				return ErrControlChanged
			}
			if err := validateContinuationDocument(tx, *baseline); err != nil {
				return err
			}
		}
		if record.Role == "user" && record.ClientID != "" {
			if err := validateInboxDocument(tx, record.ClientID); err != nil {
				return err
			}
		}
		if accepted != nil {
			var row heldWorkDocument
			found, err := tx.Get(documentNamespace("held_user_work"), heldWorkKey(id, record.ClientID), &row)
			if err != nil {
				return err
			}
			if !found || row.State != "pending" {
				return ErrHeldUserWorkSettled
			}
			if !heldPayloadMatches(row.HeldUserWork, *accepted) {
				return errors.New("accepted input changed before admission; review the held message")
			}
		}
		var err error
		seq, err = appendHistoryDocument(tx, id, record, now)
		return err
	})
	return seq, err
}

func (s *documentStore) AppendHistoryRecords(id string, records []HistoryRecord) error {
	_, _, err := s.AppendHistoryRecordsReturningRange(id, records)
	return err
}

func (s *documentStore) AppendHistoryRecordsReturningRange(id string, records []HistoryRecord) (int, int, error) {
	if len(records) == 0 {
		return 0, 0, nil
	}
	now := time.Now().UTC()
	var first, last int
	err := s.transaction(func(tx *persistence.Tx) error {
		first, last = 0, 0
		if _, err := requireSessionDocument(tx, id); err != nil {
			return err
		}
		for _, record := range records {
			seq, err := appendHistoryDocument(tx, id, record, now)
			if err != nil {
				return err
			}
			if first == 0 {
				first = seq
			}
			last = seq
		}
		return nil
	})
	return first, last, err
}

func (s *documentStore) RewriteHistoryRecords(id string, records []HistoryRecord) error {
	now := time.Now().UTC()
	return s.transaction(func(tx *persistence.Tx) error {
		sess, err := requireSessionDocument(tx, id)
		if err != nil {
			return err
		}
		for _, table := range []string{"session_messages", "session_activity"} {
			if err := deleteDocumentPrefix(tx, table, documentPrefix(id)); err != nil {
				return err
			}
		}
		if err := tx.Delete(documentNamespace("session_history_retractions"), id); err != nil {
			return err
		}
		sess.LatestCompletedTurnID = ""
		if err := putSessionDocument(tx, sess); err != nil {
			return err
		}
		for i, record := range records {
			if err := insertHistoryDocument(tx, id, i+1, record, now); err != nil {
				return err
			}
		}
		return tx.Put(documentNamespace("session_history_heads"), id, len(records))
	})
}

type historyRetraction struct{ FromSeq, ThroughSeq int }

func loadHistoryDocuments(tx *persistence.Tx, id string, includeMeta, activeOnly bool) ([]HistoryRecord, error) {
	rows, err := readDocumentRows[HistoryRecord](tx, "session_messages", documentPrefix(id))
	if err != nil {
		return nil, err
	}
	var retractions []historyRetraction
	if activeOnly {
		if _, err := tx.Get(documentNamespace("session_history_retractions"), id, &retractions); err != nil {
			return nil, err
		}
	}
	result := make([]HistoryRecord, 0, len(rows))
	for _, row := range rows {
		if !includeMeta && strings.EqualFold(row.Role, "meta") {
			continue
		}
		retracted := false
		for _, span := range retractions {
			if row.Seq >= span.FromSeq && row.Seq <= span.ThroughSeq {
				retracted = true
				break
			}
		}
		if retracted {
			continue
		}
		hydrateHistoryRecordFromStorage(&row)
		result = append(result, row)
	}
	return result, nil
}

func (s *documentStore) LoadHistoryRecords(id string, includeMeta bool) ([]HistoryRecord, error) {
	return s.loadHistoryRecords(id, includeMeta, false)
}

func (s *documentStore) LoadActiveHistoryRecords(id string, includeMeta bool) ([]HistoryRecord, error) {
	return s.loadHistoryRecords(id, includeMeta, true)
}

func (s *documentStore) loadHistoryRecords(id string, includeMeta, activeOnly bool) ([]HistoryRecord, error) {
	var result []HistoryRecord
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, id); err != nil {
			return err
		}
		var err error
		result, err = loadHistoryDocuments(tx, id, includeMeta, activeOnly)
		return err
	})
	return result, err
}

func latestCheckpointDocument(tx *persistence.Tx, id string) (HistoryCheckpoint, bool, error) {
	var checkpoint HistoryCheckpoint
	found, err := tx.Get(documentNamespace("session_history_checkpoints"), id, &checkpoint)
	for i := range checkpoint.Replacement {
		hydrateHistoryRecordFromStorage(&checkpoint.Replacement[i])
	}
	return checkpoint, found, err
}

func (s *documentStore) LatestHistoryCheckpoint(id string) (HistoryCheckpoint, bool, error) {
	id = strings.TrimSpace(id)
	var result HistoryCheckpoint
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, id); err != nil {
			return err
		}
		var err error
		result, found, err = latestCheckpointDocument(tx, id)
		return err
	})
	return result, found, err
}

func (s *documentStore) LoadProviderHistorySnapshot(id string) (ProviderHistorySnapshot, error) {
	id = strings.TrimSpace(id)
	var result ProviderHistorySnapshot
	err := s.transaction(func(tx *persistence.Tx) error {
		result = ProviderHistorySnapshot{}
		if _, err := requireSessionDocument(tx, id); err != nil {
			return err
		}
		head, err := historyHeadDocument(tx, id)
		if err != nil {
			return err
		}
		checkpoint, found, err := latestCheckpointDocument(tx, id)
		if err != nil {
			return err
		}
		records, err := loadHistoryDocuments(tx, id, true, false)
		if err != nil {
			return err
		}
		result.HeadSeq = head
		if !found {
			result.Records = records
			return nil
		}
		result.Checkpoint = &checkpoint
		result.Records = append(result.Records, checkpoint.Replacement...)
		for _, record := range records {
			if record.Seq > checkpoint.ThroughSeq && record.Seq <= head {
				result.Records = append(result.Records, record)
			}
		}
		return nil
	})
	return result, err
}
