package codexengine

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"sync"
	"time"
)

// RPCError is a JSON-RPC error object as returned by the codex app-server.
type RPCError struct {
	Code    int             `json:"code"`
	Message string          `json:"message"`
	Data    json.RawMessage `json:"data,omitempty"`
}

func (e *RPCError) Error() string {
	return fmt.Sprintf("codex app-server error %d: %s", e.Code, e.Message)
}

// incomingMessage is one NDJSON line from the app-server. Codex's JSON-RPC
// omits the jsonrpc:"2.0" envelope field; the shape is decided by which of
// id/method/result/error are present.
type incomingMessage struct {
	ID     json.RawMessage `json:"id"`
	Method string          `json:"method"`
	Params json.RawMessage `json:"params"`
	Result json.RawMessage `json:"result"`
	Error  *RPCError       `json:"error"`
}

// Client is the JSON-RPC NDJSON client over a codex app-server Transport.
type Client struct {
	transport   *Transport
	mu          sync.Mutex
	nextID      int64
	notifyToken uint64
	pending     map[int64]*pendingRequest
	notify      map[string][]*notifHandler
	requests    map[string][]*requestHandler
	closed      bool
	closeErr    error
}

// notifHandler is one registered notification handler with a unique token so
// unsubscribing removes exactly the right handler even when multiple
// subscriptions share a method.
type notifHandler struct {
	token uint64
	fn    func(json.RawMessage)
}

type requestHandler struct {
	fn func(json.RawMessage) (any, error)
}

var errRequestNotForSession = errors.New("request belongs to another Codex session")

type pendingRequest struct {
	reply chan json.RawMessage
	err   chan error
	timer *time.Timer
}

// NewClient wraps a transport. Call Start to begin consuming stdout.
func NewClient(t *Transport) *Client {
	return &Client{
		transport: t,
		pending:   make(map[int64]*pendingRequest),
		notify:    make(map[string][]*notifHandler),
		requests:  make(map[string][]*requestHandler),
	}
}

// Start begins the line-dispatch loop. Must be called once after all
// handlers are registered so no early line is missed (the transport buffers
// lines until the first handler attaches).
func (c *Client) Start() {
	c.transport.OnLine(c.handleLine)
	c.transport.OnClose(func(reason string) {
		c.mu.Lock()
		closed := c.closed
		c.closed = true
		c.closeErr = fmt.Errorf("codex app-server closed: %s", reason)
		pending := make([]*pendingRequest, 0, len(c.pending))
		for _, p := range c.pending {
			pending = append(pending, p)
		}
		c.pending = map[int64]*pendingRequest{}
		c.mu.Unlock()
		if !closed {
			for _, p := range pending {
				p.fail(c.closeErr)
			}
		}
	})
}

// OnNotification registers a handler for a notification method. The returned
// function removes exactly this handler. Unhandled notifications are ignored.
func (c *Client) OnNotification(method string, h func(json.RawMessage)) func() {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.notifyToken++
	handler := &notifHandler{token: c.notifyToken, fn: h}
	c.notify[method] = append(c.notify[method], handler)
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		handlers := c.notify[method]
		for i, existing := range handlers {
			if existing == handler {
				c.notify[method] = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
	}
}

// OnRequest registers a handler for an inbound server request. The handler's
// result is sent back as the JSON-RPC response; an error is sent as the
// error response. Without a handler the client answers method-not-found so
// the app-server never blocks on us.
func (c *Client) OnRequest(method string, h func(json.RawMessage) (any, error)) func() {
	handler := &requestHandler{fn: h}
	c.mu.Lock()
	c.requests[method] = append(c.requests[method], handler)
	c.mu.Unlock()
	return func() {
		c.mu.Lock()
		defer c.mu.Unlock()
		handlers := c.requests[method]
		for i, current := range handlers {
			if current == handler {
				handlers = append(handlers[:i], handlers[i+1:]...)
				break
			}
		}
		if len(handlers) == 0 {
			delete(c.requests, method)
		} else {
			c.requests[method] = handlers
		}
	}
}

// Request sends a JSON-RPC request and decodes the result into result.
func (c *Client) Request(ctx context.Context, method string, params, result any) error {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		if err == nil {
			err = errors.New("codex app-server client is closed")
		}
		return err
	}
	c.nextID++
	id := c.nextID
	p := &pendingRequest{reply: make(chan json.RawMessage, 1), err: make(chan error, 1)}
	c.pending[id] = p
	c.mu.Unlock()

	payload, err := json.Marshal(map[string]any{
		"id":     id,
		"method": method,
		"params": params,
	})
	if err != nil {
		c.drop(id)
		return fmt.Errorf("encode %s request: %w", method, err)
	}
	if err := c.transport.WriteLine(ctx, string(payload)); err != nil {
		c.drop(id)
		return fmt.Errorf("send %s request: %w", method, err)
	}

	select {
	case raw := <-p.reply:
		if result != nil {
			if err := json.Unmarshal(raw, result); err != nil {
				return fmt.Errorf("decode %s response: %w", method, err)
			}
		}
		return nil
	case err := <-p.err:
		return err
	case <-ctx.Done():
		c.drop(id)
		return ctx.Err()
	}
}

