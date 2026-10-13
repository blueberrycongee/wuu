package mcp

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"log/slog"
	"sync"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
)

// cancelNotificationTimeout bounds the best-effort notifications/cancelled that
// follows a cancelled call, so an unresponsive server cannot hold the caller.
const cancelNotificationTimeout = 2 * time.Second

const (
	serverReplyWorkers   = 4
	serverReplyQueueSize = 32
	// The budget includes queue waiting, not just the HTTP round trip.
	serverReplyTimeout = 5 * time.Second
)

type serverRequestID struct {
	number   int64
	text     string
	isString bool
}

func serverID(req Response) serverRequestID {
	return serverRequestID{number: req.ID, text: req.StringID, isString: req.stringID}
}

type serverReply struct {
	message      Request
	origin       *serverRequestOrigin
	ctx          context.Context
	cancel       context.CancelFunc
	detachOrigin func() bool
}

// Transport is the low-level JSON-RPC transport for MCP.
type Transport interface {
	// Send writes a JSON-RPC request, response, or notification to the server.
	// The context also owns any asynchronous response stream for that message.
	Send(ctx context.Context, req Request) error
	// Receive reads the next JSON-RPC response or notification from the server.
	Receive(ctx context.Context) (Response, error)
	// Close shuts down the transport.
	Close() error
}

// readLoop pumps JSON-RPC messages from a transport into the in-flight tracker.
type readLoop struct {
	transport      Transport
	inFlight       *inFlight
	onNotify       func(method string, params json.RawMessage)
	onRequest      func(method string, params json.RawMessage) (json.RawMessage, *RPCError)
	onExit         func(error)
	stop           chan struct{}
	stopped        chan struct{}
	stopOnce       sync.Once
	ctx            context.Context
	cancel         context.CancelFunc
	replies        chan *serverReply
	replyWG        sync.WaitGroup
	replyMu        sync.Mutex
	pendingReplies map[serverRequestID]*serverReply
}

func newReadLoop(t Transport, f *inFlight, onNotify func(method string, params json.RawMessage), onRequest func(method string, params json.RawMessage) (json.RawMessage, *RPCError), onExit func(error)) *readLoop {
	ctx, cancel := context.WithCancel(context.Background())
	return &readLoop{
		transport:      t,
		inFlight:       f,
		onNotify:       onNotify,
		onRequest:      onRequest,
		onExit:         onExit,
		stop:           make(chan struct{}),
		stopped:        make(chan struct{}),
		ctx:            ctx,
		cancel:         cancel,
		replies:        make(chan *serverReply, serverReplyQueueSize),
		pendingReplies: make(map[serverRequestID]*serverReply),
	}
}

func (r *readLoop) Start() {
	r.replyWG.Add(serverReplyWorkers)
	for range serverReplyWorkers {
		go r.sendServerReplies()
	}
	go r.run()
}

func (r *readLoop) run() {
	var terminalErr error
	defer func() {
		r.cancel()
		close(r.replies)
		r.replyWG.Wait()
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
		resp, err := r.transport.Receive(r.ctx)
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
			if resp.Method == "notifications/cancelled" {
				r.cancelServerReply(resp.Params)
			}
			if r.onNotify != nil {
				r.onNotify(resp.Method, resp.Params)
			}
			continue
		}
		if (resp.hasID || resp.ID != 0 || resp.StringID != "") && resp.Method != "" {
			if err := r.handleServerRequest(resp); err != nil {
				terminalErr = err
				return
			}
			continue
		}
		if !r.inFlight.resolve(resp.ID, resp) {
			// Orphan response — ignore.
		}
	}
}

func (r *readLoop) handleServerRequest(req Response) error {
	if r.ctx.Err() != nil || (req.requestOrigin != nil && req.requestOrigin.ctx.Err() != nil) {
		return nil
	}
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
	ctx, cancel := context.WithTimeout(r.ctx, serverReplyTimeout)
	reply := &serverReply{ctx: ctx, cancel: cancel, origin: req.requestOrigin}
	if reply.origin != nil {
		reply.detachOrigin = context.AfterFunc(reply.origin.ctx, cancel)
	}
	reply.message = Request{
		requestOrigin: req.requestOrigin,
		JSONRPC:       "2.0",
		ID:            req.ID,
		StringID:      req.StringID,
		stringID:      req.stringID,
		hasID:         req.hasID,
		Result:        result,
		Error:         rpcErr,
	}
	id := serverID(req)
	r.replyMu.Lock()
	// The server may reuse an ID after receiving its reply even while the
	// HTTP acknowledgement is still draining. Cleanup must not erase its new reply.
	r.pendingReplies[id] = reply
	r.replyMu.Unlock()
	select {
	case r.replies <- reply:
		return nil
	default:
		r.finishServerReply(id, reply)
		// Never wait for queue space on the receive loop. A POST-owned failure
		// settles only that call; an unsolicited flood fails the connection.
		return r.rejectServerReply(reply, errors.New("mcp server reply capacity exceeded"))
	}
}

