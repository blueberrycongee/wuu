package appserver

import (
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/remote/conversations"
)

// This read-only allowlist excludes raw input, tool output, reasoning, file
// references, and attachment contents from account history copies.
func textSnapshot(thread Thread) conversations.Thread {
	out := conversations.Thread{ID: thread.ID, Title: thread.Title, UpdatedAt: thread.UpdatedAt.UTC().Format(time.RFC3339Nano), Status: string(thread.Status), Messages: []conversations.Message{}}
	for _, turn := range thread.Turns {
		for _, item := range turn.Items {
			role := ""
			switch item.Type {
			case ThreadItemUserMessage:
				role = "user"
			case ThreadItemAgentMessage:
				role = "assistant"
			}
			if role != "" && item.Text != "" {
				out.Messages = append(out.Messages, conversations.Message{ID: item.ID, TurnID: turn.ID, Role: role, Text: item.Text})
			}
		}
	}
	return out
}

func (s *Server) handleThreadTextSnapshot(req Request) error {
	var params struct {
		ThreadID string `json:"thread_id"`
	}
	if err := decodeParams(req.Params, &params); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	th, err := s.historyThread(params.ThreadID)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if th == nil {
		return s.writeResponse(req.ID, nil, errors.New("conversation unavailable"))
	}
	th.mu.Lock()
	thread := th.snapshotLocked()
	th.mu.Unlock()
	if thread.ReadOnly || thread.Ephemeral || isNamedAgentSessionSource(thread.Source) || thread.ParentID != "" {
		return s.writeResponse(req.ID, nil, errors.New("conversation is not eligible for text sync"))
	}
	out := textSnapshot(thread)
	return s.writeResponse(req.ID, out, out.Validate())
}
