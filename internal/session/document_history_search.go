package session

import (
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

func (s *Store) ReadHistoryPage(id string, startSeq, limit int) (HistoryPage, error) {
	return s.ReadHistoryQuery(HistoryReadQuery{SessionID: id, StartSeq: startSeq, Limit: limit})
}

func (s *Store) ReadHistoryQuery(query HistoryReadQuery) (HistoryPage, error) {
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
		records, err := loadHistoryDocuments(tx, id, false, false)
		if err != nil {
			return err
		}
		for _, record := range records {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			if record.Seq < start || record.Seq > end {
				continue
			}
			if len(page.Records) == query.Limit {
				page.HasMore = true
				break
			}
			page.Records = append(page.Records, record)
		}
		if page.HasMore {
			page.Next = &HistoryCursor{SessionID: id, SnapshotSeq: snapshot, Seq: page.Records[len(page.Records)-1].Seq + 1}
		}
		return nil
	})
	return page, err
}

func (s *Store) SearchHistoryPage(id, query string, beforeSeq, limit int) (HistoryPage, error) {
	return s.SearchHistoryQuery(HistorySearchQuery{SessionID: id, Query: query, BeforeSeq: beforeSeq, Limit: limit})
}

func (s *Store) SearchHistoryQuery(query HistorySearchQuery) (HistoryPage, error) {
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
		records, err := loadHistoryDocuments(tx, id, false, false)
		if err != nil {
			return err
		}
		needle := strings.ToLower(query.Query)
		for i := len(records) - 1; i >= 0; i-- {
			if err := s.ctx.Err(); err != nil {
				return err
			}
			record := records[i]
			if record.Seq < start || record.Seq >= upper || record.Seq > end {
				continue
			}
			text := strings.Join([]string{record.Content, record.DisplayContent, record.ReasoningContent, string(record.ToolCalls), string(record.ToolResult), record.Name}, "\n")
			if !strings.Contains(strings.ToLower(text), needle) {
				continue
			}
			if len(page.Records) == query.Limit {
				page.HasMore = true
				break
			}
			page.Records = append(page.Records, record)
		}
		if page.HasMore {
			page.Next = &HistoryCursor{SessionID: id, SnapshotSeq: snapshot, Seq: page.Records[len(page.Records)-1].Seq}
		}
		return nil
	})
	return page, err
}