func (r *readLoop) sendServerReplies() {
	defer r.replyWG.Done()
	for reply := range r.replies {
		if reply.origin != nil && reply.origin.ctx.Err() != nil {
			reply.cancel()
		}
		err := reply.ctx.Err()
		if err == nil {
			err = r.transport.Send(reply.ctx, reply.message)
			if err == nil {
				err = reply.ctx.Err()
			}
		}
		if err != nil && r.ctx.Err() == nil && reply.ctx.Err() != context.Canceled {
			if err := r.rejectServerReply(reply, fmt.Errorf("mcp server request reply: %w", err)); err != nil {
				// Unsolicited replies have no client call to fail. Their send
				// remains bounded and observable without killing healthy calls.
				slog.Warn("MCP server reply failed", "error", err)
			}
		}
		id := serverRequestID{number: reply.message.ID, text: reply.message.StringID, isString: reply.message.stringID}
		r.finishServerReply(id, reply)
	}
}

func (r *readLoop) rejectServerReply(reply *serverReply, err error) error {
	if reply.origin == nil || !reply.origin.isCall {
		return err
	}
	if reply.origin.ctx.Err() == nil {
		r.inFlight.resolve(reply.origin.callID, Response{ID: reply.origin.callID, hasID: true, Error: &RPCError{Code: -32000, Message: err.Error()}})
	}
	return nil
}

func (r *readLoop) finishServerReply(id serverRequestID, reply *serverReply) {
	if reply.detachOrigin != nil {
		reply.detachOrigin()
	}
	reply.cancel()
	r.replyMu.Lock()
	if r.pendingReplies[id] == reply {
		delete(r.pendingReplies, id)
	}
	r.replyMu.Unlock()
}

func (r *readLoop) cancelServerReply(params json.RawMessage) {
	var notification struct {
		RequestID json.RawMessage `json:"requestId"`
	}
	if json.Unmarshal(params, &notification) != nil || len(notification.RequestID) == 0 {
		return
	}
	// Reuse the wire decoder so numeric zero and empty/string IDs keep their
	// presence and type, rather than colliding in the opposite ID namespace.
	var request Response
	if json.Unmarshal(append(append([]byte(`{"id":`), notification.RequestID...), '}'), &request) != nil || !request.hasID {
		return
	}
	r.replyMu.Lock()
	if reply := r.pendingReplies[serverID(request)]; reply != nil {
		reply.cancel()
	}
	r.replyMu.Unlock()
}

func (r *readLoop) Stop() {
	r.signalStop()
	<-r.stopped
}

func (r *readLoop) signalStop() {
	r.stopOnce.Do(func() {
		close(r.stop)
		r.cancel()
	})
}

// call performs a synchronous JSON-RPC call over the transport.
func call(ctx context.Context, t Transport, f *inFlight, method string, params any) (json.RawMessage, error) {
	return callWithProtocol(ctx, t, f, method, params, "")
}

func callWithProtocol(ctx context.Context, t Transport, f *inFlight, method string, params any, protocolVersion string) (json.RawMessage, error) {
	if err := activity.BoundControlErr(ctx); err != nil {
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
	// Completion must release request-owned transport resources even when the
	// caller keeps its context alive for subsequent calls.
	ctx, finish := context.WithCancel(ctx)
	defer finish()

	id := nextRequestID()
	req := Request{JSONRPC: "2.0", ID: id, Method: method, Params: rawParams}
	if err := activity.BoundControlErr(ctx); err != nil {
		return nil, err
	}
	ch := f.register(id)
	if err := t.Send(ctx, req); err != nil {
		f.resolve(id, Response{Error: &RPCError{Code: -32000, Message: err.Error()}, ID: id, hasID: true})
		return nil, err
	}
	select {
	case <-ctx.Done():
		// Tell the server to stop working on this request (MCP
		// notifications/cancelled). Best-effort: ctx is already done, so the
		// notification goes out on its own bounded context and errors are
		// ignored. The spec forbids cancelling initialize, so skip it there.
		f.drop(id)
		if method != "initialize" && method != "server/discover" {
			if p, err := json.Marshal(cancelledParams{RequestID: id, Reason: ctx.Err().Error()}); err == nil {
				cancelCtx, stop := context.WithTimeout(context.Background(), cancelNotificationTimeout)
				// The notification has its own cleanup budget. Waiting for it
				// here would extend the expired tool/caller deadline.
				go func() {
					defer stop()
					_ = t.Send(cancelCtx, Request{
						JSONRPC: "2.0",
						Method:  "notifications/cancelled",
						Params:  p,
					})
				}()
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
