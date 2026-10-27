package session

import (
	"time"
)

const HistoryCheckpointKindProviderRewrite = "provider_rewrite"

// HistoryCheckpoint is the current provider-history replacement. ThroughSeq is
// the physical message head covered by Replacement. Versions remain monotonic,
// but superseded payloads are discarded because no recovery path reads them.
type HistoryCheckpoint struct {
	SessionID   string          `json:"session_id"`
	Version     int             `json:"version"`
	Kind        string          `json:"kind"`
	ThroughSeq  int             `json:"through_seq"`
	Replacement []HistoryRecord `json:"replacement"`
	CreatedAt   time.Time       `json:"created_at"`
}

// ProviderHistorySnapshot is the latest exact replacement followed by any
// physical messages committed after that checkpoint. Checkpoint is nil for a
// legacy session that has never stored one.
type ProviderHistorySnapshot struct {
	Records    []HistoryRecord
	HeadSeq    int
	Checkpoint *HistoryCheckpoint
}

type historyCheckpointScanner interface {
	Scan(dest ...any) error
}
