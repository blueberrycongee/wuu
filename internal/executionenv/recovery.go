package executionenv

import (
	"context"
	"crypto/rand"
	"encoding/json"
	"errors"
	"time"

	"github.com/blueberrycongee/wuu/internal/toolctx"
)

// callOperation makes one execution attempt. Recovery only asks for a receipt;
// it never dispatches the tool again, even when the worker cannot find its ID.
func (e *Environment) callOperation(ctx context.Context, client *Client, method string, request any, actor, mode string, handler func(context.Context, json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	id := toolctx.InvocationID(ctx)
	if id == "" {
		id = "direct-" + rand.Text()
	}
	payload, err := json.Marshal(request)
	if err != nil {
		return nil, err
	}
	digest, err := OperationDigest(method, payload)
	if err != nil {
		return nil, err
	}
	query := OperationQuery{ID: id, Digest: digest, Actor: actor, PermissionMode: mode}
	if err := ValidateOperationQuery(query); err != nil {
		return nil, &OperationError{Kind: OperationNotStarted, ID: id, Message: err.Error()}
	}
	data, err := client.CallOperation(ctx, id, method, request, handler)
	var uncertain *OperationError
	if !errors.As(err, &uncertain) || uncertain.Kind != OperationUnknown {
		return data, err
	}

	// Keep the exact binding in the host's durable error result. It is not a
	// post-restart reconciliation workflow and does not grant execution authority.
	outcome := *uncertain
	workspace := client.initializedRoot
	if workspace == "" {
		workspace = e.Root()
	}
	outcome.Binding = &OperationBinding{Environment: e.identity, Session: e.session, Workspace: workspace, Method: method, ReceiptsSupported: !client.noOperationReceipts, Query: query}
	if ctx.Err() != nil || client.noOperationReceipts || client.initializedRoot == "" {
		return nil, &outcome
	}
	lookupCtx, cancel := context.WithTimeout(ctx, 5*time.Second)
	defer cancel()
	status, lookupErr := e.lookupOperation(lookupCtx, client, query)
	if lookupErr != nil {
		return nil, &outcome
	}
	switch status.State {
	case OperationCompleted:
		if len(status.Data) != 0 && json.Valid(status.Data) && status.Error == "" {
			return status.Data, nil
		}
	case OperationFailed:
		if status.Error != "" {
			return nil, errors.New(status.Error)
		}
	}
	return nil, &outcome
}

func (e *Environment) lookupOperation(ctx context.Context, original *Client, query OperationQuery) (OperationStatus, error) {
	if !e.mu.TryLock() {
		return OperationStatus{}, errors.New("execution environment is busy; receipt lookup remains unavailable")
	}
	defer e.mu.Unlock()
	if e.closed {
		return OperationStatus{}, errors.New("execution environment is closed")
	}
	client := e.client
	if client == nil || client.Failed() {
		// prepare() may create a paid sandbox or replace a workspace. A receipt
		// lookup must not call it. These pinned commands only enter an existing
		// container/host/image; custom and cloud adapter startup has no such contract.
		switch e.profile.Backend {
		case "docker", "ssh", "singularity":
		default:
			return OperationStatus{}, errors.New("this transport cannot reconnect without possible provisioning")
		}
		client = NewClient(original.argv, original.env)
		client.token = original.token
		defer client.Close()
		hello, err := client.Call(ctx, "initialize", Init{Version: ProtocolVersion, Identity: e.identity, Session: e.session, Root: original.initializedRoot, RequireExistingWorkspace: true})
		if err != nil {
			return OperationStatus{}, err
		}
		var capability struct {
			Version  int  `json:"version"`
			Receipts bool `json:"operation_receipts"`
		}
		if err := json.Unmarshal(hello, &capability); err != nil || capability.Version != ProtocolVersion || !capability.Receipts {
			return OperationStatus{}, errors.New("receipt lookup is unavailable on the pinned worker")
		}
	}
	if client.noOperationReceipts {
		return OperationStatus{}, errors.New("worker does not support operation receipts")
	}
	raw, err := client.Call(ctx, "operation/status", query)
	if err != nil {
		return OperationStatus{}, err
	}
	var status OperationStatus
	if err := json.Unmarshal(raw, &status); err != nil {
		return OperationStatus{}, err
	}
	if status.ID != query.ID || status.Digest != query.Digest {
		return OperationStatus{}, errors.New("operation receipt identity does not match the original request")
	}
	return status, nil
}
