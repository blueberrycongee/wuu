package session

import (
	"time"
)

const (
	turnTerminalContent = "turn_terminal"
	tokenUsageContent   = "token_usage"
)

// SubscriptionActivityKey selects one dashboard source. EngineID matches an
// external engine binding. Provider matches the built-in model service, which
// keeps its own credentials and is not an engine binding.
type SubscriptionActivityKey struct {
	EngineID string
	Provider string
}

// SubscriptionActivity is the newest settled request this store can prove for
// one source. Status and Error come from its turn_terminal row. Usage is
// attached only from the same request boundary and recorded provider. Legacy
// usage without a terminal has unknown status; missing usage is not zero usage.
type SubscriptionActivity struct {
	Key                 SubscriptionActivityKey
	Status              string
	Error               string
	At                  time.Time
	Model               string
	InputTokens         int
	OutputTokens        int
	CacheCreationTokens int
	CacheReadTokens     int
	UsageReported       bool
	LocalUsage          SubscriptionUsage
}

// SubscriptionUsage totals retained Wuu token records, not account billing.
type SubscriptionUsage struct {
	InputTokens         int `json:"input_tokens"`
	OutputTokens        int `json:"output_tokens"`
	CacheCreationTokens int `json:"cache_creation_tokens"`
	CacheReadTokens     int `json:"cache_read_tokens"`
	ReportedTurns       int `json:"reported_turns"`
}
