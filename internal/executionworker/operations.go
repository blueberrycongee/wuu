package executionworker

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"path/filepath"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/securefs"
	"github.com/blueberrycongee/wuu/internal/storelock"
)

const (
	maxOperationReceiptBytes = 8 * 1024 * 1024
	maxOperationStoreBytes   = 256 * 1024 * 1024
	maxOperationReceipts     = 16384
)

type operationReceipt struct {
	Version        int    `json:"version"`
	Scope          string `json:"scope"`
	Actor          string `json:"actor"`
	PermissionMode string `json:"permission_mode"`
	executionenv.OperationStatus
}

type operationFlight struct {
	query executionenv.OperationQuery
	done  chan struct{}
	data  json.RawMessage
	err   error
}

type operationStore struct {
	mu        sync.Mutex
	directory string
	scope     string
	active    map[string]*operationFlight
}

func newOperationStore(directory, workspace string, init executionenv.Init) (*operationStore, error) {
	directory = filepath.Clean(directory)
	relative, err := filepath.Rel(workspace, directory)
	if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, errors.New("operation receipts must be outside the workspace")
	}
	if err := os.MkdirAll(directory, 0700); err != nil {
		return nil, err
	}
	canonical, err := filepath.EvalSymlinks(directory)
	if err != nil {
		return nil, err
	}
	relative, err = filepath.Rel(workspace, canonical)
	if err != nil || relative == "." || relative != ".." && !strings.HasPrefix(relative, ".."+string(os.PathSeparator)) {
		return nil, errors.New("operation receipts must be outside the workspace")
	}
	info, err := os.Lstat(directory)
	if err != nil {
		return nil, err
	}
	if !info.IsDir() || info.Mode().Perm()&0077 != 0 {
		return nil, errors.New("operation receipt directory is not private")
	}
	// MkdirAll (including runtime ancestors created by the process manager)
	// does not make directory entries durable. Anchor the complete path before
	// any receipt can authorize an effect, not only the final receipt rename.
	for current := canonical; ; current = filepath.Dir(current) {
		if err := syncOperationDirectory(current); err != nil {
			return nil, fmt.Errorf("persist operation receipt directory: %w", err)
		}
		if filepath.Dir(current) == current {
			break
		}
	}
	binding, _ := json.Marshal([]string{init.Identity, init.Session, workspace})
	scope := sha256.Sum256(binding)
	return &operationStore{directory: canonical, scope: hex.EncodeToString(scope[:]), active: make(map[string]*operationFlight)}, nil
}

func operationFailure(kind, id, message string) error {
	return &executionenv.OperationError{Kind: kind, ID: id, Message: message}
}
func (s *operationStore) file(id string) string {
	digest := sha256.Sum256([]byte(id))
	return filepath.Join(s.directory, hex.EncodeToString(digest[:])+".json")
}

func (s *operationStore) read(query executionenv.OperationQuery) (*operationReceipt, error) {
	file := s.file(query.ID)
	info, err := os.Lstat(file)
	if errors.Is(err, os.ErrNotExist) {
		return nil, nil
	}
	if err != nil {
		return nil, err
	}
	if !info.Mode().IsRegular() || info.Mode().Perm()&0077 != 0 || info.Size() > maxOperationReceiptBytes {
		return nil, errors.New("invalid operation receipt file")
	}
	f, err := os.Open(file)
	if err != nil {
		return nil, err
	}
	defer f.Close()
	opened, err := f.Stat()
	if err != nil || !os.SameFile(info, opened) {
		return nil, errors.New("operation receipt changed during read")
	}
	raw, err := io.ReadAll(io.LimitReader(f, maxOperationReceiptBytes+1))
	if err != nil {
		return nil, err
	}
	if len(raw) > maxOperationReceiptBytes {
		return nil, errors.New("operation receipt exceeds storage limit")
	}
	var receipt operationReceipt
	decoder := json.NewDecoder(bytes.NewReader(raw))
	decoder.DisallowUnknownFields()
	if err := decoder.Decode(&receipt); err != nil {
		return nil, err
	}
	if err := decoder.Decode(new(any)); err != io.EOF {
		return nil, errors.New("operation receipt must contain one JSON value")
	}
	if receipt.Version != 1 || receipt.Scope != s.scope || receipt.ID != query.ID || receipt.Digest != query.Digest || receipt.Actor != query.Actor || receipt.PermissionMode != query.PermissionMode {
		return nil, operationFailure(executionenv.OperationConflict, query.ID, "durable operation identity or authority does not match")
	}
	if receipt.State == executionenv.OperationRunning && (len(receipt.Data) != 0 || receipt.Error != "") || receipt.State == executionenv.OperationCompleted && (len(receipt.Data) == 0 || receipt.Error != "") || receipt.State == executionenv.OperationFailed && receipt.Error == "" {
		return nil, errors.New("invalid operation receipt result")
	}
	switch receipt.State {
	case executionenv.OperationRunning, executionenv.OperationCompleted, executionenv.OperationFailed:
	default:
		return nil, errors.New("invalid operation receipt state")
	}
	return &receipt, nil
}

