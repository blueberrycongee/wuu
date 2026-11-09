package session

import (
	"time"
)

// TokenUsageRow is one persisted token_usage meta row. At is the recorded UTC
// time; legacy rows may have a zero At and still count toward totals.
type TokenUsageRow struct {
	SessionID           string
	At                  time.Time
	Provider            string
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
}
