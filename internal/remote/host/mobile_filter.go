package host

import (
	"encoding/json"
	"strings"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/remote/wire"
)

func normalizeClientProfile(profile string) string {
	switch strings.TrimSpace(profile) {
	case wire.ClientProfileMobileChat, wire.ClientProfileMobileActivity:
		return strings.TrimSpace(profile)
	default:
		return ""
	}
}

func filterMobileChatLine(line []byte) ([]byte, bool) {
	return (mobileChatFilter{}).line(line)
}

type mobileChatFilter struct {
	tools    bool
	threadID string
}

func (f mobileChatFilter) line(line []byte) ([]byte, bool) {
	var env map[string]json.RawMessage
	if err := json.Unmarshal(line, &env); err != nil {
		return line, true
	}
	method := jsonString(env["method"])
	_, hasID := env["id"]
	switch {
	case method != "" && !hasID:
		return f.notification(env, method)
	case method == "":
		if f.response(env) {
			out, err := json.Marshal(env)
			if err == nil {
				return out, true
			}
		}
	}
	return line, true
}

func (f mobileChatFilter) notification(env map[string]json.RawMessage, method string) ([]byte, bool) {
	switch method {
	case appserver.NotificationThreadStarted,
		appserver.NotificationThreadResumed,
		appserver.NotificationThreadUpdated:
		if !f.threadParam(env) {
			return nil, false
		}
	case appserver.NotificationTurnStarted,
		appserver.NotificationTurnCompleted,
		appserver.NotificationTurnError:
		f.turnParam(env)
	case appserver.NotificationItemStarted,
		appserver.NotificationItemCompleted:
		if !f.itemParam(env) {
			return nil, false
		}
	case appserver.NotificationTurnQueued,
		appserver.NotificationTurnDequeued,
		appserver.NotificationTurnHeld,
		appserver.NotificationTurnSteered,
		appserver.NotificationTurnUnsteered,
		appserver.NotificationAgentMessageDelta,
		appserver.NotificationAgentMessageReplace,
		appserver.NotificationItemRemoved,
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

func (f mobileChatFilter) response(env map[string]json.RawMessage) bool {
	raw, ok := env["result"]
	if !ok || len(raw) == 0 || string(raw) == "null" {
		return false
	}
	var result map[string]json.RawMessage
	if err := json.Unmarshal(raw, &result); err != nil {
		return false
	}
	f.threadID = jsonString(result["thread_id"])
	changed := f.result(result)
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

func (f mobileChatFilter) result(result map[string]json.RawMessage) bool {
	changed := false
	if raw, ok := result["thread"]; ok {
		if slim, keep := f.thread(raw); keep {
			result["thread"] = slim
			changed = true
		}
	}
	if raw, ok := result["threads"]; ok {
		if slim, keep := f.threads(raw); keep {
			result["threads"] = slim
			changed = true
		}
	}
	if raw, ok := result["turn"]; ok {
		if slim, keep := f.turn(raw); keep {
			result["turn"] = slim
			changed = true
		}
	}
	if raw, ok := result["turns"]; ok {
		if slim, keep := f.turns(raw); keep {
			result["turns"] = slim
			changed = true
		}
	}
	return changed
}

func (f mobileChatFilter) threadParam(env map[string]json.RawMessage) bool {
	raw := env["params"]
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return true
	}
	thread, ok := params["thread"]
	if !ok {
		return true
	}
	slim, keep := f.thread(thread)
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

func (f mobileChatFilter) turnParam(env map[string]json.RawMessage) {
	raw := env["params"]
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return
	}
	turn, ok := params["turn"]
	if !ok {
		return
	}
	f.threadID = jsonString(params["thread_id"])
	slim, keep := f.turn(turn)
	if !keep {
		return
	}
	params["turn"] = slim
	out, err := json.Marshal(params)
	if err == nil {
		env["params"] = out
	}
}

func (f mobileChatFilter) itemParam(env map[string]json.RawMessage) bool {
	raw := env["params"]
	var params map[string]json.RawMessage
	if err := json.Unmarshal(raw, &params); err != nil {
		return true
	}
	item, ok := params["item"]
	if !ok {
		return true
	}
	f.threadID = jsonString(params["thread_id"])
	slim, keep := f.item(item, jsonString(params["turn_id"]))
	if !keep {
		return false
	}
	params["item"] = slim
	if out, err := json.Marshal(params); err == nil {
		env["params"] = out
	}
	return true
}

func (f mobileChatFilter) threads(raw json.RawMessage) (json.RawMessage, bool) {
	var threads []json.RawMessage
	if err := json.Unmarshal(raw, &threads); err != nil {
		return raw, false
	}
	out := make([]json.RawMessage, 0, len(threads))
	for _, thread := range threads {
		// The app-server owns list visibility. This profile only trims heavy content.
		slim, keep := f.thread(thread)
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

func (f mobileChatFilter) thread(raw json.RawMessage) (json.RawMessage, bool) {
	var thread map[string]json.RawMessage
	if err := json.Unmarshal(raw, &thread); err != nil {
		return raw, true
	}
	delete(thread, "child_agents")
	delete(thread, "browser_state")
	f.threadID = jsonString(thread["id"])
	if turnsRaw, ok := thread["turns"]; ok {
		if slim, keep := f.turns(turnsRaw); keep {
			thread["turns"] = slim
		}
	}
	data, err := json.Marshal(thread)
	if err != nil {
		return raw, true
	}
	return data, true
}

func (f mobileChatFilter) turns(raw json.RawMessage) (json.RawMessage, bool) {
	var turns []json.RawMessage
	if err := json.Unmarshal(raw, &turns); err != nil {
		return raw, false
	}
	result := make([]json.RawMessage, 0, len(turns))
	for _, turn := range turns {
		if slim, keep := f.turn(turn); keep {
			result = append(result, slim)
		}
	}
	data, err := json.Marshal(result)
	return data, err == nil
}

func (f mobileChatFilter) turn(raw json.RawMessage) (json.RawMessage, bool) {
	var turn map[string]json.RawMessage
	if err := json.Unmarshal(raw, &turn); err != nil {
		return raw, true
	}
	if itemsRaw, ok := turn["items"]; ok {
		var items []json.RawMessage
		if err := json.Unmarshal(itemsRaw, &items); err == nil {
			slimItems := make([]json.RawMessage, 0, len(items))
			for _, item := range items {
				if slim, keep := f.item(item, jsonString(turn["id"])); keep {
					slimItems = append(slimItems, slim)
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

func (f mobileChatFilter) item(raw json.RawMessage, turnID string) (json.RawMessage, bool) {
	var item map[string]json.RawMessage
	if err := json.Unmarshal(raw, &item); err != nil {
		return raw, true
	}
	switch appserver.ThreadItemType(jsonString(item["type"])) {
	case appserver.ThreadItemUserMessage,
		appserver.ThreadItemAgentMessage:
		return raw, true
	case appserver.ThreadItemToolCall:
		if !f.tools {
			return nil, false
		}
		var tool appserver.ThreadItem
		if err := json.Unmarshal(raw, &tool); err != nil {
			return nil, false
		}
		out, err := json.Marshal(appserver.RemoteThreadItem(f.threadID, turnID, tool))
		return out, err == nil
	default:
		return nil, false
	}
}

func jsonString(raw json.RawMessage) string {
	var s string
	_ = json.Unmarshal(raw, &s)
	return s
}
