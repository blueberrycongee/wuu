package appserver

import (
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"errors"
	"fmt"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/session"
)

const historyPageTurns = 20
const historyPageBytes = 256 * 1024

// The cursor names the first retained turn, so appends do not shift older
// pages. A removed boundary fails explicitly instead of silently skipping data.
// Pages may split a turn at item boundaries. Oversized individual items retain
// a preview and an address for their complete content.
func historyPage(threadID string, turns []Turn, endTurn, endItem int) ([]Turn, string) {
	result := []Turn{}
	size := 0
	for ti := endTurn; ti >= 0 && len(result) < historyPageTurns; ti-- {
		turn := turns[ti]
		limit := len(turn.Items)
		if ti == endTurn {
			limit = endItem
		}
		turn.Items = []ThreadItem{}
		for ii := limit - 1; ii >= 0; ii-- {
			item := historyItem(threadID, turns[ti].ID, turns[ti].Items[ii])
			raw, _ := json.Marshal(item)
			if size > 0 && size+len(raw) > historyPageBytes {
				if len(turn.Items) > 0 {
					result = append([]Turn{turn}, result...)
				}
				return result, historyCursor(result)
			}
			size += len(raw)
			turn.Items = append([]ThreadItem{item}, turn.Items...)
		}
		result = append([]Turn{turn}, result...)
		if ti > 0 && len(result) >= historyPageTurns {
			return result, historyCursor(result)
		}
	}
	return result, ""
}

func historyCursor(turns []Turn) string {
	if len(turns[0].Items) == 0 {
		return "turn:" + turns[0].ID
	}
	raw, _ := json.Marshal([]string{turns[0].ID, turns[0].Items[0].ID})
	return "item:" + base64.RawURLEncoding.EncodeToString(raw)
}

func pageThreadSnapshot(thread Thread) Thread {
	end := len(thread.Turns) - 1
	if end >= 0 {
		thread.Turns, thread.HistoryCursor = historyPage(thread.ID, thread.Turns, end, len(thread.Turns[end].Items))
	}
	thread.HistoryPaged = true
	return thread
}

func (th *threadState) resumeSnapshotLocked(paged bool) Thread {
	if !paged {
		return th.snapshotLocked()
	}
	thread := th.snapshotTurnsLocked(nil)
	thread.Turns = th.Turns
	return pageThreadSnapshot(thread)
}

// RemoteThreadItem projects an item for a bandwidth-limited controller. Oversized
// content retains an address readable through thread/content/read.
func RemoteThreadItem(threadID, turnID string, item ThreadItem) ThreadItem {
	if item.RemoteContentRef != "" {
		return item
	}
	return historyItem(threadID, turnID, item)
}

func historyItem(threadID, turnID string, item ThreadItem) ThreadItem {
	source := item
	item = cloneThreadItem(item)
	for index, img := range item.Images {
		if len(img.Data) <= 16*1024 {
			continue
		}
		digest := sha256.Sum256([]byte(img.MediaType + "\x00" + img.Data))
		ref, _ := json.Marshal([]any{threadID, turnID, item.ID, index, fmt.Sprintf("%x", digest)})
		item.Images[index].Data = ""
		item.Images[index].RemoteRef = "thread:" + base64.RawURLEncoding.EncodeToString(ref)
	}
	if item.ResultDetail != nil {
		for index, part := range item.ResultDetail.Content {
			if part.Type != "image" || len(part.Data) <= 16*1024 {
				continue
			}
			digest := sha256.Sum256([]byte(part.MIMEType + "\x00" + part.Data))
			ref, _ := json.Marshal([]any{threadID, turnID, item.ID, index, fmt.Sprintf("%x", digest), "result"})
			item.ResultDetail.Content[index].Data = ""
			item.ResultDetail.Content[index].RemoteRef = "thread:" + base64.RawURLEncoding.EncodeToString(ref)
		}
	}
	raw, _ := json.Marshal(item)
	if len(raw) <= 64*1024 {
		return item
	}
	full, _ := json.Marshal(source)
	digest := sha256.Sum256(full)
	ref, _ := json.Marshal([]string{threadID, turnID, item.ID, fmt.Sprintf("%x", digest)})
	item.RemoteContentRef = "content:" + base64.RawURLEncoding.EncodeToString(ref)
	item.Text = historyTextPreview(item.Text)
	item.InputText = ""
	item.Reason = historyTextPreview(item.Reason)
	item.Error = historyTextPreview(item.Error)
	item.Summary = historyTextPreview(item.Summary)
	item.Result = historyTextPreview(item.Result)
	item.Arguments = ""
	item.ContentParts = nil
	if item.ResultDetail != nil {
		item.ResultDetail.StructuredContent = nil
		item.ResultDetail.Meta = nil
		for index := range item.ResultDetail.Content {
			part := &item.ResultDetail.Content[index]
			part.Text = historyTextPreview(part.Text)
			part.Resource = nil
			if part.Type != "image" {
				part.Data = ""
			}
		}
		if len(item.ResultDetail.Content) > 4 {
			item.ResultDetail.Content = item.ResultDetail.Content[:4]
		}
	}
	item.Display = nil
	item.Files = nil
	// Thousands of attachments must not make a single item unbounded either.
	if len(item.Images) > 4 {
		item.Images = item.Images[:4]
	}
	return item
}

