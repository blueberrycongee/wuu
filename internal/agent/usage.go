package agent

import (
	"sync"

	"github.com/blueberrycongee/wuu/internal/contextbudget"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// UsageTracker estimates how many tokens of context the current
// conversation occupies. It is provider-agnostic by design: instead of
// shipping a tokenizer per model, it treats the most recent successful
// API response's `usage.input_tokens + output_tokens` as ground truth
// for everything sent up to and including that round, then adds a
// cheap local estimate for messages added afterwards (the user's next
// prompt + tool results that haven't been sent yet).
//
// Errors are bounded: the estimate only ever covers messages from the LAST
// round onward, so the worst-case undercount is one turn's delta. The next
// successful API call collapses the delta to zero by overwriting the ground
// truth.
//
// UsageTracker is safe for concurrent reads/writes.
type UsageTracker struct {
	mu sync.Mutex
	// lastResponseTotal is the most recent (input+output) token count
	// reported by the provider. Zero means no successful round yet.
	lastResponseTotal int
	// lastSuccessfulRequestTokens is the provider-reported model input for the
	// same response (uncached + cache creation + cache read), excluding generated
	// output. Reactive compaction treats this as the accepted full-request lower
	// bound rather than conflating request size with post-generation occupancy.
	lastSuccessfulRequestTokens int
	// lastResponseContract identifies the provider/model contract that produced
	// both observations. Persisted seeds intentionally leave this empty: they are
	// useful for display and proactive estimates, but must not be treated as a
	// safe lower bound after a model or provider switch.
	lastResponseContract string
	// pendingDelta is an estimate of tokens added to `messages` since
	// the last response was recorded. Reset every time RecordResponse
	// is called.
	pendingDelta int
	// adjustment explains which state transition produced the current
	// baseline/delta pair. It is metadata-only and surfaces in compact traces.
	adjustment UsageAdjustment
}

// UsageAdjustment describes the most recent tracker transition relevant to a
// compact decision. Values are stable because they are persisted in traces.
type UsageAdjustment string

const (
	UsageAdjustmentProviderResponse          UsageAdjustment = "provider_response"
	UsageAdjustmentSeededGroundTruth         UsageAdjustment = "seeded_ground_truth"
	UsageAdjustmentInitialHistoryEstimate    UsageAdjustment = "initial_history_estimate"
	UsageAdjustmentLengthReset               UsageAdjustment = "length_reset"
	UsageAdjustmentProviderContractReset     UsageAdjustment = "provider_contract_reset"
	UsageAdjustmentRequestShapeReset         UsageAdjustment = "request_shape_reset"
	UsageAdjustmentRequestShapeTailRebase    UsageAdjustment = "request_shape_tail_rebase"
	UsageAdjustmentExternalRewriteSeed       UsageAdjustment = "external_rewrite_seed"
	UsageAdjustmentExternalRewriteEstimate   UsageAdjustment = "external_rewrite_estimate"
	UsageAdjustmentCompactionRewriteEstimate UsageAdjustment = "compaction_rewrite_estimate"
	UsageAdjustmentRuntimeRebuildSeed        UsageAdjustment = "runtime_rebuild_seed"
	UsageAdjustmentProviderOverflow          UsageAdjustment = "provider_overflow"
)

// UsageBreakdown is an atomic snapshot of the values behind EstimateCurrent.
// It lets compact diagnostics explain whether a trigger came from provider
// ground truth or locally estimated pending messages.
type UsageBreakdown struct {
	LastResponseTotal int
	PendingDelta      int
	Adjustment        UsageAdjustment
}

func (b UsageBreakdown) Total() int {
	return b.LastResponseTotal + b.PendingDelta
}

// NewUsageTracker constructs a fresh tracker with no recorded usage.
func NewUsageTracker() *UsageTracker {
	return &UsageTracker{}
}

// RecordResponse stores the per-call usage from a successful API
// response. Callers should pass the same numbers they hand to
// LoopConfig.OnUsage. nil is a no-op.
//
// The "total" we store here uses TokenUsage.TotalContextTokens(),
// which includes CacheReadTokens. Cached tokens still occupy the
// model's context window, so dropping them would make a Claude
// session with prompt caching look almost empty and the auto-compact
// trigger would never fire.
func (t *UsageTracker) RecordResponse(usage *providers.TokenUsage) {
	t.RecordResponseForContract("", usage)
}

// RecordResponseForContract stores successful provider usage together with the
// provider/model contract that accepted it. Reactive compaction uses this
// identity to avoid carrying a large-model observation across a model switch.
func (t *UsageTracker) RecordResponseForContract(contract string, usage *providers.TokenUsage) {
	if t == nil || usage == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastResponseTotal = usage.TotalContextTokens()
	t.lastSuccessfulRequestTokens = usage.InputTokens + usage.CacheCreationTokens + usage.CacheReadTokens
	t.lastResponseContract = contract
	// The new ground truth already includes everything we'd been
	// estimating, so the pending delta resets to zero.
	t.pendingDelta = 0
	t.adjustment = UsageAdjustmentProviderResponse
}

// RecordPendingMessages adds an estimate for messages that have been
// queued onto the conversation since the last response. This is what
// the loop calls after appending the assistant's tool calls + tool
// result messages within a single round, before the next request.
func (t *UsageTracker) RecordPendingMessages(msgs []providers.ChatMessage) {
	if t == nil || len(msgs) == 0 {
		return
	}
	add := estimateMessages(msgs)
	t.mu.Lock()
	defer t.mu.Unlock()
	t.pendingDelta += add
}

// EstimateCurrent returns the best-effort current token usage of the
// conversation: ground truth from the last response plus the pending
// delta accumulated since.
func (t *UsageTracker) EstimateCurrent() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastResponseTotal + t.pendingDelta
}

