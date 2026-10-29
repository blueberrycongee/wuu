package executionworker

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const readReceiptLimit = 1024
const readReceiptLifetime = 2 * time.Minute
const readCommittedReceiptLifetime = 10 * time.Minute

type readReceipt struct {
	actor, mode                string
	attribution, shellDisabled bool
	ticket                     *tools.ReadObservationTicket
	expires                    time.Time
	committed, changed         bool
}
type readReceiptRequest struct {
	Receipt                   string `json:"receipt"`
	Actor                     string `json:"actor"`
	PermissionMode            string `json:"permission_mode"`
	GitAttributionEnabled     bool   `json:"git_attribution_enabled,omitempty"`
	ShellEarlyHandoffDisabled bool   `json:"shell_early_handoff_disabled,omitempty"`
}

func (s *Server) prepareObservation(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var req executionenv.ToolRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	if req.Call.Name != "read_file" {
		return nil, errors.New("prepared observation requires read_file")
	}
	a, err := s.actor(req.Actor, req.PermissionMode, req.GitAttributionEnabled, req.ShellEarlyHandoffDisabled)
	if err != nil {
		return nil, err
	}
	var ticket *tools.ReadObservationTicket
	ctx = toolctx.WithUnobservedRead(ctx)
	ctx = toolctx.WithPreparedReadCapture(ctx, func(v any) { ticket, _ = v.(*tools.ReadObservationTicket) })
	result, err := a.kit.ExecuteEnvironmentResult(ctx, req.Call)
	if err != nil {
		return nil, err
	}
	if result.IsError || ticket == nil {
		return nil, errors.New("prepared read did not return a text observation")
	}
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("execution worker is closed")
	}
	now := time.Now()
	for token, receipt := range s.readReceipts {
		if !receipt.expires.After(now) {
			delete(s.readReceipts, token)
		}
	}
	if len(s.readReceipts) >= readReceiptLimit {
		return nil, errors.New("remote read receipt capacity reached")
	}
	token := rand.Text()
	s.readReceipts[token] = &readReceipt{actor: req.Actor, mode: req.PermissionMode, attribution: req.GitAttributionEnabled, shellDisabled: req.ShellEarlyHandoffDisabled, ticket: ticket, expires: now.Add(readReceiptLifetime)}
	return json.Marshal(struct {
		Receipt string `json:"receipt"`
		Result  any    `json:"result"`
	}{token, result})
}

func (s *Server) commitObservation(ctx context.Context, data json.RawMessage, statusOnly bool) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var req readReceiptRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("execution worker is closed")
	}
	receipt := s.readReceipts[req.Receipt]
	if receipt == nil || !receipt.expires.After(time.Now()) || receipt.actor != req.Actor || receipt.mode != req.PermissionMode || receipt.attribution != req.GitAttributionEnabled || receipt.shellDisabled != req.ShellEarlyHandoffDisabled {
		return nil, errors.New("remote read receipt unavailable")
	}
	if !statusOnly && !receipt.committed {
		changed, err := receipt.ticket.Commit(ctx)
		if err != nil {
			return nil, err
		}
		receipt.committed = true
		receipt.changed = changed
		receipt.expires = time.Now().Add(readCommittedReceiptLifetime)
	}
	return json.Marshal(struct {
		Committed bool `json:"committed"`
		Changed   bool `json:"changed"`
	}{receipt.committed, receipt.changed})
}

func (s *Server) abortObservation(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var req readReceiptRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("execution worker is closed")
	}
	receipt := s.readReceipts[req.Receipt]
	if receipt == nil {
		return json.Marshal(map[string]bool{"released": true})
	}
	if receipt.actor != req.Actor || receipt.mode != req.PermissionMode || receipt.attribution != req.GitAttributionEnabled || receipt.shellDisabled != req.ShellEarlyHandoffDisabled {
		return nil, errors.New("remote read receipt unavailable")
	}
	if !receipt.committed {
		delete(s.readReceipts, req.Receipt)
	}
	return json.Marshal(map[string]bool{"released": !receipt.committed})
}
