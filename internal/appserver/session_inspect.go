package appserver

import (
	"context"

	"github.com/blueberrycongee/wuu/internal/session"
)

// sessionTranscriptExcerpt reads a bounded recent slice of a session's settled
// history plus the running turn's live progress, for a managing agent that
// must assess evidence without loading the whole transcript.
func (s *Server) sessionTranscriptExcerpt(ctx context.Context, id, query string, before, limit int) (map[string]any, error) {
	if limit < 1 || limit > 30 {
		limit = 8
	}
	var page session.HistoryPage
	var err error
	if query != "" {
		page, err = session.SearchHistoryQuery(ctx, s.rt.SessionDir, session.HistorySearchQuery{SessionID: id, Query: query, BeforeSeq: before, Limit: limit})
	} else {
		head, readErr := session.ReadHistoryPage(ctx, s.rt.SessionDir, id, 1, 1)
		if readErr != nil {
			return nil, readErr
		}
		end := head.HeadSeq
		if before > 0 && before <= end {
			end = before - 1
		}
		start := max(1, end-limit+1)
		page, err = session.ReadHistoryQuery(ctx, s.rt.SessionDir, session.HistoryReadQuery{SessionID: id, StartSeq: start, EndSeq: end, Limit: limit})
		page.HasMore = start > 1
		if page.HasMore {
			page.Next = &session.HistoryCursor{SessionID: id, SnapshotSeq: head.HeadSeq, Seq: start}
		}
	}
	if err != nil {
		return nil, err
	}
	items := make([]map[string]any, 0, len(page.Records))
	for _, r := range page.Records {
		if r.Role == "system" || r.Hidden {
			continue
		}
		items = append(items, map[string]any{"seq": r.Seq, "role": r.Role, "name": r.Name, "content": excerpt(r.Content, 1600), "tool_calls": excerpt(string(r.ToolCalls), 1600), "tool_result": excerpt(string(r.ToolResult), 1600), "truncated": len([]rune(r.Content)) > 1600})
	}
	progress := make([]map[string]any, 0)
	if th := s.thread(id); th != nil {
		th.mu.Lock()
		if th.running && len(th.Turns) > 0 {
			current := th.Turns[len(th.Turns)-1]
			for _, item := range current.Items[max(0, len(current.Items)-limit):] {
				if item.Type == ThreadItemReasoning {
					continue
				}
				progress = append(progress, map[string]any{"turn_id": current.ID, "type": item.Type, "status": item.Status, "name": item.Name, "text": excerpt(item.Text, 1600), "arguments": excerpt(item.Arguments, 1600), "result": excerpt(item.Result, 1600), "error": excerpt(item.Error, 1600)})
			}
		}
		th.mu.Unlock()
	}
	return map[string]any{"history": items, "live_progress": progress, "history_scope": "History contains settled turns; a running session may have uncommitted progress on its executing host. Missing history is not evidence of a failed start.", "page": map[string]any{"has_more": page.HasMore, "next": page.Next}}, nil
}

func excerpt(text string, limit int) string {
	r := []rune(text)
	if len(r) > limit {
		return string(r[:limit]) + "\n[excerpt]"
	}
	return text
}