// Breakdown returns an atomic diagnostic snapshot of the tracker.
func (t *UsageTracker) Breakdown() UsageBreakdown {
	if t == nil {
		return UsageBreakdown{}
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return UsageBreakdown{
		LastResponseTotal: t.lastResponseTotal,
		PendingDelta:      t.pendingDelta,
		Adjustment:        t.adjustment,
	}
}

// LastResponseTotal returns the most recently recorded ground truth.
// Useful for tests and diagnostics.
func (t *UsageTracker) LastResponseTotal() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.lastResponseTotal
}

// LastSuccessfulRequestTokensForContract returns the provider-reported full
// model input only when it belongs to the requested provider/model contract.
func (t *UsageTracker) LastSuccessfulRequestTokensForContract(contract string) int {
	if t == nil || contract == "" {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastResponseContract != contract {
		return 0
	}
	return t.lastSuccessfulRequestTokens
}

// HasGroundTruth reports whether the tracker has seen at least one
// successful provider response and therefore holds a real usage
// baseline instead of pure local estimates.
func (t *UsageTracker) HasGroundTruth() bool {
	return t.LastResponseTotal() > 0
}

// PendingDelta returns the current estimate for messages added since
// the last response. Useful for tests and diagnostics.
func (t *UsageTracker) PendingDelta() int {
	if t == nil {
		return 0
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.pendingDelta
}

// ObserveProviderOverflow raises the tracker to a provider-reported overflow
// count so the next request does not keep trusting an undercount.
func (t *UsageTracker) ObserveProviderOverflow(promptTokens, windowTokens int) {
	if t == nil || promptTokens <= 0 {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if promptTokens > t.lastResponseTotal {
		t.lastResponseTotal = promptTokens
	}
	if promptTokens > t.lastSuccessfulRequestTokens {
		t.lastSuccessfulRequestTokens = promptTokens
	}
	if windowTokens > 0 && promptTokens > windowTokens && t.pendingDelta < promptTokens-windowTokens {
		t.pendingDelta = promptTokens - windowTokens
	}
	t.adjustment = UsageAdjustmentProviderOverflow
}

// SeedGroundTruth primes the tracker with a persisted retained-context total
// when no live state exists yet. It reports whether the seed was applied:
// a tracker that already holds a baseline or pending estimates keeps its
// (fresher) live state untouched.
func (t *UsageTracker) SeedGroundTruth(total int) bool {
	if t == nil || total <= 0 {
		return false
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	if t.lastResponseTotal > 0 || t.pendingDelta > 0 {
		return false
	}
	t.lastResponseTotal = total
	t.lastSuccessfulRequestTokens = 0
	t.lastResponseContract = ""
	t.adjustment = UsageAdjustmentSeededGroundTruth
	return true
}

// SetAdjustment records why the current baseline/delta pair was adopted.
func (t *UsageTracker) SetAdjustment(adjustment UsageAdjustment) {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.adjustment = adjustment
}

// Reset clears all recorded state. Used after a compact pass replaces
// the conversation history with a summary.
func (t *UsageTracker) Reset() {
	if t == nil {
		return
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lastResponseTotal = 0
	t.lastSuccessfulRequestTokens = 0
	t.lastResponseContract = ""
	t.pendingDelta = 0
	t.adjustment = ""
}

// Clone returns a snapshot copy of the current tracker state. Useful
// when a caller wants to speculate on one run and only commit the
// updated usage state if that run's history is actually adopted.
func (t *UsageTracker) Clone() *UsageTracker {
	if t == nil {
		return nil
	}
	t.mu.Lock()
	defer t.mu.Unlock()
	return &UsageTracker{
		lastResponseTotal:           t.lastResponseTotal,
		lastSuccessfulRequestTokens: t.lastSuccessfulRequestTokens,
		lastResponseContract:        t.lastResponseContract,
		pendingDelta:                t.pendingDelta,
		adjustment:                  t.adjustment,
	}
}

// estimateMessages computes a rough token count for a slice of chat
// messages. Used only for the delta between API calls — never as
// ground truth. The heuristic is intentionally cheap and slightly
// pessimistic so it tends to over-count rather than miss a compact
// trigger.
func estimateMessages(msgs []providers.ChatMessage) int {
	return contextbudget.EstimateMessagesTokens(msgs)
}
