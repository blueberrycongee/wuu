package appserver

import (
	"encoding/json"
	"slices"
	"strings"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
)

const completionAnswerClientIDPrefix = "wuu-completion-answer:"

type completionAnswerReceipts struct {
	Agents    []string `json:"agents,omitempty"`
	Processes []string `json:"processes,omitempty"`
}

func completionReceipts(clientID string) completionAnswerReceipts {
	clientID = strings.TrimSpace(clientID)
	switch {
	case strings.HasPrefix(clientID, agentCompletionAnswerClientIDPrefix):
		return completionAnswerReceipts{Agents: splitAgentCompletionResultIDs(strings.TrimPrefix(clientID, agentCompletionAnswerClientIDPrefix))}
	case strings.HasPrefix(clientID, processCompletionAnswerClientIDPrefix):
		return completionAnswerReceipts{Processes: splitAgentCompletionResultIDs(strings.TrimPrefix(clientID, processCompletionAnswerClientIDPrefix))}
	case strings.HasPrefix(clientID, completionAnswerClientIDPrefix):
		var receipts completionAnswerReceipts
		if json.Unmarshal([]byte(strings.TrimPrefix(clientID, completionAnswerClientIDPrefix)), &receipts) == nil {
			return receipts
		}
	}
	return completionAnswerReceipts{}
}

func markCompletionAnswer(res *agent.LoopResult, receipts completionAnswerReceipts) bool {
	if res == nil || len(res.NewMessages) == 0 {
		return false
	}
	receipts.Agents = uniqueSortedCompletionIDs(receipts.Agents)
	receipts.Processes = uniqueSortedCompletionIDs(receipts.Processes)
	if len(receipts.Agents)+len(receipts.Processes) == 0 {
		return false
	}
	marked := false
	// The durable transcript is a separate copy and is what restart recovery
	// reads. One answer may consume both child-agent and process results.
	for _, messages := range [][]providers.ChatMessage{res.NewMessages, res.DurableNewMessages} {
		markerIndex := -1
		for i, msg := range messages {
			for _, id := range agentCompletionResultIDs(msg.ClientID) {
				if slices.Contains(receipts.Agents, id) {
					markerIndex = i
				}
			}
			for _, id := range processCompletionIDs(msg.ClientID) {
				if slices.Contains(receipts.Processes, id) {
					markerIndex = i
				}
			}
		}
		for i := len(messages) - 1; i > markerIndex; i-- {
			msg := &messages[i]
			if !strings.EqualFold(strings.TrimSpace(msg.Role), "assistant") || strings.TrimSpace(msg.Content) == "" {
				continue
			}
			merged := completionReceipts(msg.ClientID)
			merged.Agents = uniqueSortedCompletionIDs(append(merged.Agents, receipts.Agents...))
			merged.Processes = uniqueSortedCompletionIDs(append(merged.Processes, receipts.Processes...))
			switch {
			case len(merged.Processes) == 0:
				msg.ClientID = agentCompletionAnswerClientIDPrefix + strings.Join(merged.Agents, ",")
			case len(merged.Agents) == 0:
				msg.ClientID = processCompletionAnswerClientIDPrefix + strings.Join(merged.Processes, ",")
			default:
				data, _ := json.Marshal(merged)
				msg.ClientID = completionAnswerClientIDPrefix + string(data)
			}
			marked = true
			break
		}
	}
	return marked
}
