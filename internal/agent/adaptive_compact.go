package agent

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"fmt"
	"net/url"
	"strings"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/contextbudget"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const (
	observedCompactSafetyDivisor = 5
	observedCompactMinMargin     = 4_000
)

type compactBudgetHint struct {
	Reason               CompactReason
	LastSuccessfulTokens int
	FailedRequestTokens  int
	TargetTotalTokens    int
}

type compactBudgetHintContextKey struct{}

func withCompactBudgetHint(ctx context.Context, hint compactBudgetHint) context.Context {
	return context.WithValue(ctx, compactBudgetHintContextKey{}, hint)
}

func compactBudgetHintFromContext(ctx context.Context) (compactBudgetHint, bool) {
	if ctx == nil {
		return compactBudgetHint{}, false
	}
	hint, ok := ctx.Value(compactBudgetHintContextKey{}).(compactBudgetHint)
	return hint, ok
}

// BuildProviderObservationKey identifies the configured provider instance and
// the endpoint/protocol contract implemented by its client. The returned value
// is opaque so endpoint details do not leak into traces or persisted usage.
func BuildProviderObservationKey(providerInstance, endpoint string, protocolParts ...string) string {
	parts := []string{strings.TrimSpace(providerInstance), normalizeObservationEndpoint(endpoint)}
	for _, part := range protocolParts {
		parts = append(parts, strings.TrimSpace(part))
	}
	joined := strings.Join(parts, "\x00")
	if strings.Trim(joined, "\x00") == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(joined))
	return fmt.Sprintf("%x", sum[:16])
}

func normalizeObservationEndpoint(endpoint string) string {
	normalized := strings.TrimSpace(endpoint)
	parsed, err := url.Parse(normalized)
	if err != nil || parsed.Host == "" {
		return normalized
	}
	parsed.Scheme = strings.ToLower(parsed.Scheme)
	parsed.Host = strings.ToLower(parsed.Host)
	return parsed.String()
}

// ProviderUsageNormalizationKey isolates observations whose raw provider usage
// fields are normalized with different semantics before reaching UsageTracker.
func ProviderUsageNormalizationKey(cacheCreationOmitted, inputIncludesCacheRead bool) string {
	return fmt.Sprintf("usage-cache-creation-omitted=%t;input-includes-cache-read=%t", cacheCreationOmitted, inputIncludesCacheRead)
}

func usageContractKey(providerObservationKey, provider, model, variant, effort string, options map[string]any) string {
	parts := []string{
		strings.TrimSpace(providerObservationKey),
		strings.TrimSpace(provider),
		strings.TrimSpace(model),
		strings.TrimSpace(variant),
		strings.TrimSpace(effort),
	}
	if raw, err := json.Marshal(options); err == nil {
		parts = append(parts, string(raw))
	} else {
		// Provider options are expected to be JSON-compatible, but a custom
		// extension can still supply another Go value. Keep it in the opaque hash
		// instead of silently collapsing distinct contracts.
		parts = append(parts, fmt.Sprintf("non-json:%#v", options))
	}
	joined := strings.Join(parts, "\x00")
	if strings.Trim(joined, "\x00") == "" {
		return ""
	}
	sum := sha256.Sum256([]byte(joined))
	return fmt.Sprintf("%x", sum[:16])
}

func reactiveCompactTarget(threshold, lastSuccessful int) int {
	target := threshold
	if lastSuccessful <= 0 {
		return target
	}
	margin := lastSuccessful / observedCompactSafetyDivisor
	if margin < observedCompactMinMargin {
		margin = observedCompactMinMargin
	}
	if margin >= lastSuccessful {
		margin = lastSuccessful / 2
	}
	observedTarget := lastSuccessful - margin
	if observedTarget > 0 && (target <= 0 || observedTarget < target) {
		target = observedTarget
	}
	return target
}

func estimateOutboundRequestTokens(req providers.ChatRequest) int {
	tokens := estimateMessages(req.Messages)
	for _, message := range req.Messages {
		if len(message.DiscoveredTools) == 0 {
			continue
		}
		if encoded, err := json.Marshal(message.DiscoveredTools); err == nil {
			tokens += contextbudget.EstimateJSONTokens(string(encoded))
		}
	}
	// Tool schemas are JSON-heavy. Match contextbudget's JSON estimator
	// without serializing the definitions a second time.
	if toolBytes := toolSchemaBytesForRequestShape(req.Tools); toolBytes > 0 {
		tokens += toolBytes*contextbudget.JSONNonCJKTokenNumerator/contextbudget.JSONNonCJKTokenDenominator + 1
	}
	return tokens
}

