package externalengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/url"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type openCodeError struct {
	Name string `json:"name"`
	Data struct {
		Message string `json:"message"`
	} `json:"data"`
}

func (e *openCodeError) Error() string {
	return fmt.Sprintf("OpenCode %s: %s", e.Name, e.Data.Message)
}

type openCodeInfo struct {
	ID        string         `json:"id"`
	SessionID string         `json:"sessionID"`
	ParentID  string         `json:"parentID"`
	Role      string         `json:"role"`
	Finish    string         `json:"finish"`
	Error     *openCodeError `json:"error"`
	Time      struct {
		Completed int64 `json:"completed"`
	} `json:"time"`
	Tokens struct {
		Input, Output int
		Cache         struct{ Read, Write int }
	} `json:"tokens"`
}

type openCodePart struct {
	ID        string `json:"id"`
	SessionID string `json:"sessionID"`
	MessageID string `json:"messageID"`
	Type      string `json:"type"`
	Text      string `json:"text"`
	CallID    string `json:"callID"`
	Tool      string `json:"tool"`
	State     struct {
		Status string          `json:"status"`
		Input  json.RawMessage `json:"input"`
		Output string          `json:"output"`
		Error  string          `json:"error"`
	} `json:"state"`
}

type openCodeMessage struct {
	Info  openCodeInfo   `json:"info"`
	Parts []openCodePart `json:"parts"`
}

type openCodeEvent struct {
	Type       string          `json:"type"`
	Properties json.RawMessage `json:"properties"`
}

type openCodeTurn struct {
	ref, userID string
	turn        *turn
	messages    map[string]openCodeInfo
	parts       map[string]openCodePart
}

func newOpenCodeTurn(ref, userID string, t *turn) *openCodeTurn {
	return &openCodeTurn{ref: ref, userID: userID, turn: t, messages: make(map[string]openCodeInfo), parts: make(map[string]openCodePart)}
}

func (s *openCodeTurn) owns(info openCodeInfo) bool {
	return info.ID != "" && info.SessionID == s.ref && info.Role == "assistant" && info.ParentID == s.userID
}

func (s *openCodeTurn) message(message openCodeMessage) error {
	if !s.owns(message.Info) {
		return nil
	}
	if message.Info.Error != nil {
		return message.Info.Error
	}
	s.messages[message.Info.ID] = message.Info
	usage := providers.TokenUsage{}
	for _, info := range s.messages {
		usage.InputTokens += info.Tokens.Input
		usage.OutputTokens += info.Tokens.Output
		usage.CacheReadTokens += info.Tokens.Cache.Read
		usage.CacheCreationTokens += info.Tokens.Cache.Write
	}
	s.turn.usage = usage
	for _, part := range message.Parts {
		if err := s.part(part); err != nil {
			return err
		}
	}
	return nil
}

func (s *openCodeTurn) part(part openCodePart) error {
	if part.ID == "" || part.SessionID != s.ref || !s.owns(s.messages[part.MessageID]) {
		return nil
	}
	previous, exists := s.parts[part.ID]
	switch part.Type {
	case "text", "reasoning":
		if exists && strings.HasPrefix(previous.Text, part.Text) {
			// A snapshot may lag already delivered deltas. Never replay it.
			return nil
		}
		if !strings.HasPrefix(part.Text, previous.Text) {
			return errors.New("OpenCode replaced already streamed text")
		}
		if part.Text != previous.Text {
			s.turn.content(part.Text[len(previous.Text):], part.Type == "reasoning")
		}
	case "tool":
		status, output := part.State.Status, part.State.Output
		if status == "error" {
			status, output = "failed", part.State.Error
		}
		s.turn.tool(part.CallID, part.Tool, string(part.State.Input), status, output)
	}
	s.parts[part.ID] = part
	return nil
}