func (c *Client) drop(id int64) {
	c.mu.Lock()
	p, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if ok && p.timer != nil {
		p.timer.Stop()
	}
}

func (p *pendingRequest) fail(err error) {
	select {
	case p.err <- err:
	default:
	}
	if p.timer != nil {
		p.timer.Stop()
	}
}

// handleLine dispatches one incoming NDJSON message.
func (c *Client) handleLine(line string) {
	var msg incomingMessage
	if err := json.Unmarshal([]byte(line), &msg); err != nil {
		c.fatal(fmt.Sprintf("codex app-server sent invalid JSON: %v", err))
		return
	}
	switch {
	case len(msg.ID) > 0 && msg.Method != "":
		// Inbound server request: must answer.
		var number int64
		var text string
		if string(msg.ID) == "null" || (json.Unmarshal(msg.ID, &number) != nil && json.Unmarshal(msg.ID, &text) != nil) {
			return
		}
		// Approval handlers may wait minutes for a user decision. Keep the
		// stdout reader free so notifications and other threads continue.
		go c.dispatchServerRequest(msg.ID, msg.Method, msg.Params)
	case len(msg.ID) > 0:
		// Response to one of our requests.
		c.dispatchResponse(msg.ID, msg.Result, msg.Error)
	default:
		// Notification.
		c.dispatchNotification(msg.Method, msg.Params)
	}
}

func (c *Client) dispatchResponse(rawID json.RawMessage, result json.RawMessage, rpcErr *RPCError) {
	var id int64
	if err := json.Unmarshal(rawID, &id); err != nil {
		return
	}
	c.mu.Lock()
	p, ok := c.pending[id]
	if ok {
		delete(c.pending, id)
	}
	c.mu.Unlock()
	if !ok {
		return
	}
	if p.timer != nil {
		p.timer.Stop()
	}
	if rpcErr != nil {
		p.fail(rpcErr)
		return
	}
	p.reply <- result
}

func (c *Client) dispatchNotification(method string, params json.RawMessage) {
	c.mu.Lock()
	handlers := make([]*notifHandler, len(c.notify[method]))
	copy(handlers, c.notify[method])
	c.mu.Unlock()
	for _, handler := range handlers {
		handler.fn(params)
	}
}

func (c *Client) dispatchServerRequest(id json.RawMessage, method string, params json.RawMessage) {
	c.mu.Lock()
	handlers := append([]*requestHandler(nil), c.requests[method]...)
	c.mu.Unlock()
	if len(handlers) == 0 {
		c.writeError(id, &RPCError{Code: -32601, Message: "no handler registered for " + method})
		return
	}
	for _, handler := range handlers {
		result, err := handler.fn(params)
		if errors.Is(err, errRequestNotForSession) {
			continue
		}
		if err != nil {
			var rpcErr *RPCError
			if errors.As(err, &rpcErr) {
				c.writeError(id, rpcErr)
				return
			}
			c.writeError(id, &RPCError{Code: -32603, Message: err.Error()})
			return
		}
		c.writeResult(id, result)
		return
	}
	c.writeError(id, &RPCError{Code: -32601, Message: "no handler registered for " + method})
}

func (c *Client) writeResult(id json.RawMessage, result any) {
	payload, err := json.Marshal(map[string]any{"id": id, "result": result})
	if err != nil {
		return
	}
	_ = c.transport.WriteLine(context.Background(), string(payload))
}

func (c *Client) writeError(id json.RawMessage, rpcErr *RPCError) {
	payload, err := json.Marshal(map[string]any{"id": id, "error": rpcErr})
	if err != nil {
		return
	}
	_ = c.transport.WriteLine(context.Background(), string(payload))
}

func (c *Client) fatal(reason string) {
	c.mu.Lock()
	if c.closed {
		c.mu.Unlock()
		return
	}
	c.closed = true
	c.closeErr = errors.New(reason)
	c.mu.Unlock()
	_ = c.transport.Close()
}

// Close shuts down the client and its transport.
func (c *Client) Close() error {
	c.mu.Lock()
	if c.closed {
		err := c.closeErr
		c.mu.Unlock()
		return err
	}
	c.closed = true
	c.mu.Unlock()
	return c.transport.Close()
}