func applyAdaptiveCompactBudget(ctx context.Context, messages []providers.ChatMessage, budget compact.Budget) (compact.Budget, error) {
	hint, ok := compactBudgetHintFromContext(ctx)
	if !ok || (hint.Reason != CompactReasonOverflow && hint.Reason != CompactReasonNewContext) {
		return budget, nil
	}

	// An unknown model should not start at the legacy 4k summary-input floor.
	// Use half of the same-contract successful lower bound when available, or
	// half of the overflowing request as a first probe, capped by compact's 80k
	// summary-input ceiling. Compact geometrically backs off if that probe is too
	// large for the active provider/model contract.
	probeBase := hint.LastSuccessfulTokens
	if probeBase <= 0 {
		probeBase = hint.FailedRequestTokens
	}
	if probeBase > 0 {
		knownInputWindow := budget.InputTokens
		if knownInputWindow <= 0 && budget.ContextTokens > 0 {
			knownInputWindow = budget.ContextTokens - budget.OutputReserveTokens
		}
		if knownInputWindow > 0 {
			budget.SummaryInputTokens = knownInputWindow / 2
		} else {
			budget.SummaryInputTokens = probeBase / 2
		}
		if budget.SummaryInputTokens < 1 {
			budget.SummaryInputTokens = 1
		}
		if budget.SummaryInputTokens > compact.SummaryInputMaxTokens {
			budget.SummaryInputTokens = compact.SummaryInputMaxTokens
		}
	}

	if hint.TargetTotalTokens <= 0 && hint.FailedRequestTokens > 0 {
		// The failed request is negative rather than safe-bound evidence, but it
		// still tells an unknown-model first retry to be materially smaller instead
		// of retaining a provider-neutral fixed tail that may exceed the window.
		margin := max(hint.FailedRequestTokens/observedCompactSafetyDivisor, observedCompactMinMargin)
		if margin >= hint.FailedRequestTokens {
			margin = hint.FailedRequestTokens / 2
		}
		candidate := hint.FailedRequestTokens - margin
		minimumUsefulTarget := protectedDurableTokens(messages) + compact.SummaryReserveTokens(budget)
		if candidate > minimumUsefulTarget {
			hint.TargetTotalTokens = candidate
		}
	}
	if hint.TargetTotalTokens <= 0 {
		return budget, nil
	}
	currentHistoryTokens := compact.EstimateMessagesTokens(messages)
	fixedRequestTokens := hint.FailedRequestTokens - currentHistoryTokens
	if fixedRequestTokens < 0 {
		fixedRequestTokens = 0
	}
	protectedTokens := protectedDurableTokens(messages)
	requiredTokens := fixedRequestTokens + protectedTokens + compact.SummaryReserveTokens(budget)
	if requiredTokens >= hint.TargetTotalTokens {
		return budget, fmt.Errorf(
			"context overflow cannot be recovered by compacting old history: fixed request surface, current turn, and summary reserve require about %d tokens, exceeding the %d-token safe retry target; reduce the system prompt, tool surface, attachments, or current request",
			requiredTokens,
			hint.TargetTotalTokens,
		)
	}
	budget.TargetTokens = hint.TargetTotalTokens - fixedRequestTokens
	if budget.TargetTokens < 1 {
		budget.TargetTokens = 1
	}
	return budget, nil
}

func protectedDurableTokens(messages []providers.ChatMessage) int {
	total := 0
	for _, msg := range messages {
		if !strings.EqualFold(strings.TrimSpace(msg.Role), "system") {
			break
		}
		if !compact.IsConversationSummaryContent(msg.Content) {
			total += compact.EstimateMessagesTokens([]providers.ChatMessage{msg})
		}
	}
	for i := len(messages) - 1; i >= 0; i-- {
		if strings.EqualFold(strings.TrimSpace(messages[i].Role), "user") {
			return total + compact.EstimateMessagesTokens(messages[i:i+1])
		}
	}
	return total
}
