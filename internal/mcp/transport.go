package mcp

import (
	"context"
	"encoding/json"
	"fmt"
	"io"
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
	stop      chan struct{}
	stopped   chan struct{}
}

func newReadLoop(t Transport, f *inFlight, onNotify func(method string, params json.RawMessage)) *readLoop {
	return &readLoop{
		transport: t,
		inFlight:  f,
		onNotify:  onNotify,
		stop:      make(chan struct{}),
		stopped:   make(chan struct{}),
	}
}

func (r *readLoop) Start() {
	go r.run()
}

func (r *readLoop) run() {
	defer close(r.stopped)
	for {
		select {
		case <-r.stop:
			return
		default:
		}
		resp, err := r.transport.Receive(context.Background())
		if err != nil {
			if err == io.EOF {
				return
			}
			select {
			case <-r.stop:
				return
			default:
				continue
			}
		}
		// Notifications have no ID.
		if resp.ID == 0 && r.onNotify != nil {
			// We can't distinguish method from Response; this is a
			// limitation of the simple transport interface. For now,
			// notifications are best-effort.
			continue
		}
		if !r.inFlight.resolve(resp.ID, resp) {
			// Orphan response — ignore.
		}
	}
}

func (r *readLoop) Stop() {
	close(r.stop)
	<-r.stopped
}

// call performs a synchronous JSON-RPC call over the transport.
func call(ctx context.Context, t Transport, f *inFlight, method string, params any) (json.RawMessage, error) {
	var rawParams json.RawMessage
	if params != nil {
		b, err := json.Marshal(params)
		if err != nil {
			return nil, fmt.Errorf("marshal params: %w", err)
		}
		rawParams = b
	}
	id := nextRequestID()
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams}
	ch := f.register(id)
	if err := t.Send(ctx, req); err != nil {
		f.resolve(id, Response{Error: &RPCError{Code: -32000, Message: err.Error()}})
		return nil, err
	}
	select {
	case <-ctx.Done():
		return nil, ctx.Err()
	case resp := <-ch:
		if resp.Error != nil {
			return nil, resp.Error
		}
		return resp.Result, nil
	}
}
