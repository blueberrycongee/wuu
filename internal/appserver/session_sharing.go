package appserver

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"time"

	"github.com/blueberrycongee/wuu/internal/principal"
	"github.com/blueberrycongee/wuu/internal/sessionshare"
)

// The recipient uses projected endpoints only. The common resource authorization
// gate runs before this handler; provider checks bind share ID to source session
// and re-evaluate expiry/revocation after loading current source data.
func (s *Server) handleSessionShare(ctx context.Context, req Request) error {
	var in struct {
		RecoverOnly  bool                   `json:"recover_only,omitempty"`
		OperationID  string                 `json:"operation_id,omitempty"`
		ThreadID     string                 `json:"thread_id"`
		ShareID      string                 `json:"share_id,omitempty"`
		Selection    sessionshare.Selection `json:"selection,omitempty"`
		Mode         string                 `json:"mode,omitempty"`
		Recipients   []principal.Identity   `json:"recipients,omitempty"`
		ExpiresAt    time.Time              `json:"expires_at,omitempty"`
		Reviewed     bool                   `json:"reviewed,omitempty"`
		ReviewDigest string                 `json:"review_digest,omitempty"`
		LiveUpdates  bool                   `json:"live_updates,omitempty"`
		AttachmentID string                 `json:"attachment_id,omitempty"`
	}
	if err := decodeParams(req.Params, &in); err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	if in.ThreadID == "" {
		return s.writeResponse(req.ID, nil, errors.New("thread_id is required"))
	}
	binding, _, err := s.retainUserThreadExecutionState(in.ThreadID, true)
	if err != nil {
		return s.writeResponse(req.ID, nil, err)
	}
	defer binding.Close()
	sharing := binding.SessionSharing()
	if sharing == nil {
		return s.writeResponse(req.ID, nil, errors.New("session sharing provider is unavailable"))
	}
	actor := s.Principal(ctx)
	if !actor.Valid() {
		return s.writeResponse(req.ID, nil, errors.New("authenticated principal is required"))
	}
	read := sessionshare.ReadRequest{Actor: actor.Identity(), SessionID: in.ThreadID, ShareID: in.ShareID}
	source := func(selection sessionshare.Selection) (sessionshare.Source, error) {
		th, err := s.historyThread(binding, in.ThreadID)
		if err != nil {
			return sessionshare.Source{}, err
		}
		if th == nil {
			return sessionshare.Source{}, errors.New("conversation unavailable")
		}
		th.mu.Lock()
		thread := th.snapshotLocked()
		th.mu.Unlock()
		return sessionShareSource(thread, selection)
	}
	var out any
	switch req.Method {
	case "session/share/preview", "session/share/create":
		data := sessionshare.Source{SessionID: in.ThreadID}
		if req.Method != "session/share/create" || !in.RecoverOnly {
			data, err = source(in.Selection)
			if err != nil {
				break
			}
		}
		if req.Method == "session/share/preview" {
			out, err = sharing.Preview(ctx, sessionshare.PreviewRequest{Source: data, Selection: in.Selection})
		} else {
			out, err = sharing.Create(ctx, sessionshare.CreateRequest{OperationID: in.OperationID, Actor: actor.Identity(), Source: data, Selection: in.Selection, Mode: in.Mode, Recipients: in.Recipients, ExpiresAt: in.ExpiresAt, Reviewed: in.Reviewed, ReviewDigest: in.ReviewDigest, LiveUpdates: in.LiveUpdates})
		}
	case "session/share/read", "session/share/attachment/read":
		var plan sessionshare.ReadPlan
		plan, err = sharing.Resolve(ctx, read)
		if err != nil {
			break
		}
		if plan.Mode == "live" {
			var data sessionshare.Source
			data, err = source(plan.Selection)
			if err != nil {
				break
			}
			read.Source = &data
		}
		if req.Method == "session/share/read" {
			out, err = sharing.Read(ctx, read)
		} else {
			out, err = sharing.Attachment(ctx, sessionshare.AttachmentRequest{ReadRequest: read, AttachmentID: in.AttachmentID})
		}
	case "session/share/revoke":
		err = sharing.Revoke(ctx, sessionshare.RevokeRequest{Actor: actor.Identity(), SessionID: in.ThreadID, ShareID: in.ShareID})
		out = struct{}{}
	default:
		err = errors.New("unknown sharing method")
	}
	return s.writeResponse(req.ID, out, err)
}

func sessionShareSource(thread Thread, selection sessionshare.Selection) (sessionshare.Source, error) {
	source := sessionshare.Source{SessionID: thread.ID, Items: []sessionshare.Item{}}
	messages, results, attachments := map[string]bool{}, map[string]bool{}, map[string]bool{}
	for _, id := range selection.MessageIDs {
		messages[id] = true
	}
	for _, id := range selection.ToolResultIDs {
		results[id] = true
	}
	for _, id := range selection.AttachmentIDs {
		attachments[id] = true
	}
	for _, turn := range thread.Turns {
		at := time.Time{}
		if turn.StartedAt != nil {
			at = *turn.StartedAt
		}
		for _, item := range turn.Items {
			if messages[item.ID] && (item.Type == ThreadItemUserMessage || item.Type == ThreadItemAgentMessage) {
				role := "assistant"
				if item.Type == ThreadItemUserMessage {
					role = "user"
				}
				source.Items = append(source.Items, sessionshare.Item{ID: item.ID, TurnID: turn.ID, Kind: "message", Role: role, Text: item.Text, At: at})
			}
			if results[item.ID] && item.Type == ThreadItemToolCall {
				source.Items = append(source.Items, sessionshare.Item{ID: item.ID, TurnID: turn.ID, Kind: "tool_result", Text: item.Result, At: at})
			}
			for index, image := range item.Images {
				id := fmt.Sprintf("%s/image/%d", item.ID, index)
				if attachments[id] {
					source.Items = append(source.Items, sessionshare.Item{ID: id, TurnID: turn.ID, Kind: "attachment", At: at, MediaType: image.MediaType, Data: image.Data})
				}
			}
			for index, file := range item.Files {
				id := fmt.Sprintf("%s/file/%d", item.ID, index)
				if attachments[id] {
					source.Items = append(source.Items, sessionshare.Item{ID: id, TurnID: turn.ID, Kind: "attachment", At: at, MediaType: file.MediaType, Data: file.Data})
				}
			}
			// Structured tool-result metadata (including artifact URIs) is never forwarded.
			if item.ResultDetail != nil {
				for index, part := range item.ResultDetail.Content {
					id := fmt.Sprintf("%s/result/%d", item.ID, index)
					if attachments[id] && part.Type == "image" && part.Data != "" {
						source.Items = append(source.Items, sessionshare.Item{ID: id, TurnID: turn.ID, Kind: "attachment", At: at, MediaType: part.MIMEType, Data: part.Data})
					}
				}
			}
		}
	}
	_, items, err := sessionshare.Project(source, selection)
	source.Items = items
	if err == nil {
		raw, _ := json.Marshal(source)
		if len(raw) > sessionshare.MaxProjectionBytes+sessionshare.MaxAttachmentBytes*2 {
			err = errors.New("selected share source is too large")
		}
	}
	return source, err
}
