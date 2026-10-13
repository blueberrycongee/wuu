package appserver

import (
	"encoding/json"
	"errors"
)

// ConversationActivity describes a contiguous folded region. Its address is the
// first source item, so paging older turns cannot change its identity.
type ConversationActivity struct {
	LastItemID  string `json:"last_item_id"`
	ItemCount   int    `json:"item_count"`
	ToolCount   int    `json:"tool_count"`
	FailedCount int    `json:"failed_count"`
}

// conversationTurn folds settled work before applying the history byte budget.
// Attachments remain at their original item address and publication position.
func conversationTurn(turn Turn) Turn {
	hasAnswer := false
	for _, item := range turn.Items {
		if item.Type == ThreadItemAgentMessage && item.Terminal && (item.Text != "" || len(item.Images) > 0 || len(item.MarkdownImages) > 0) {
			hasAnswer = true
		}
	}
	turn.HasFinalAnswer = hasAnswer
	if turn.Status == TurnStatusInProgress {
		return turn
	}
	items := make([]ThreadItem, 0, len(turn.Items))
	for _, item := range turn.Items {
		switch item.Type {
		case ThreadItemUserMessage:
			sessionMessage := (item.Origin == "host" || item.Origin == "plugin") && item.PresentationKind == "session_message"
			if !sessionMessage && (item.Name == "wuu_process_notification" || item.Name == "wuu_agent_notification") {
				continue
			}
		case ThreadItemAgentMessage, ThreadItemToolCall, ThreadItemError:
		default:
			continue
		}
		attachments := len(item.Files) > 0 || len(item.MarkdownImages) > 0 ||
			(item.Type != ThreadItemToolCall && len(item.Images) > 0) ||
			(item.Type == ThreadItemAgentMessage && len(markdownImageSources(item.Text)) > 0)
		if item.ResultDetail != nil {
			for _, part := range item.ResultDetail.Content {
				if part.Type == "image" && part.Artifact != nil && part.Artifact.Placement != "" {
					attachments = true
				}
			}
		}
		fold := !attachments && (item.Type == ThreadItemToolCall || (hasAnswer && item.Type == ThreadItemAgentMessage && !item.Terminal))
		if !fold {
			items = append(items, item)
			continue
		}
		if len(items) == 0 || items[len(items)-1].Activity == nil {
			items = append(items, ThreadItem{ID: item.ID, Type: ThreadItemProcessSummary, Status: ThreadItemStatusCompleted, Activity: &ConversationActivity{}})
		}
		summary := items[len(items)-1].Activity
		summary.LastItemID = item.ID
		summary.ItemCount++
		if item.Type == ThreadItemToolCall {
			summary.ToolCount++
		}
		if item.Status == ThreadItemStatusFailed {
			summary.FailedCount++
		}
	}
	turn.Items = items
	turn.ItemsView = TurnItemsViewConversation
	return turn
}

func conversationTurns(turns []Turn) []Turn {
	result := make([]Turn, len(turns))
	for i, turn := range turns {
		result[i] = conversationTurn(turn)
	}
	return result
}

func conversationSnapshot(thread Thread) Thread {
	thread.Turns = conversationTurns(thread.Turns)
	thread = pageThreadSnapshot(thread)
	if thread.HistoryCursor != "" {
		thread.HistoryCursor = "conversation:" + thread.HistoryCursor
	}
	return thread
}

// Activity pages run forward within one settled group, independently of the
// conversation cursor. Deleted or regrouped boundaries fail explicitly.
func (s *Server) handleThreadActivityRead(req Request) error {
	var params struct {
		ThreadID   string `json:"thread_id"`
		TurnID     string `json:"turn_id"`
		ItemID     string `json:"item_id"`
		LastItemID string `json:"last_item_id"`
		Cursor     string `json:"cursor"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	th, err := s.historyThread(params.ThreadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	th.mu.Lock()
	for _, turn := range th.Turns {
		if turn.ID != params.TurnID {
			continue
		}
		valid := false
		for _, item := range conversationTurn(turn).Items {
			if item.ID == params.ItemID && item.Activity != nil && item.Activity.LastItemID == params.LastItemID {
				valid = true
				break
			}
		}
		if !valid {
			break
		}
		start, end := -1, -1
		for i, item := range turn.Items {
			if item.ID == params.ItemID {
				start = i
			}
			if item.ID == params.LastItemID {
				end = i
			}
		}
		if start < 0 || end < start {
			break
		}
		if params.Cursor != "" {
			found := false
			for i := start; i <= end; i++ {
				if "item:"+turn.Items[i].ID == params.Cursor {
					start, found = i, true
					break
				}
			}
			if !found {
				break
			}
		}
		items := []ThreadItem{}
		size, cursor := 0, ""
		for i := start; i <= end; i++ {
			item := historyItem(th.ID, turn.ID, turn.Items[i])
			raw, _ := json.Marshal(item)
			if len(items) > 0 && (size+len(raw) > historyPageBytes || len(items) >= 100) {
				cursor = "item:" + item.ID
				break
			}
			items = append(items, item)
			size += len(raw)
		}
		turn.Items = items
		th.mu.Unlock()
		return s.writeResponse(req.ID, struct {
			ThreadID   string `json:"thread_id"`
			Turn       Turn   `json:"turn"`
			NextCursor string `json:"next_cursor,omitempty"`
		}{th.ID, turn, cursor}, nil)
	}
	th.mu.Unlock()
	return s.writeResponse(req.ID, nil, errors.New("activity changed; reopen the conversation"))
}
