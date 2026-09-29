package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
)

// Transport is the low-level JSON-RPC transport for MCP.
type Transport interface {
	// Send writes a JSON-RPC request or notification to the server.
	Send(ctx context.Context, req Request) error
	// Receive reads the next JSON-RPC response or notification from the server.
	Receive(ctx context.Context) (Response, error)
	// Close shuts down the transport.
	Close() error
}

// readLoop pumps JSON-RPC messages from a transport into the in-flight tracker.
type readLoop struct {
	transport Transport
	inFlight  *inFlight
	onNotify  func(method string, params json.RawMessage)
	onRequest func(method string, params json.RawMessage) (json.RawMessage, *RPCError)
	onExit    func(error)
	stop      chan struct{}
	stopped   chan struct{}
	stopOnce  sync.Once
}

func newReadLoop(t Transport, f *inFlight, onNotify func(method string, params json.RawMessage), onRequest func(method string, params json.RawMessage) (json.RawMessage, *RPCError), onExit func(error)) *readLoop {
	return &readLoop{
		transport: t,
		inFlight:  f,
		onNotify:  onNotify,
		onRequest: onRequest,
		onExit:    onExit,
		stop:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
}

func (r *readLoop) Start() {
	go r.run()
}

func (r *readLoop) run() {
	var terminalErr error
	defer func() {
		if terminalErr != nil {
			r.inFlight.failAll(terminalErr)
		}
		close(r.stopped)
		if terminalErr != nil && r.onExit != nil {
			r.onExit(terminalErr)
		}
	}()
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		resp, err := r.transport.Receive(context.Background())
		if err != nil {
			select {
			case <-r.stop:
				return
			default:
				terminalErr = fmt.Errorf("mcp transport receive: %w", err)
				return
			}
		}
		// Notifications have no ID and carry a method. IDs may be either
		// numbers or strings; do not use zero as the presence check.
		if !resp.hasID && resp.ID == 0 && resp.StringID == "" && resp.Method != "" {
			if r.onNotify != nil {
				r.onNotify(resp.Method, resp.Params)
			}
			continue
		}
		if (resp.hasID || resp.ID != 0 || resp.StringID != "") && resp.Method != "" {
			r.handleServerRequest(resp)
			continue
		}
		if !r.inFlight.resolve(resp.ID, resp) {
			// Orphan response — ignore.
		}
	}
}

func (r *readLoop) handleServerRequest(req Response) {
	var result json.RawMessage
	var rpcErr *RPCError
	if r.onRequest != nil {
		result, rpcErr = r.onRequest(req.Method, req.Params)
	} else {
		rpcErr = &RPCError{Code: -32601, Message: "MCP client request is not supported"}
	}
	if rpcErr == nil && result == nil {
		result = json.RawMessage(`{}`)
	}
	_ = r.transport.Send(context.Background(), Request{
		JSONRPC:  "2.0",
		ID:       req.ID,
		StringID: req.StringID,
		stringID: req.stringID,
		hasID:    req.hasID,
		Result:   result,
		Error:    rpcErr,
	})
}

func (r *readLoop) Stop() {
	r.signalStop()
	<-r.stopped
}

func (r *readLoop) signalStop() {
	r.stopOnce.Do(func() { close(r.stop) })
}

// call performs a synchronous JSON-RPC call over the transport.
func call(ctx context.Context, t Transport, f *inFlight, method string, params any) (json.RawMessage, error) {
	return callWithProtocol(ctx, t, f, method, params, "")
}

func callWithProtocol(ctx context.Context, t Transport, f *inFlight, method string, params any, protocolVersion string) (json.RawMessage, error) {
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = b
	}
	if protocolVersion == PreferredProtocolVersion {
		var err error
		rawParams, err = addModernRequestMeta(rawParams)
		if err != nil {
			return nil, fmt.Errorf("marshal modern MCP request metadata: %w", err)
		}
	}
	id := nextRequestID()
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams}
	ch := f.register(id)
	if err := t.Send(ctx, req); err != nil {
		f.resolve(id, Response{Error: &RPCError{Code: -32000, Message: err.Error()}, ID: id, hasID: true})
		return nil, err
	}
	select {
	case <-ctx.Done():
		// Tell the server to stop working on this request (MCP
		// notifications/cancelled). Best-effort: ctx is already done, so the
		// notification goes out on a background context and errors are ignored.
		// The spec forbids cancelling initialize, so skip it there.
		f.drop(id)
		if method != "initialize" && method != "server/discover" {
			if p, err := json.Marshal(cancelledParams{RequestID: id, Reason: ctx.Err().Error()}); err == nil {
				_ = t.Send(context.Background(), Request{
					JSONRPC: "2.0",
					Method:  "notifications/cancelled",
					Params:  p,
				})
			}
		}
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		if protocolVersion == PreferredProtocolVersion {
			var envelope struct {
				ResultType string `json:"resultType"`
			}
			if err := json.Unmarshal(resp.Result, &envelope); err != nil {
				return nil, fmt.Errorf("decode MCP result envelope: %w", err)
			}
			switch envelope.ResultType {
			case "", "complete":
			case "input_required":
				return nil, errors.New("MCP request requires additional input, which Wuu does not support")
			default:
				return nil, fmt.Errorf("unsupported MCP result type %q", envelope.ResultType)
			}
		}
		return resp.Result, nil
	}
}

func addModernRequestMeta(params json.RawMessage) (json.RawMessage, error) {
	values := make(map[string]json.RawMessage)
	if len(params) > 0 {
		if err := json.Unmarshal(params, &values); err != nil {
			return nil, err
		}
	}
	meta, err := json.Marshal(map[string]any{
		"io.modelcontextprotocol/protocolVersion":    PreferredProtocolVersion,
		"io.modelcontextprotocol/clientInfo":         map[string]string{"name": "wuu", "version": "0.1.0"},
		"io.modelcontextprotocol/clientCapabilities": map[string]any{},
	})
	if err != nil {
		return nil, err
	}
	values["_meta"] = meta
	return json.Marshal(values)
}

// cancelledParams is the payload for MCP notifications/cancelled.
type cancelledParams struct {
	RequestID int64  `json:"requestId"`
	Reason    string `json:"reason,omitempty"`
}
