package session

import (
	"time"
)

// CompactionNote is one plugin-owned, session-scoped Markdown continuation
// document. CoveredMessages and CoveredHash anchor the document to the exact
// provider-history prefix it summarizes without exposing it as a chat message.
type CompactionNote struct {
	SessionID       string
	ProviderKey     string
	Markdown        string
	CoveredMessages int
	CoveredHash     string
	UpdatedAt       time.Time
}