func (s *operationStore) write(receipt operationReceipt) error {
	raw, err := json.Marshal(receipt)
	if err != nil {
		return err
	}
	if len(raw) > maxOperationReceiptBytes {
		return errors.New("operation result exceeds receipt limit")
	}
	if err := securefs.WriteFileAtomic(s.file(receipt.ID), raw); err != nil {
		return err
	}
	// A renamed receipt is not durable until the directory entry is synchronized.
	return syncOperationDirectory(s.directory)
}

func syncOperationDirectory(path string) error {
	directory, err := os.Open(path)
	if err != nil {
		return err
	}
	defer directory.Close()
	return directory.Sync()
}

func (s *operationStore) capacity() error {
	entries, err := os.ReadDir(s.directory)
	if err != nil {
		return err
	}
	count := 0
	var size int64
	for _, entry := range entries {
		if !strings.HasSuffix(entry.Name(), ".json") {
			continue
		}
		info, err := entry.Info()
		if err != nil {
			return err
		}
		if !info.Mode().IsRegular() || info.Size() > maxOperationReceiptBytes {
			return errors.New("invalid operation receipt entry")
		}
		count++
		raw, err := os.ReadFile(filepath.Join(s.directory, entry.Name()))
		if err != nil {
			return err
		}
		var receipt struct {
			State string `json:"state"`
		}
		if err := json.Unmarshal(raw, &receipt); err != nil {
			return err
		}
		// Reservations are on disk, so separate worker processes cannot each
		// consume capacity already promised to another admitted operation.
		if receipt.State == executionenv.OperationRunning {
			size += maxOperationReceiptBytes
		} else {
			size += info.Size()
		}
	}
	// Never evict an admission into a state that could authorize a second run.
	if count >= maxOperationReceipts || size+maxOperationReceiptBytes > maxOperationStoreBytes {
		return errors.New("operation receipt storage is full")
	}
	return nil
}

func (s *operationStore) status(query executionenv.OperationQuery) (executionenv.OperationStatus, error) {
	if err := executionenv.ValidateOperationQuery(query); err != nil {
		return executionenv.OperationStatus{}, err
	}
	s.mu.Lock()
	defer s.mu.Unlock()
	receipt, err := s.read(query)
	if err != nil {
		return executionenv.OperationStatus{}, err
	}
	if receipt == nil {
		return executionenv.OperationStatus{ID: query.ID, Digest: query.Digest, State: executionenv.OperationNotFound}, nil
	}
	status := receipt.OperationStatus
	if status.State == executionenv.OperationRunning && s.active[query.ID] == nil {
		status.State = executionenv.OperationUnknown
	}
	return status, nil
}

