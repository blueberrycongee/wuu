package session

import (
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/participant"
)

var ErrParticipantNotFound = errors.New("participant not found")

type ParticipantRun struct {
	ID            string
	ParticipantID string
	AgentID       string
	TaskID        string
	SessionID     string
	Summary       string
	Outcome       string
	CreatedAt     time.Time
	UpdatedAt     time.Time
}

// retiredAfter reports whether a was retired after b, tie-breaking on
// updated_at so re-stamped rows resolve deterministically.
func retiredAfter(a, b participant.Participant) bool {
	var at, bt time.Time
	if a.RetiredAt != nil {
		at = *a.RetiredAt
	}
	if b.RetiredAt != nil {
		bt = *b.RetiredAt
	}
	if !at.Equal(bt) {
		return at.After(bt)
	}
	return a.UpdatedAt.After(b.UpdatedAt)
}
