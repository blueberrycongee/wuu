package executionworker

import (
	"context"
	"crypto/rand"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/tools"
)

const readReceiptLimit = 1024
const readReceiptLifetime = 2 * time.Minute
const readCommittedReceiptLifetime = 10 * time.Minute

type observationCommit interface {
	Commit(context.Context) (bool, error)
}

type readReceipt struct {
	actor, mode                string
	attribution, shellDisabled bool
	ticket                     observationCommit
	expires                    time.Time
	committed, changed         bool
	running                    bool
	done                       chan struct{}
	cancel                     context.CancelFunc
	batchID                    string
}
type readBatch struct {
	digest    string
	expires   time.Time
	committed bool
	aborted   bool
	running   bool
	actor     string
	members   []readReceiptRequest
	prepared  bool
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
	for id, batch := range s.readBatches {
		if !batch.running && !batch.expires.After(now) {
			delete(s.readBatches, id)
		}
	}
	for token, receipt := range s.readReceipts {
		if !receipt.running && !receipt.expires.After(now) {
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
	for {
		s.mu.Lock()
		if s.closed {
			s.mu.Unlock()
			return nil, errors.New("execution worker is closed")
		}
		receipt := s.readReceipts[req.Receipt]
		if receipt == nil || (!receipt.running && !receipt.expires.After(time.Now())) || receipt.actor != req.Actor || receipt.mode != req.PermissionMode || receipt.attribution != req.GitAttributionEnabled || receipt.shellDisabled != req.ShellEarlyHandoffDisabled {
			s.mu.Unlock()
			return nil, errors.New("remote read receipt unavailable")
		}
		if !statusOnly && receipt.batchID != "" {
			s.mu.Unlock()
			return nil, errors.New("remote read receipt belongs to a batch")
		}
		if statusOnly || receipt.committed {
			response := struct {
				Committed  bool `json:"committed"`
				Changed    bool `json:"changed"`
				InProgress bool `json:"in_progress,omitempty"`
			}{receipt.committed, receipt.changed, receipt.running}
			s.mu.Unlock()
			return json.Marshal(response)
		}
		if receipt.running {
			done := receipt.done
			s.mu.Unlock()
			select {
			case <-done:
				continue
			case <-ctx.Done():
				return nil, ctx.Err()
			}
		}
		commitCtx, cancel := context.WithCancel(ctx)
		receipt.running = true
		receipt.done = make(chan struct{})
		receipt.cancel = cancel
		s.readCommits.Add(1)
		s.mu.Unlock()
		changed, err := receipt.ticket.Commit(commitCtx)
		cancel()
		s.mu.Lock()
		receipt.running = false
		receipt.cancel = nil
		if err == nil {
			receipt.committed = true
			receipt.changed = changed
			receipt.expires = time.Now().Add(readCommittedReceiptLifetime)
		}
		close(receipt.done)
		s.mu.Unlock()
		s.readCommits.Done()
		if err != nil {
			return nil, err
		}
	}
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
	if receipt.running || receipt.batchID != "" {
		return nil, errors.New("remote read receipt belongs to an active batch")
	}
	if !receipt.committed {
		delete(s.readReceipts, req.Receipt)
	}
	return json.Marshal(map[string]bool{"released": !receipt.committed})
}

// releaseObservation removes a committed receipt only after the host observed
// its acknowledgement; pending receipts are never treated as committed.
func (s *Server) releaseObservation(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
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
	if receipt.actor != req.Actor || receipt.mode != req.PermissionMode || receipt.attribution != req.GitAttributionEnabled || receipt.shellDisabled != req.ShellEarlyHandoffDisabled || receipt.running || !receipt.committed {
		return nil, errors.New("committed remote read receipt unavailable")
	}
	delete(s.readReceipts, req.Receipt)
	if receipt.batchID != "" {
		remaining := false
		for _, other := range s.readReceipts {
			if other.batchID == receipt.batchID {
				remaining = true
				break
			}
		}
		if !remaining {
			delete(s.readBatches, receipt.batchID)
		}
	}
	return json.Marshal(map[string]bool{"released": true})
}

// commitObservationBatch applies only one worker actor's pending receipts in a
// single store transaction. A mixed actor, duplicate or partially committed
// batch is rejected before any state changes.
func (s *Server) commitObservationBatch(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var batch struct {
		OperationID    string               `json:"operation_id"`
		WorkerInstance string               `json:"worker_instance"`
		Receipts       []readReceiptRequest `json:"receipts"`
	}
	if err := json.Unmarshal(data, &batch); err != nil {
		return nil, err
	}
	if batch.WorkerInstance != s.instanceID {
		return nil, errors.New("remote worker instance changed")
	}
	if batch.OperationID == "" || len(batch.OperationID) > 128 || len(batch.Receipts) == 0 || len(batch.Receipts) > 128 {
		return nil, errors.New("invalid remote read receipt batch size")
	}
	canonical := append([]readReceiptRequest(nil), batch.Receipts...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Receipt < canonical[j].Receipt })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return nil, err
	}
	sum := sha256.Sum256(encoded)
	digest := fmt.Sprintf("%x", sum[:])
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("execution worker is closed")
	}
	if previous := s.readBatches[batch.OperationID]; previous != nil && previous.digest != digest {
		s.mu.Unlock()
		return nil, errors.New("remote read batch identity reused with different receipts")
	}
	seen := make(map[string]struct{}, len(batch.Receipts))
	receipts := make([]*readReceipt, 0, len(batch.Receipts))
	tickets := make([]*tools.ReadObservationTicket, 0, len(batch.Receipts))
	actor := batch.Receipts[0].Actor
	committed := 0
	for _, req := range batch.Receipts {
		if _, ok := seen[req.Receipt]; ok {
			s.mu.Unlock()
			return nil, errors.New("duplicate remote read receipt")
		}
		seen[req.Receipt] = struct{}{}
		receipt := s.readReceipts[req.Receipt]
		if receipt == nil || receipt.running || (receipt.batchID != "" && receipt.batchID != batch.OperationID) || !receipt.expires.After(time.Now()) || actor == "" || req.Actor != actor || receipt.actor != req.Actor || receipt.mode != req.PermissionMode || receipt.attribution != req.GitAttributionEnabled || receipt.shellDisabled != req.ShellEarlyHandoffDisabled {
			s.mu.Unlock()
			return nil, errors.New("remote read receipt unavailable")
		}
		ticket, ok := receipt.ticket.(*tools.ReadObservationTicket)
		if !ok {
			s.mu.Unlock()
			return nil, errors.New("invalid remote read receipt")
		}
		if receipt.committed {
			committed++
		}
		receipts = append(receipts, receipt)
		tickets = append(tickets, ticket)
	}
	if committed == len(receipts) {
		previous := s.readBatches[batch.OperationID]
		if previous == nil || !previous.committed || previous.digest != digest {
			s.mu.Unlock()
			return nil, errors.New("committed receipts do not belong to this batch")
		}
		for _, receipt := range receipts {
			if receipt.batchID != batch.OperationID {
				s.mu.Unlock()
				return nil, errors.New("committed receipt belongs to another batch")
			}
		}
		s.mu.Unlock()
		return json.Marshal(map[string]bool{"committed": true})
	}
	if committed != 0 {
		s.mu.Unlock()
		return nil, errors.New("remote read batch has mixed terminal states")
	}
	previous := s.readBatches[batch.OperationID]
	if previous != nil && (!previous.prepared || previous.running || previous.actor != actor) {
		s.mu.Unlock()
		return nil, errors.New("remote read batch is already in progress")
	}
	if previous == nil {
		if len(s.readBatches) >= readReceiptLimit {
			s.mu.Unlock()
			return nil, errors.New("remote read batch capacity reached")
		}
		previous = &readBatch{digest: digest, expires: time.Now().Add(readReceiptLifetime), actor: actor, members: append([]readReceiptRequest(nil), batch.Receipts...)}
		s.readBatches[batch.OperationID] = previous
	}
	previous.running = true
	commitCtx, cancel := context.WithCancel(ctx)
	done := make(chan struct{})
	for _, receipt := range receipts {
		receipt.running = true
		receipt.cancel = cancel
		receipt.done = done
	}
	s.readCommits.Add(1)
	s.mu.Unlock()
	changed, err := tools.CommitReadObservationBatch(commitCtx, tickets)
	cancel()
	s.mu.Lock()
	for i, receipt := range receipts {
		receipt.running = false
		receipt.cancel = nil
		if err == nil {
			receipt.committed = true
			receipt.batchID = batch.OperationID
			receipt.changed = changed[i]
			receipt.expires = time.Now().Add(readCommittedReceiptLifetime)
		}
	}
	if err == nil {
		s.readBatches[batch.OperationID].running = false
		s.readBatches[batch.OperationID].committed = true
		s.readBatches[batch.OperationID].expires = time.Now().Add(readCommittedReceiptLifetime)
	} else if s.readBatches[batch.OperationID].prepared {
		s.readBatches[batch.OperationID].running = false
	} else {
		delete(s.readBatches, batch.OperationID)
	}
	close(done)
	s.mu.Unlock()
	s.readCommits.Done()
	if err != nil {
		return nil, err
	}
	return json.Marshal(map[string]bool{"committed": true})
}