func (s *operationStore) execute(ctx context.Context, query executionenv.OperationQuery, run func() (json.RawMessage, error)) (json.RawMessage, error) {
	if err := executionenv.ValidateOperationQuery(query); err != nil {
		return nil, operationFailure(executionenv.OperationNotStarted, query.ID, err.Error())
	}
	s.mu.Lock()
	if flight := s.active[query.ID]; flight != nil {
		if flight.query != query {
			s.mu.Unlock()
			return nil, operationFailure(executionenv.OperationConflict, query.ID, "durable operation identity or authority does not match")
		}
		s.mu.Unlock()
		select {
		case <-flight.done:
			return flight.data, flight.err
		case <-ctx.Done():
			return nil, operationFailure(executionenv.OperationUnknown, query.ID, "operation is still settling; its outcome is unknown")
		}
	}
	lock, err := storelock.Acquire(s.directory)
	if err != nil {
		s.mu.Unlock()
		return nil, operationFailure(executionenv.OperationUnknown, query.ID, "operation receipt store is unavailable; an earlier outcome cannot be verified")
	}
	receipt, err := s.read(query)
	if err != nil {
		lock.Release()
		s.mu.Unlock()
		var conflict *executionenv.OperationError
		if errors.As(err, &conflict) {
			return nil, err
		}
		return nil, operationFailure(executionenv.OperationUnknown, query.ID, "operation receipt cannot be verified; it will not be repeated")
	}
	if receipt != nil {
		lock.Release()
		s.mu.Unlock()
		if receipt.State == executionenv.OperationCompleted {
			return receipt.Data, nil
		}
		if receipt.State == executionenv.OperationFailed {
			return receipt.Data, errors.New(receipt.Error)
		}
		return nil, operationFailure(executionenv.OperationUnknown, query.ID, "operation was admitted without a confirmed result; it will not be repeated")
	}
	if err = s.capacity(); err == nil {
		err = ctx.Err()
	}
	receipt = &operationReceipt{Version: 1, Scope: s.scope, Actor: query.Actor, PermissionMode: query.PermissionMode, OperationStatus: executionenv.OperationStatus{ID: query.ID, Digest: query.Digest, State: executionenv.OperationRunning}}
	if err == nil {
		err = s.write(*receipt)
	}
	if err != nil {
		lock.Release()
		s.mu.Unlock()
		return nil, operationFailure(executionenv.OperationNotStarted, query.ID, "operation was not started: "+err.Error())
	}
	flight := &operationFlight{query: query, done: make(chan struct{})}
	s.active[query.ID] = flight
	lock.Release()
	s.mu.Unlock()
	data, runErr := run()
	receipt.State = executionenv.OperationCompleted
	receipt.Data = data
	if runErr != nil {
		receipt.State = executionenv.OperationFailed
		receipt.Error = runErr.Error()
	}
	s.mu.Lock()
	lock, persistErr := storelock.Acquire(s.directory)
	if persistErr == nil {
		persistErr = s.write(*receipt)
		lock.Release()
	}
	if persistErr != nil {
		data = nil
		runErr = operationFailure(executionenv.OperationUnknown, query.ID, "operation finished but its result could not be durably recorded; do not repeat it")
	}
	flight.data, flight.err = data, runErr
	delete(s.active, query.ID)
	close(flight.done)
	s.mu.Unlock()
	return data, runErr
}

func (s *Server) handleIdentified(ctx context.Context, request executionenv.Request) (json.RawMessage, error) {
	if request.OperationID == "" && request.OperationDigest == "" {
		return s.Handle(ctx, request.Method, request.Data)
	}
	digest, err := executionenv.OperationDigest(request.Method, request.Data)
	if err != nil || digest != request.OperationDigest {
		return nil, operationFailure(executionenv.OperationConflict, request.OperationID, "operation payload does not match its digest")
	}
	var binding struct {
		Actor          string `json:"actor"`
		PermissionMode string `json:"permission_mode"`
	}
	if err := json.Unmarshal(request.Data, &binding); err != nil {
		return nil, err
	}
	query := executionenv.OperationQuery{ID: request.OperationID, Digest: digest, Actor: binding.Actor, PermissionMode: binding.PermissionMode}
	s.mu.Lock()
	store, storeError := s.operations, s.operationStoreError
	s.mu.Unlock()
	if store == nil {
		return nil, operationFailure(executionenv.OperationUnknown, query.ID, "this attempt was not executed, but an earlier outcome cannot be verified; operation receipts are unavailable: "+storeError)
	}
	return store.execute(ctx, query, func() (json.RawMessage, error) { return s.Handle(ctx, request.Method, request.Data) })
}

func (s *Server) operationStatus(data json.RawMessage) (json.RawMessage, error) {
	var query executionenv.OperationQuery
	if err := json.Unmarshal(data, &query); err != nil {
		return nil, err
	}
	s.mu.Lock()
	store := s.operations
	s.mu.Unlock()
	if store == nil {
		return nil, errors.New("execution worker is not initialized")
	}
	status, err := store.status(query)
	if err != nil {
		return nil, fmt.Errorf("inspect operation status: %w", err)
	}
	return json.Marshal(status)
}
