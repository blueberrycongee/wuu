package session

import (
	"encoding/json"
	"errors"
	"fmt"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func resolveHistoryDocumentSnapshot(tx *persistence.Tx, id string, snapshot int, cursor *HistoryCursor) (string, int, error) {
	id = strings.TrimSpace(id)
	if cursor != nil {
		if !cursor.valid() {
			return "", 0, errors.New("history cursor is incomplete")
		}
		if id == "" {
			id = cursor.SessionID
		} else if id != cursor.SessionID {
			return "", 0, errors.New("history cursor session does not match")
		}
		if snapshot == 0 {
			snapshot = cursor.SnapshotSeq
		} else if snapshot != cursor.SnapshotSeq {
			return "", 0, errors.New("history cursor snapshot does not match")
		}
	}
	if id == "" {
		return "", 0, errors.New("history session id is required")
	}
	if _, err := requireSessionDocument(tx, id); err != nil {
		return "", 0, err
	}
	head, err := historyHeadDocument(tx, id)
	if err != nil {
		return "", 0, err
	}
	if snapshot == 0 && cursor == nil {
		snapshot = head
	}
	if snapshot > head {
		return "", 0, fmt.Errorf("%w: requested snapshot %d is beyond head %d", ErrHistorySnapshotGone, snapshot, head)
	}
	return id, snapshot, nil
}

func (s *documentStore) ReadHistoryPage(id string, startSeq, limit int) (HistoryPage, error) {
	return s.ReadHistoryQuery(HistoryReadQuery{SessionID: id, StartSeq: startSeq, Limit: limit})
}

func (s *documentStore) ReadHistoryQuery(query HistoryReadQuery) (HistoryPage, error) {
	return s.readHistoryQuery(query, false)
}

func (s *documentStore) ReadActiveHistoryQuery(query HistoryReadQuery) (HistoryPage, error) {
	return s.readHistoryQuery(query, true)
}

func (s *documentStore) readHistoryQuery(query HistoryReadQuery, activeOnly bool) (HistoryPage, error) {
	if query.StartSeq < 1 && query.Cursor == nil {
		return HistoryPage{}, errors.New("history start sequence must be positive")
	}
	if query.Limit < 1 {
		return HistoryPage{}, errors.New("history page limit must be positive")
	}
	if query.EndSeq < 0 || query.SnapshotSeq < 0 {
		return HistoryPage{}, errors.New("history snapshot bounds must be non-negative")
	}
	var page HistoryPage
	err := s.transaction(func(tx *persistence.Tx) error {
		id, snapshot, err := resolveHistoryDocumentSnapshot(tx, query.SessionID, query.SnapshotSeq, query.Cursor)
		if err != nil {
			return err
		}
		page = HistoryPage{HeadSeq: snapshot, SnapshotSeq: snapshot}
		start, end := max(query.StartSeq, 1), query.EndSeq
		if end == 0 || end > snapshot {
			end = snapshot
		}
		if query.Cursor != nil {
			start = query.Cursor.Seq
		}
		if end < start {
			return nil
		}
		var retractions []historyRetraction
		if activeOnly {
			if _, err := tx.Get(documentNamespace("session_history_retractions"), id, &retractions); err != nil {
				return err
			}
		}
		scan := persistence.Query{Namespace: documentNamespace("session_messages"), Prefix: documentPrefix(id), Start: historyDocumentKey(id, start), End: historyDocumentKey(id, end+1), Limit: min(query.Limit+1, 256)}
		for {
			view, err := tx.ReadView(scan)
			if err != nil {
				return err
			}
			for _, document := range view.Documents {
				var record HistoryRecord
				if err := json.Unmarshal(document.Value, &record); err != nil {
					return err
				}
				if historySequenceRetracted(record.Seq, retractions) {
					continue
				}
				if strings.EqualFold(record.Role, "meta") {
					continue
				}
				if len(page.Records) == query.Limit {
					page.HasMore = true
					page.Next = &HistoryCursor{SessionID: id, SnapshotSeq: snapshot, Seq: page.Records[len(page.Records)-1].Seq + 1}
					return nil
				}
				hydrateHistoryRecordFromStorage(&record)
				page.Records = append(page.Records, record)
			}
			if view.NextCursor == "" {
				break
			}
			scan.Cursor, scan.AtRevision = view.NextCursor, &view.Revision
		}

		return nil
	})
	return page, err
}

func (s *documentStore) SearchHistoryPage(id, query string, beforeSeq, limit int) (HistoryPage, error) {
	return s.SearchHistoryQuery(HistorySearchQuery{SessionID: id, Query: query, BeforeSeq: beforeSeq, Limit: limit})
}

func (s *documentStore) SearchHistoryQuery(query HistorySearchQuery) (HistoryPage, error) {
	if !query.TimeMin.IsZero() && !query.TimeMax.IsZero() && !query.TimeMax.After(query.TimeMin) {
		return HistoryPage{}, errors.New("history time_max must follow time_min")
	}
	if query.View != "" && query.View != "conversation" {
		return HistoryPage{}, errors.New("unsupported history search view")
	}
	query.Query = strings.TrimSpace(query.Query)
	if query.Query == "" {
		return HistoryPage{}, errors.New("history search query is required")
	}
	if query.BeforeSeq < 0 || query.StartSeq < 0 || query.EndSeq < 0 || query.SnapshotSeq < 0 {
		return HistoryPage{}, errors.New("history search cursor must be non-negative")
	}
	if query.Limit < 1 {
		return HistoryPage{}, errors.New("history search limit must be positive")
	}
	var page HistoryPage
	err := s.transaction(func(tx *persistence.Tx) error {
		id, snapshot, err := resolveHistoryDocumentSnapshot(tx, query.SessionID, query.SnapshotSeq, query.Cursor)
		if err != nil {
			return err
		}
		page = HistoryPage{HeadSeq: snapshot, SnapshotSeq: snapshot}
		start, end, upper := max(query.StartSeq, 1), query.EndSeq, query.BeforeSeq
		if end == 0 || end > snapshot {
			end = snapshot
		}
		if query.Cursor != nil {
			upper = query.Cursor.Seq
		}
		if upper <= 0 || upper > end+1 {
			upper = end + 1
		}
		if upper <= start {
			return nil
		}
		scan := persistence.Query{Namespace: documentNamespace("session_messages"), Prefix: documentPrefix(id), Start: historyDocumentKey(id, start), End: historyDocumentKey(id, upper), Reverse: true, Limit: 256}
		needle := strings.ToLower(query.Query)
		for {
			view, err := tx.ReadView(scan)
			if err != nil {
				return err
			}
			for _, document := range view.Documents {
				var record HistoryRecord
				if err := json.Unmarshal(document.Value, &record); err != nil {
					return err
				}
				if strings.EqualFold(record.Role, "meta") {
					continue
				}
				hydrateHistoryRecordFromStorage(&record)
				if !query.TimeMin.IsZero() && (record.At.IsZero() || record.At.Before(query.TimeMin)) || !query.TimeMax.IsZero() && (record.At.IsZero() || !record.At.Before(query.TimeMax)) {
					continue
				}
				text := strings.Join([]string{record.Content, record.DisplayContent, record.ReasoningContent, string(record.ToolCalls), string(record.ToolResult), record.Name}, "\n")
				if query.View == "conversation" {
					role := strings.ToLower(record.Role)
					if record.Hidden || (role != "user" && role != "assistant") || role == "user" && (record.ReadOnly || record.Origin != "" && record.Origin != "user") {
						continue
					}
					content := record.DisplayContent
					if content == "" {
						content = record.Content
					}
					attachments := len(record.Files) > 0 || len(record.Images) > 0
					if content == "" && !attachments {
						continue
					}
					if role == "user" && record.Origin == "user" && attachments {
						content = record.DisplayContent
					}
					text = content + "\n" + record.Name
				}
				if !strings.Contains(strings.ToLower(text), needle) {
					continue
				}
				if len(page.Records) == query.Limit {
					page.HasMore = true
					page.Next = &HistoryCursor{SessionID: id, SnapshotSeq: snapshot, Seq: page.Records[len(page.Records)-1].Seq}
					return nil
				}
				page.Records = append(page.Records, record)
			}
			if view.NextCursor == "" {
				break
			}
			scan.Cursor, scan.AtRevision = view.NextCursor, &view.Revision
		}

		return nil
	})
	return page, err
}
