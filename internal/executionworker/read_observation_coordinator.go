package executionworker

import (
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"sort"
	"time"
)

type readBatchRequest struct {
	OperationID    string               `json:"operation_id"`
	WorkerInstance string               `json:"worker_instance"`
	Actor          string               `json:"actor"`
	Receipts       []readReceiptRequest `json:"receipts,omitempty"`
}

func batchDigest(receipts []readReceiptRequest) (string, error) {
	canonical := append([]readReceiptRequest(nil), receipts...)
	sort.Slice(canonical, func(i, j int) bool { return canonical[i].Receipt < canonical[j].Receipt })
	encoded, err := json.Marshal(canonical)
	if err != nil {
		return "", err
	}
	sum := sha256.Sum256(encoded)
	return fmt.Sprintf("%x", sum[:]), nil
}
func (s *Server) prepareReadBatch(ctx context.Context, data json.RawMessage) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var req readBatchRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	if req.WorkerInstance != s.instanceID || req.Actor == "" || req.OperationID == "" || len(req.OperationID) > 128 || len(req.Receipts) == 0 || len(req.Receipts) > 128 {
		return nil, errors.New("invalid remote read batch")
	}
	digest, err := batchDigest(req.Receipts)
	if err != nil {
		return nil, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	if s.closed {
		return nil, errors.New("execution worker is closed")
	}
	if old := s.readBatches[req.OperationID]; old != nil {
		if old.digest != digest || old.actor != req.Actor || old.aborted || !old.expires.After(time.Now()) {
			return nil, errors.New("remote read batch identity mismatch")
		}
		return json.Marshal(map[string]bool{"prepared": true})
	}
	if len(s.readBatches) >= readReceiptLimit {
		return nil, errors.New("remote read batch capacity reached")
	}
	seen := make(map[string]struct{}, len(req.Receipts))
	receipts := make([]*readReceipt, 0, len(req.Receipts))
	for _, binding := range req.Receipts {
		if _, ok := seen[binding.Receipt]; ok {
			return nil, errors.New("duplicate remote read receipt")
		}
		seen[binding.Receipt] = struct{}{}
		receipt := s.readReceipts[binding.Receipt]
		if receipt == nil || receipt.running || receipt.committed || receipt.batchID != "" || !receipt.expires.After(time.Now()) || binding.Actor != req.Actor || receipt.actor != binding.Actor || receipt.mode != binding.PermissionMode || receipt.attribution != binding.GitAttributionEnabled || receipt.shellDisabled != binding.ShellEarlyHandoffDisabled {
			return nil, errors.New("remote read receipt unavailable")
		}
		receipts = append(receipts, receipt)
	}
	expires := time.Now().Add(readReceiptLifetime)
	for _, receipt := range receipts {
		receipt.batchID = req.OperationID
		receipt.expires = expires
	}
	s.readBatches[req.OperationID] = &readBatch{digest: digest, expires: expires, actor: req.Actor, members: append([]readReceiptRequest(nil), req.Receipts...), prepared: true}
	return json.Marshal(map[string]bool{"prepared": true})
}
func (s *Server) readBatchState(ctx context.Context, data json.RawMessage, action string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var req readBatchRequest
	if err := json.Unmarshal(data, &req); err != nil {
		return nil, err
	}
	if req.WorkerInstance != s.instanceID {
		return nil, errors.New("remote worker instance changed")
	}
	s.mu.Lock()
	if s.closed {
		s.mu.Unlock()
		return nil, errors.New("execution worker is closed")
	}
	batch := s.readBatches[req.OperationID]
	if batch == nil || batch.actor != req.Actor || (!batch.running && !batch.expires.After(time.Now())) {
		s.mu.Unlock()
		return nil, errors.New("remote read batch unavailable")
	}
	if action == "status" {
		state := "prepared"
		if batch.running {
			state = "committing"
		}
		if batch.committed {
			state = "committed"
		}
		if batch.aborted {
			state = "aborted"
		}
		s.mu.Unlock()
		return json.Marshal(map[string]string{"state": state})
	}
	if action == "release" {
		if batch.running || (!batch.committed && !batch.aborted) {
			s.mu.Unlock()
			return nil, errors.New("remote read batch is not committed")
		}
		for _, binding := range batch.members {
			delete(s.readReceipts, binding.Receipt)
		}
		delete(s.readBatches, req.OperationID)
		s.mu.Unlock()
		return json.Marshal(map[string]bool{"released": true})
	}
	if action == "abort" {
		if batch.running || batch.committed {
			s.mu.Unlock()
			return nil, errors.New("remote read batch outcome is not abortable")
		}
		for _, binding := range batch.members {
			delete(s.readReceipts, binding.Receipt)
		}
		batch.aborted = true
		batch.expires = time.Now().Add(readCommittedReceiptLifetime)
		s.mu.Unlock()
		return json.Marshal(map[string]bool{"aborted": true})
	}
	if batch.aborted {
		s.mu.Unlock()
		return nil, errors.New("remote read batch was aborted")
	}
	if action != "commit" {
		s.mu.Unlock()
		return nil, errors.New("unknown remote batch action")
	}
	members := append([]readReceiptRequest(nil), batch.members...)
	s.mu.Unlock()
	encoded, err := json.Marshal(readBatchRequest{OperationID: req.OperationID, WorkerInstance: req.WorkerInstance, Actor: req.Actor, Receipts: members})
	if err != nil {
		return nil, err
	}
	return s.commitObservationBatch(ctx, encoded)
}
