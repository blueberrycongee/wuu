package session

import (
	"bytes"
	"errors"
	"fmt"
	"sort"
	"strings"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

type heldWorkDocument struct {
	HeldUserWork
	State string
}

func heldWorkKey(sessionID, id string) string { return documentPrefix(sessionID) + id }

func nextHeldPosition(tx *persistence.Tx, sessionID string) (int, error) {
	rows, err := readDocumentRows[heldWorkDocument](tx, "held_user_work", documentPrefix(sessionID))
	position := 0
	for _, row := range rows {
		position = max(position, row.Position+1)
	}
	return position, err
}

func (s *documentStore) ReplaceHeldUserWork(sessionID string, entries []HeldUserWork) error {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return errors.New("session_id is required")
	}
	return s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		if err := deleteDocumentPrefix(tx, "held_user_work", documentPrefix(sessionID)); err != nil {
			return err
		}
		seen := make(map[string]bool, len(entries))
		for position, entry := range entries {
			entry.ID, entry.Origin = strings.TrimSpace(entry.ID), strings.TrimSpace(entry.Origin)
			if err := validateHeldUserWork(entry); err != nil {
				return err
			}
			if seen[entry.ID] {
				return fmt.Errorf("duplicate held user work %q", entry.ID)
			}
			seen[entry.ID] = true
			entry.SessionID, entry.Position, entry.DeliveryUnconfirmed = sessionID, position, false
			if err := tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, entry.ID), heldWorkDocument{HeldUserWork: entry, State: "pending"}); err != nil {
				return err
			}
		}
		return nil
	})
}

func (s *documentStore) LoadHeldUserWork(sessionID string) ([]HeldUserWork, error) {
	sessionID = strings.TrimSpace(sessionID)
	if sessionID == "" {
		return nil, errors.New("session_id is required")
	}
	var result []HeldUserWork
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[heldWorkDocument](tx, "held_user_work", documentPrefix(sessionID))
		if err != nil {
			return err
		}
		result = nil
		for _, row := range rows {
			if row.State != "pending" && row.State != "dispatching" {
				continue
			}
			row.DeliveryUnconfirmed = row.State == "dispatching"
			result = append(result, row.HeldUserWork)
		}
		sort.Slice(result, func(i, j int) bool {
			if result[i].Origin != result[j].Origin {
				return result[i].Origin == HeldUserWorkOriginSteer
			}
			return result[i].Position < result[j].Position
		})
		return nil
	})
	return result, err
}

func (s *documentStore) DeleteHeldUserWork(sessionID, id string) (bool, error) {
	sessionID, id = strings.TrimSpace(sessionID), strings.TrimSpace(id)
	if sessionID == "" || id == "" {
		return false, errors.New("session_id and id are required")
	}
	var removed bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var row heldWorkDocument
		var err error
		removed, err = tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, id), &row)
		if err != nil || !removed {
			return err
		}
		if err := tx.Delete(documentNamespace("held_user_work"), heldWorkKey(sessionID, id)); err != nil {
			return err
		}
		rows, err := readDocumentRows[heldWorkDocument](tx, "held_user_work", documentPrefix(sessionID))
		if err != nil {
			return err
		}
		for _, other := range rows {
			if other.Position <= row.Position {
				continue
			}
			other.Position--
			if err := tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, other.ID), other); err != nil {
				return err
			}
		}
		return nil
	})
	return removed, err
}

func (s *documentStore) AcceptHeldUserWork(sessionID string, entry HeldUserWork) (bool, error) {
	if err := validateHeldUserWork(entry); err != nil {
		return false, err
	}
	var accepted bool
	err := s.transaction(func(tx *persistence.Tx) error {
		accepted = false
		if _, err := requireSessionDocument(tx, sessionID); err != nil {
			return err
		}
		var prior heldWorkDocument
		found, err := tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, entry.ID), &prior)
		if err != nil {
			return err
		}
		position := prior.Position
		if found {
			switch {
			case prior.State == "cancelled" && prior.Origin != entry.Origin:
			case prior.State == "dispatching":
				return ErrHeldUserWorkUnconfirmed
			case prior.State != "pending":
				return ErrHeldUserWorkSettled
			default:
				return nil
			}
		} else {
			history, err := readDocumentRows[HistoryRecord](tx, "session_messages", documentPrefix(sessionID))
			if err != nil {
				return err
			}
			for _, record := range history {
				if record.Role == "user" && record.ClientID == entry.ID {
					return ErrHeldUserWorkSettled
				}
			}
			position, err = nextHeldPosition(tx, sessionID)
			if err != nil {
				return err
			}
		}
		current := entry
		current.SessionID, current.Position, current.DeliveryUnconfirmed = sessionID, position, false
		accepted = true
		return tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, entry.ID), heldWorkDocument{HeldUserWork: current, State: "pending"})
	})
	return accepted, err
}

