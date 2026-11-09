package appserver

import (
	"encoding/json"
	"slices"
	"unicode/utf8"

	"github.com/blueberrycongee/wuu/internal/pluginhost"
)

const (
	pluginProgressItems     = 32
	pluginProgressBytes     = 32 * 1024
	pluginProgressTextBytes = 4096
)

// A shared turn is not an output attribution boundary: a steer can join a
// different request's turn. V1 deliberately requires both session ownership and
// one exclusively correlated input, rather than guessing which reply belongs
// to which sender. This samples existing state and owns no background resources.
func readPluginSessionProgress(th *threadState, pluginID, requestID, turnID string) *pluginhost.SessionProgressSnapshot {
	unavailable := &pluginhost.SessionProgressSnapshot{Version: 1, Reason: "turn_unavailable", Items: []pluginhost.SessionProgressItem{}}
	if th == nil {
		return unavailable
	}
	th.mu.Lock()
	defer th.mu.Unlock()
	if th.Owner != "plugin:"+pluginID {
		unavailable.Reason = "session_not_owned"
		return unavailable
	}
	if turnID == "" || requestID == "" {
		return unavailable
	}
	for turnIndex := len(th.Turns) - 1; turnIndex >= 0; turnIndex-- {
		turn := &th.Turns[turnIndex]
		if turn.ID != turnID {
			continue
		}
		matched := false
		for _, item := range turn.Items {
			if item.Type != ThreadItemUserMessage {
				continue
			}
			if item.SourceID != pluginSessionRequestClientID(pluginID, requestID) {
				unavailable.Reason = "request_not_exclusive"
				return unavailable
			}
			matched = true
		}
		if !matched {
			unavailable.Reason = "request_not_exclusive"
			return unavailable
		}
		result := &pluginhost.SessionProgressSnapshot{Version: 1, Available: true, SessionID: th.ID, RequestID: requestID, TurnID: turn.ID, Items: []pluginhost.SessionProgressItem{}}
		base, _ := json.Marshal(result)
		remaining := pluginProgressBytes - len(base)
		if remaining < 0 {
			return unavailable
		}
		for index := len(turn.Items) - 1; index >= 0; index-- {
			item := turn.Items[index]
			if item.Type != ThreadItemAgentMessage && item.Type != ThreadItemToolCall {
				continue
			}
			if len(result.Items) == pluginProgressItems {
				result.Truncated = true
				break
			}
			// Never truncate correlation IDs into aliases of another item.
			if len(item.ID) > 1024 {
				result.Truncated = true
				continue
			}
			projected := pluginhost.SessionProgressItem{ID: item.ID, Status: string(item.Status)}
			if item.Type == ThreadItemAgentMessage {
				projected.Type = "assistant"
				projected.Text, projected.Truncated = progressText(item.Text, pluginProgressTextBytes)
			} else {
				projected.Type = "tool"
				projected.Name, projected.Truncated = progressText(item.Name, 1024)
			}
			encoded, _ := json.Marshal(projected)
			// Include a separator even for the first item, conservatively bounding
			// actual JSON bytes (escaped controls can exceed UTF-8 source bytes).
			if len(encoded)+1 > remaining {
				result.Truncated = true
				// Keep the newest bounded suffix. Searching older items for a
				// smaller fit would make JSON work grow with the entire turn.
				break
			}
			remaining -= len(encoded) + 1
			result.Items = append(result.Items, projected)
			result.Truncated = result.Truncated || projected.Truncated
		}
		slices.Reverse(result.Items)
		return result
	}
	return unavailable
}

func progressText(text string, limit int) (string, bool) {
	if len(text) <= limit {
		return text, false
	}
	for limit > 0 && !utf8.RuneStart(text[limit]) {
		limit--
	}
	return text[:limit], true
}
