package session

import (
	"errors"
)

// Control is an execution fence, independent of session ownership and ancestry.
// Revision changes invalidate previously admitted automatic instructions.
type Control struct {
	SessionID string `json:"session_id"`
	ManagerID string `json:"manager_id"`
	Revision  int64  `json:"revision"`
	State     string `json:"state"`
}

const (
	ControlActive    = "active"
	ControlPaused    = "paused"
	ControlTakenOver = "taken_over"
	ControlReleased  = "released"
)

var ErrControlChanged = errors.New("session control changed; inspect before continuing")

func validateContinuationBaseline(current, baseline Control) error {
	if current == baseline {
		return nil
	}
	if (baseline.State == "" || baseline.State == ControlReleased) &&
		current.SessionID == baseline.SessionID && current.State == ControlActive &&
		current.Revision == baseline.Revision+1 {
		return nil
	}
	return ErrControlChanged
}
