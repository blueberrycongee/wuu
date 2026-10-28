package session

import (
	"time"
)

// InboxMessage is host-produced input waiting to reach a session, such as a
// managed session's result for its project coordinator. A message counts as
// delivered once its client ID appears in the target's persisted history, so
// delivery can be retried after a restart without duplicating the input.
type InboxMessage struct {
	ClientID         string
	SessionID        string
	RelatedSessionID string
	// Cause names the event for clients that render the delivered message.
	Cause   string
	Content string
	// Wake starts a turn when the target is idle. Other messages wait for the
	// target's next turn, whatever starts it.
	Wake      bool
	CreatedAt time.Time
	// Controls fence queued input across human takeover and return.
	Controls     []Control
	WorkID       string
	WorkRevision int
	WorkVersion  int
}