func heldPayloadMatches(current HeldUserWork, expected HeldUserWork) bool {
	return current.Origin == expected.Origin && bytes.Equal(current.MessageJSON, expected.MessageJSON) && bytes.Equal(current.RuntimeJSON, expected.RuntimeJSON)
}

func (s *documentStore) UpdateHeldUserWork(sessionID string, entry, expected HeldUserWork) (bool, error) {
	if err := validateHeldUserWork(entry); err != nil {
		return false, err
	}
	var changed bool
	err := s.transaction(func(tx *persistence.Tx) error {
		changed = false
		var row heldWorkDocument
		found, err := tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, entry.ID), &row)
		if err != nil || !found {
			return err
		}
		if row.State != "pending" || !heldPayloadMatches(row.HeldUserWork, expected) {
			return nil
		}
		row.Origin, row.MessageJSON, row.RuntimeJSON = entry.Origin, entry.MessageJSON, entry.RuntimeJSON
		changed = true
		return tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, entry.ID), row)
	})
	return changed, err
}

func (s *documentStore) CancelHeldUserWork(sessionID, id, origin string) (bool, error) {
	var removed bool
	err := s.transaction(func(tx *persistence.Tx) error {
		removed = false
		var row heldWorkDocument
		found, err := tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, id), &row)
		if err != nil {
			return err
		}
		if found {
			if row.State == "dispatching" {
				return ErrHeldUserWorkUnconfirmed
			}
			removed = row.State == "pending" && (origin == "" || origin == row.Origin)
			if !removed {
				return nil
			}
		} else {
			if _, err := requireSessionDocument(tx, sessionID); err != nil {
				return err
			}
			position, err := nextHeldPosition(tx, sessionID)
			if err != nil {
				return err
			}
			row.HeldUserWork = HeldUserWork{SessionID: sessionID, Position: position, ID: id, Origin: origin}
		}
		row.State, row.MessageJSON, row.RuntimeJSON = "cancelled", nil, nil
		return tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, id), row)
	})
	return removed, err
}

// The receipt and history share one commit so recovery cannot see both pending
// input and its admitted history. A native dispatch claim is never replayed.
func consumeHeldWorkDocument(tx *persistence.Tx, sessionID string, record HistoryRecord) error {
	var row heldWorkDocument
	found, err := tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, record.ClientID), &row)
	if err != nil || !found {
		return err
	}
	if row.State != "pending" && row.State != "dispatching" || record.Hidden || record.ReadOnly || record.Origin != "" && record.Origin != "user" {
		return ErrHeldUserWorkSettled
	}
	if len(record.ExpectedHeldMessage) == 0 || !bytes.Equal(record.ExpectedHeldMessage, row.MessageJSON) {
		return errors.New("accepted steering changed before dispatch; review the held message")
	}
	row.State, row.MessageJSON, row.RuntimeJSON = "consumed", nil, nil
	return tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, record.ClientID), row)
}

func (s *documentStore) HasHeldUserWorkReceipt(sessionID, id string) (bool, error) {
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var row heldWorkDocument
		var err error
		found, err = tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, id), &row)
		return err
	})
	return found, err
}

func (s *documentStore) ClaimHeldUserWork(sessionID string, entry HeldUserWork) error {
	if err := validateHeldUserWork(entry); err != nil {
		return err
	}
	return s.transaction(func(tx *persistence.Tx) error {
		var row heldWorkDocument
		found, err := tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, entry.ID), &row)
		if err != nil {
			return err
		}
		if !found || row.State != "pending" || !heldPayloadMatches(row.HeldUserWork, entry) {
			return errors.New("accepted input changed before native dispatch; review the held message")
		}
		row.State = "dispatching"
		return tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, entry.ID), row)
	})
}

func (s *documentStore) ReleaseHeldUserWork(sessionID, id string) error {
	return s.transaction(func(tx *persistence.Tx) error {
		var row heldWorkDocument
		found, err := tx.Get(documentNamespace("held_user_work"), heldWorkKey(sessionID, id), &row)
		if err != nil || !found || row.State != "dispatching" {
			return err
		}
		row.State = "pending"
		return tx.Put(documentNamespace("held_user_work"), heldWorkKey(sessionID, id), row)
	})
}
