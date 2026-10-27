package compactionservice

import (
	"fmt"
	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
	"strings"
)

const observedCompactSafetyDivisor = 5
const observedCompactMinMargin = 4_000

// Temporary extraction: remove the agent copy after selected compact consumers migrate.
func adaptiveBudget(messages []providers.ChatMessage, budget compact.Budget, hint BudgetHint) (compact.Budget, error) {
	if hint.Reason != "overflow" && hint.Reason != "new_context" {
		return budget, nil
	}
	messages = providers.ProjectMediaForPolicy(providers.ApplyToolResultProjections(messages), budget.MediaInput)

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