func historyTextPreview(text string) string {
	if len(text) <= 2048 {
		return text
	}
	return strings.ToValidUTF8(text[:2048], "") + "…"
}

func (s *Server) historyThread(id string) (*threadState, error) {
	if th := s.thread(id); th != nil {
		return th, nil
	}
	// Read-only fallback also works after a host restart without a prior resume.
	th, err := s.loadPersistedThreadState(id, time.Now().UTC())
	if errors.Is(err, session.ErrSessionNotFound) {
		thread, ok, agentErr := s.agentSessionThread(id)
		if agentErr != nil {
			return nil, agentErr
		}
		if ok {
			return &threadState{ID: thread.ID, Turns: thread.Turns}, nil
		}
	}
	return th, err
}

func (s *Server) handleThreadHistoryRead(req Request) error {
	var params struct {
		ThreadID string `json:"thread_id"`
		Cursor   string `json:"cursor"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	boundaryTurn, boundaryItem := "", ""
	if strings.HasPrefix(params.Cursor, "turn:") {
		boundaryTurn = strings.TrimPrefix(params.Cursor, "turn:")
	} else if strings.HasPrefix(params.Cursor, "item:") {
		raw, err := base64.RawURLEncoding.DecodeString(strings.TrimPrefix(params.Cursor, "item:"))
		var parts []string
		if err == nil && json.Unmarshal(raw, &parts) == nil && len(parts) == 2 {
			boundaryTurn, boundaryItem = parts[0], parts[1]
		}
	}
	if boundaryTurn == "" {
		return s.writeResponse(req.ID, nil, errors.New("invalid history cursor"))
	}
	th, err := s.historyThread(params.ThreadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	th.mu.Lock()
	end, itemEnd := -1, 0
	for i := len(th.Turns) - 1; i >= 0; i-- {
		if th.Turns[i].ID == boundaryTurn {
			end = i
			break
		}
	}
	if end >= 0 && boundaryItem != "" {
		itemEnd = -1
		for i, item := range th.Turns[end].Items {
			if item.ID == boundaryItem {
				itemEnd = i
				break
			}
		}
	}
	if end < 0 || itemEnd < 0 {
		th.mu.Unlock()
		return s.writeResponse(req.ID, nil, errors.New("history changed; reopen the conversation"))
	}
	if itemEnd == 0 {
		end--
		if end >= 0 {
			itemEnd = len(th.Turns[end].Items)
		}
	}
	turns, cursor := historyPage(th.ID, th.Turns, end, itemEnd)
	th.mu.Unlock()
	return s.writeResponse(req.ID, struct {
		ThreadID      string `json:"thread_id"`
		Cursor        string `json:"cursor"`
		Turns         []Turn `json:"turns"`
		HistoryCursor string `json:"history_cursor,omitempty"`
	}{params.ThreadID, params.Cursor, turns, cursor}, nil)
}
