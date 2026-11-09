package agent

import (
	"context"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// ContextNoteMetric records a completed background attempt, independently of
// turn completion. Usage is provider-reported, not the estimated input budget.
type ContextNoteMetric struct {
	Model            string               `json:"model"`
	ProviderKey      string               `json:"provider_key"`
	StartedAt        time.Time            `json:"started_at"`
	DurationMS       int64                `json:"duration_ms"`
	Outcome          string               `json:"outcome"`
	SnapshotMessages int                  `json:"snapshot_messages"`
	LagMessages      int                  `json:"lag_messages"`
	Usage            providers.TokenUsage `json:"usage"`
}

// ContextNoteMetricStore is optional for in-memory runners. Persistent hosts
// record every attempted fork, including failed and superseded paid requests.
type ContextNoteMetricStore interface {
	RecordContextNoteMetric(context.Context, ContextNoteMetric) error
}
