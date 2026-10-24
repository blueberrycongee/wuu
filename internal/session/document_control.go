package session

import (
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/persistence"
)

func (s *Store) ReadControl(id string) (Control, bool, error) {
	var control Control
	var found bool
	err := s.transaction(func(tx *persistence.Tx) error {
		var err error
		found, err = tx.Get(documentNamespace("session_controls"), id, &control)
		return err
	})
	return control, found, err
}

func (s *Store) ListControls() (map[string]Control, error) {
	var result map[string]Control
	err := s.transaction(func(tx *persistence.Tx) error {
		rows, err := readDocumentRows[Control](tx, "session_controls", "")
		if err != nil {
			return err
		}
		result = make(map[string]Control, len(rows))
		for _, row := range rows {
			result[row.SessionID] = row
		}
		return nil
	})
	return result, err
}

func (s *Store) ChangeControl(id, manager, state string, expected int64) (Control, error) {
	if id == "" || manager == "" {
		return Control{}, errors.New("session and manager are required")
	}
	switch state {
	case ControlActive, ControlPaused, ControlTakenOver, ControlReleased:
	default:
		return Control{}, errors.New("invalid control state")
	}
	result := Control{SessionID: id, ManagerID: manager, Revision: expected + 1, State: state}
	err := s.transaction(func(tx *persistence.Tx) error {
		if _, err := requireSessionDocument(tx, id); err != nil {
			return err
		}
		var old Control
		if _, err := tx.Get(documentNamespace("session_controls"), id, &old); err != nil {
			return err
		}
		if old.Revision != expected || old.ManagerID != "" && old.ManagerID != manager && old.State != ControlReleased {
			return ErrControlChanged
		}
		return tx.Put(documentNamespace("session_controls"), id, result)
	})
	return result, err
}

func validateControlDocument(tx *persistence.Tx, expected Control) error {
	var current Control
	found, err := tx.Get(documentNamespace("session_controls"), expected.SessionID, &current)
	if err != nil {
		return err
	}
	if !found || current.ManagerID != expected.ManagerID || current.Revision != expected.Revision || current.State != ControlActive {
		return fmt.Errorf("%w: automatic execution is no longer authorized", ErrControlChanged)
	}
	return nil
}

func (s *Store) ValidateControl(expected Control) error {
	return s.transaction(func(tx *persistence.Tx) error { return validateControlDocument(tx, expected) })
}

func validateContinuationDocument(tx *persistence.Tx, baseline Control) error {
	current := Control{SessionID: baseline.SessionID}
	if _, err := tx.Get(documentNamespace("session_controls"), baseline.SessionID, &current); err != nil {
		return err
	}
	return validateContinuationBaseline(current, baseline)
}

func (s *Store) ValidateContinuationControl(baseline Control) error {
	return s.transaction(func(tx *persistence.Tx) error { return validateContinuationDocument(tx, baseline) })
}
