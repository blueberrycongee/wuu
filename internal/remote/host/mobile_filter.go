package host

import (
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

func normalizeClientProfile(profile string) string {
	switch strings.TrimSpace(profile) {
	case wire.ClientProfileMobileChat:
		return wire.ClientProfileMobileChat
	default:
		return ""
	}
}

func filterMobileChatLine(line []byte) ([]byte, bool) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(line, &env); err != nil {
		return line, true
	}
	method := jsonString(env["method"])
	_, hasID := env["id"]
	switch {
	case method != "" && !hasID:
		return filterMobileChatNotification(env, method)
	case method == "":
		if slimMobileChatResponse(env) {
			out, err := json.Marshal(env)
			if err == nil {
				return out, true
			}
		}
	}
	return line, true
}

func filterMobileChatNotification(env map[string]json.RawMessage, method string) ([]byte, bool) {
	switch method {
	case appserver.NotificationThreadStarted,
		appserver.NotificationThreadResumed,
		appserver.NotificationThreadUpdated:
		if !slimMobileChatThreadParam(env) {
			return nil, false
		}
	case appserver.NotificationTurnStarted,
		appserver.NotificationTurnCompleted,
		appserver.NotificationTurnError:
		slimMobileChatTurnParam(env)
	case appserver.NotificationItemStarted,
		appserver.NotificationItemCompleted:
		if !slimMobileChatItemParam(env) {
			return nil, false
		}
	case appserver.NotificationTurnQueued,
		appserver.NotificationTurnDequeued,
		appserver.NotificationUserQuestionRequested,
		appserver.NotificationUserQuestionResolved:
		// Direct chat state.
	default:
		return nil, false
	}
	out, err := json.Marshal(env)
	if err != nil {
		return nil, false
	}
	return out, true
}

func slimMobileChatResponse(env map[string]json.RawMessage) bool {
	raw, ok := env["result"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	changed := slimMobileChatResult(result)
	if !changed {
		return false
	}
	out, err := json.Marshal(result)
	if err != nil {
		return false
	}
	env["result"] = out
	return true
}

func slimMobileChatResult(result map[string]json.RawMessage) bool {
	changed := false
	if raw, ok := result["thread"]; ok {
		if slim, keep := slimMobileChatThread(raw); keep {
			result["thread"] = slim
			changed = true
		}
	}
	if raw, ok := result["threads"]; ok {
		if slim, keep := slimMobileChatThreads(raw); keep {
			result["threads"] = slim
			changed = true
		}
	}
	if raw, ok := result["turn"]; ok {
		if slim, keep := slimMobileChatTurn(raw); keep {
			result["turn"] = slim
			changed = true
		}
	}
	return changed
}

func slimMobileChatThreadParam(env map[string]json.RawMessage) bool {
	raw := env["params"]
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return true
	}
	thread, ok := params["thread"]
	if !ok {
		return true
	}
	if !isMobileChatThread(thread) {
		return false
	}
	slim, keep := slimMobileChatThread(thread)
	if !keep {
		return false
	}
	params["thread"] = slim
	out, err := json.Marshal(params)
	if err == nil {
		env["params"] = out
	}
	return true
}

func slimMobileChatTurnParam(env map[string]json.RawMessage) {
	raw := env["params"]
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	turn, ok := params["turn"]
	if !ok {
		return
	}
	slim, keep := slimMobileChatTurn(turn)
	if !keep {
		return
	}
	params["turn"] = slim
	out, err := json.Marshal(params)
	if err == nil {
		env["params"] = out
	}
}

func slimMobileChatItemParam(env map[string]json.RawMessage) bool {
	raw := env["params"]
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return true
	}
	item, ok := params["item"]
	if !ok {
		return true
	}
	if !isMobileChatItem(item) {
		return false
	}
	return true
}

func slimMobileChatThreads(raw json.RawMessage) (json.RawMessage, bool) {
	var threads []json.RawMessage
	if err := json.Unmarshal(raw, &threads); err != nil {
		return raw, false
	}
	out := make([]json.RawMessage, 0, len(threads))
	for _, thread := range threads {
		if !isMobileChatThread(thread) {
			continue
		}
		slim, keep := slimMobileChatThread(thread)
		if keep {
			out = append(out, slim)
		}
	}
	data, err := json.Marshal(out)
	if err != nil {
		return raw, false
	}
	return data, true
}

func slimMobileChatThread(raw json.RawMessage) (json.RawMessage, bool) {
	var thread map[string]json.RawMessage
	if err := json.Unmarshal(raw, &thread); err != nil {
		return raw, true
	}
	delete(thread, "child_agents")
	delete(thread, "browser_state")
	if turnsRaw, ok := thread["turns"]; ok {
		var turns []json.RawMessage
		if err := json.Unmarshal(turnsRaw, &turns); err == nil {
			slimTurns := make([]json.RawMessage, 0, len(turns))
			for _, turn := range turns {
				if slim, keep := slimMobileChatTurn(turn); keep {
					slimTurns = append(slimTurns, slim)
				}
			}
			if data, err := json.Marshal(slimTurns); err == nil {
				thread["turns"] = data
			}
		}
	}
	data, err := json.Marshal(thread)
	if err != nil {
		return raw, true
	}
	return data, true
}

func slimMobileChatTurn(raw json.RawMessage) (json.RawMessage, bool) {
	var turn map[string]json.RawMessage
	if err := json.Unmarshal(raw, &turn); err != nil {
		return raw, true
	}
	if itemsRaw, ok := turn["items"]; ok {
		var items []json.RawMessage
		if err := json.Unmarshal(itemsRaw, &items); err == nil {
			slimItems := make([]json.RawMessage, 0, len(items))
			for _, item := range items {
				if isMobileChatItem(item) {
					slimItems = append(slimItems, item)
				}
			}
			if data, err := json.Marshal(slimItems); err == nil {
				turn["items"] = data
			}
		}
	}
	data, err := json.Marshal(turn)
	if err != nil {
		return raw, true
	}
	return data, true
}

func isMobileChatThread(raw json.RawMessage) bool {
	return false
}

func isMobileChatItem(raw json.RawMessage) bool {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return true
	}
	switch appserver.ThreadItemType(jsonString(item["type"])) {
	case appserver.ThreadItemUserMessage,
		appserver.ThreadItemAgentMessage:
		return true
	default:
		return false
	}
}

func jsonString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}
