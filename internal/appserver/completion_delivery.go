package appserver

import (
	"errors"
	"slices"

	"github.com/blueberrycongee/wuu/internal/providers"
)

var errCompletionDeliveryPaused = errors.New("completion delivery is paused after a failed turn; continue the conversation to retry")

// completionDeliveryPaused keeps automatic replay from reopening a terminal
// failure. Request recovery already owns retry classification, backoff and its
// attempt budget; another turn must not reset that budget. Use the durable turn
// projection so reconnecting or rebuilding a runtime cannot restart delivery.
// A later human message permits recovery, while the delivery itself stays pending.
func (th *threadState) completionDeliveryPaused(agentIDs, processIDs []string) bool {
	if len(agentIDs) == 0 && len(processIDs) == 0 {
		return false
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	for i := len(th.Turns) - 1; i >= 0; i-- {
		turn := th.Turns[i]
		if turn.Status == TurnStatusFailed || turn.Status == TurnStatusInterrupted {
			for _, item := range turn.Items {
				if item.Type != ThreadItemUserMessage {
					continue
				}
				for _, id := range agentCompletionResultIDs(item.SourceID) {
					if slices.Contains(agentIDs, id) {
						return true
					}
				}
				for _, id := range processCompletionIDs(item.SourceID) {
					if slices.Contains(processIDs, id) {
						return true
					}
				}
			}
		}
		for _, item := range turn.Items {
			if item.Type == ThreadItemUserMessage && isHumanUserMessage(providers.ChatMessage{
				Role: "user", Origin: item.Origin, ReadOnly: item.ReadOnly,
				Name: item.Name, ClientID: item.SourceID, Content: item.Text,
			}) {
				return false
			}
		}
	}
	return false
}