func (s *Session) openCodeEvent(ctx context.Context, client *openCodeClient, state *openCodeTurn, raw json.RawMessage) error {
	var event openCodeEvent
	if err := json.Unmarshal(raw, &event); err != nil {
		return fmt.Errorf("invalid OpenCode event: %w", err)
	}
	switch event.Type {
	case "message.updated":
		var message openCodeMessage
		if err := json.Unmarshal(event.Properties, &message); err != nil {
			return err
		}
		return state.message(message)
	case "message.part.updated":
		var update struct {
			Part openCodePart `json:"part"`
		}
		if err := json.Unmarshal(event.Properties, &update); err != nil {
			return err
		}
		return state.part(update.Part)
	case "message.part.delta":
		var delta struct {
			SessionID string `json:"sessionID"`
			MessageID string `json:"messageID"`
			PartID    string `json:"partID"`
			Field     string `json:"field"`
			Delta     string `json:"delta"`
		}
		if err := json.Unmarshal(event.Properties, &delta); err != nil {
			return err
		}
		part, exists := state.parts[delta.PartID]
		if exists && delta.SessionID == state.ref && delta.MessageID == part.MessageID && delta.Field == "text" {
			part.Text += delta.Delta
			return state.part(part)
		}
	case "session.error":
		var failure struct {
			SessionID string         `json:"sessionID"`
			Error     *openCodeError `json:"error"`
		}
		if err := json.Unmarshal(event.Properties, &failure); err != nil {
			return err
		}
		if failure.SessionID == state.ref {
			if failure.Error != nil {
				return failure.Error
			}
			return errors.New("OpenCode reported a session error")
		}
	case "permission.asked":
		return s.openCodePermission(ctx, client, state.ref, event.Properties)
	case "question.asked":
		var question struct {
			ID        string `json:"id"`
			SessionID string `json:"sessionID"`
		}
		if err := json.Unmarshal(event.Properties, &question); err != nil {
			return err
		}
		if question.ID == "" {
			return errors.New("OpenCode question has no request ID")
		}
		replyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
		defer cancel()
		if err := client.call(replyCtx, "POST", "/question/"+url.PathEscape(question.ID)+"/reject", nil, nil); err != nil {
			return err
		}
		if question.SessionID == state.ref {
			return errors.New("this OpenCode turn requested structured user input, which this integration does not support")
		}
	case "server.instance.disposed":
		return errors.New("OpenCode disposed the active workspace")
	}
	return nil
}

func (s *Session) openCodePermission(ctx context.Context, client *openCodeClient, ref string, raw json.RawMessage) error {
	var req struct {
		ID         string          `json:"id"`
		SessionID  string          `json:"sessionID"`
		Permission string          `json:"permission"`
		Patterns   []string        `json:"patterns"`
		Metadata   json.RawMessage `json:"metadata"`
	}
	if err := json.Unmarshal(raw, &req); err != nil {
		return err
	}
	if req.ID == "" {
		return errors.New("OpenCode permission has no request ID")
	}
	decision := agentengine.ApprovalDecline
	if req.SessionID == ref {
		if s.binding.PermissionMode == "unconfined" {
			decision = agentengine.ApprovalAccept
		} else if s.binding.RequestApproval != nil {
			kind := agentengine.ApprovalPermissions
			if req.Permission == "bash" {
				kind = agentengine.ApprovalCommandExecution
			}
			if req.Permission == "edit" {
				kind = agentengine.ApprovalFileChange
			}
			var err error
			decision, err = s.binding.RequestApproval(ctx, agentengine.ApprovalRequest{
				Kind: kind, EngineID: "opencode", ThreadID: s.binding.ThreadID, ItemID: req.ID,
				Reason: req.Permission + ": " + strings.Join(req.Patterns, ", "), CWD: s.binding.RootDir, Permissions: req.Metadata,
			})
			if err != nil {
				return err
			}
		}
	}
	reply := "reject"
	// Native "always" can outlive this host grant. Only send one-shot replies.
	if decision == agentengine.ApprovalAccept || decision == agentengine.ApprovalAcceptForSession {
		reply = "once"
	}
	if decision == agentengine.ApprovalCancel {
		return context.Canceled
	}
	replyCtx, cancel := context.WithTimeout(ctx, 30*time.Second)
	defer cancel()
	return client.call(replyCtx, "POST", "/permission/"+url.PathEscape(req.ID)+"/reply", map[string]string{"reply": reply}, nil)
}
