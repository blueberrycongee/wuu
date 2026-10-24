package session

import (
	"errors"
	"sort"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

type inboxDocument struct {
	InboxMessage
	Order       int64
	DeliveredAt *time.Time
}

func insertInboxDocument(tx *persistence.Tx, message InboxMessage, now time.Time) error {
	if strings.TrimSpace(message.ClientID) == "" || strings.TrimSpace(message.SessionID) == "" {
		return errors.New("inbox message requires a client ID and target session")
	}
	var prior inboxDocument
	if found, err := tx.Get(documentNamespace("session_inbox"), message.ClientID, &prior); err != nil || found {
		return err
	}
	if _, err := requireSessionDocument(tx, message.SessionID); err != nil {
		return err
	}
	if message.CreatedAt.IsZero() {
		message.CreatedAt = now
	}
	var order int64
	if _, err := tx.Get(documentNamespace("sequences"), "inbox", &order); err != nil {
		return err
	}
	order++
	if err := tx.Put(documentNamespace("sequences"), "inbox", order); err != nil {
		return err
	}
	return tx.Put(documentNamespace("session_inbox"), message.ClientID, inboxDocument{InboxMessage: message, Order: order})
}

func (s *Store) EnqueueInbox(message InboxMessage) error {
	now := time.Now().UTC()
	return s.transaction(func(tx *persistence.Tx) error { return insertInboxDocument(tx, message, now) })
}

func (s *Store) SettleInbox(clientID, sessionID string) error {
	if strings.TrimSpace(clientID) == "" || strings.TrimSpace(sessionID) == "" {
		return errors.New("inbox message requires a client ID and target session")
	}
	now := time.Now().UTC()
	return s.transaction(func(tx *persistence.Tx) error {
		if err := insertInboxDocument(tx, InboxMessage{ClientID: clientID, SessionID: sessionID}, now); err != nil {
			return err
		}
		var row inboxDocument
		if _, err := tx.Get(documentNamespace("session_inbox"), clientID, &row); err != nil {
			return err
		}
		row.DeliveredAt = &now
		return tx.Put(documentNamespace("session_inbox"), clientID, row)
	})
}

func (s *Store) PendingInbox(sessionID string) ([]InboxMessage, error) {
	now := time.Now().UTC()
	var result []InboxMessage
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[inboxDocument](tx, "session_inbox", "")
		if err != nil {
			return err
		}
		history, err := readDocumentRows[HistoryRecord](tx, "session_messages", documentPrefix(sessionID))
		if err != nil {
			return err
		}
		delivered := make(map[string]bool, len(history))
		for _, record := range history {
			delivered[record.ClientID] = true
		}
		sort.Slice(rows, func(i, j int) bool {
			if !rows[i].CreatedAt.Equal(rows[j].CreatedAt) {
				return rows[i].CreatedAt.Before(rows[j].CreatedAt)
			}
			return rows[i].Order < rows[j].Order
		})
		result = nil
		for _, row := range rows {
			if row.SessionID != sessionID || row.DeliveredAt != nil {
				continue
			}
			if delivered[row.ClientID] {
				row.DeliveredAt = &now
				if err := tx.Put(documentNamespace("session_inbox"), row.ClientID, row); err != nil {
					return err
				}
			} else {
				result = append(result, row.InboxMessage)
			}
		}
		return nil
	})
	return result, err
}

func (s *Store) InboxTargets() ([]string, error) {
	var result []string
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[inboxDocument](tx, "session_inbox", "")
		if err != nil {
			return err
		}
		seen := make(map[string]bool)
		for _, row := range rows {
			if row.DeliveredAt == nil {
				seen[row.SessionID] = true
			}
		}
		result = nil
		for id := range seen {
			result = append(result, id)
		}
		sort.Strings(result)
		return nil
	})
	return result, err
}

func (s *Store) InboxHas(clientID string) (bool, error) {
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var row inboxDocument
		var err error
		found, err = tx.Get(documentNamespace("session_inbox"), clientID, &row)
		return err
	})
	return found, err
}

func (s *Store) ValidateInboxControls(clientID string) error {
	return s.transaction(func(tx *persistence.Tx) error { return validateInboxDocument(tx, clientID) })
}

func validateInboxDocument(tx *persistence.Tx, clientID string) error {
	var row inboxDocument
	found, err := tx.Get(documentNamespace("session_inbox"), clientID, &row)
	if err != nil || !found {
		return err
	}
	if row.WorkID != "" {
		var work ProjectWork
		found, err := tx.Get(documentNamespace("project_work"), row.WorkID, &work)
		if err != nil {
			return err
		}
		if !found || work.Revision != row.WorkRevision || work.Phase == "stopped" || row.WorkVersion > 0 && work.Version != row.WorkVersion {
			return ErrControlChanged
		}
		if row.Cause == "project" {
			current := work.LeadInput
			if row.SessionID == work.ExecutorID {
				current = work.ExecutorInput
			}
			if current == nil || current.ClientID != clientID {
				return ErrControlChanged
			}
		}
	}
	for _, control := range row.Controls {
		if err := validateControlDocument(tx, control); err != nil {
			return err
		}
	}
	return nil
}
