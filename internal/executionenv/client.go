package executionenv

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"strconv"
	"strings"
	"sync"
	"time"
)

// Version 5 adds actor-bound durable operation receipts and read-only status.
// Older workers fail closed rather than silently executing a different API.
const ProtocolVersion = 5
const MaxFrameBytes = 16 * 1024 * 1024

type Request struct {
	Token           string          `json:"token,omitempty"`
	OperationID     string          `json:"operation_id,omitempty"`
	OperationDigest string          `json:"operation_digest,omitempty"`
	ID              string          `json:"id"`
	Method          string          `json:"method"`
	Data            json.RawMessage `json:"data,omitempty"`
}

type Response struct {
	transportFailure bool

	Method    string          `json:"method,omitempty"`
	ParentID  string          `json:"parent_id,omitempty"`
	ID        string          `json:"id"`
	Data      json.RawMessage `json:"data,omitempty"`
	Error     string          `json:"error,omitempty"`
	ErrorKind string          `json:"error_kind,omitempty"`
}

// Client multiplexes a private stdio connection. A transport failure is terminal:
// tools with unknown outcomes must never be replayed automatically.
type Client struct {
	noOperationReceipts bool
	initializedRoot     string

	closeGrace time.Duration
	event      func(json.RawMessage)
	handlers   map[string]func(context.Context, json.RawMessage) (json.RawMessage, error)
	contexts   map[string]context.Context
	token      string
	argv       []string
	env        []string
	mu         sync.Mutex
	writeMu    sync.Mutex
	cmd        *exec.Cmd
	stdin      io.WriteCloser
	pending    map[string]chan Response
	retiring   map[string]chan struct{}
	sequence   uint64
	failed     error
	exitErr    error
	done       chan struct{}
}

func NewClient(argv, env []string) *Client {
	return &Client{argv: append([]string(nil), argv...), env: append([]string(nil), env...), pending: make(map[string]chan Response), retiring: make(map[string]chan struct{}), handlers: make(map[string]func(context.Context, json.RawMessage) (json.RawMessage, error)), contexts: make(map[string]context.Context)}
}

func (c *Client) startLocked() error {
	if c.failed != nil {
		return c.failed
	}
	if c.cmd != nil {
		return nil
	}
	if len(c.argv) == 0 {
		return errors.New("execution transport command is empty")
	}
	cmd := exec.Command(c.argv[0], c.argv[1:]...)
	cmd.Env = c.env
	cmd.WaitDelay = 2 * time.Second
	diagnostic := &transportDiagnostic{}
	cmd.Stderr = diagnostic
	out, err := cmd.StdoutPipe()
	if err != nil {
		return err
	}
	in, err := cmd.StdinPipe()
	if err != nil {
		_ = out.Close()
		return err
	}
	if err = cmd.Start(); err != nil {
		_ = out.Close()
		_ = in.Close()
		return fmt.Errorf("start execution transport: %w", err)
	}
	c.cmd, c.stdin, c.done = cmd, in, make(chan struct{})
	go func() {
		scan := bufio.NewScanner(out)
		scan.Buffer(make([]byte, 4096), MaxFrameBytes)
		for scan.Scan() {
			var response Response
			if err := json.Unmarshal(scan.Bytes(), &response); err != nil {
				c.fail(fmt.Errorf("invalid execution response: %w", err))
				break
			}
			if response.Method == "process" {
				if c.event != nil {
					c.event(response.Data)
				}
				continue
			}
			if response.Method == "tool" {
				c.mu.Lock()
				handler := c.handlers[response.ParentID]
				callCtx := c.contexts[response.ParentID]
				c.mu.Unlock()
				go func(callback Response) {
					reply := Response{ID: callback.ID}
					if handler == nil {
						reply.Error = "parent execution scope is closed"
					} else {
						var err error
						reply.Data, err = handler(callCtx, callback.Data)
						if err != nil {
							reply.Error = err.Error()
						}
					}
					data, _ := json.Marshal(reply)
					if err := c.write(Request{ID: callback.ID, Method: "callback", Data: data}); err != nil {
						c.fail(err)
					}
				}(response)
				continue
			}
			c.mu.Lock()
			ch := c.pending[response.ID]
			delete(c.pending, response.ID)
			if drained := c.retiring[response.ID]; drained != nil {
				close(drained)
				delete(c.retiring, response.ID)
			}
			c.mu.Unlock()
			if ch != nil {
				ch <- response
			}
		}
		scanErr := scan.Err()
		_ = in.Close()
		if scanErr != nil {
			_ = cmd.Process.Kill()
		}
		exited := make(chan error, 1)
		go func() { exited <- cmd.Wait() }()
		var waitErr error
		select {
		case waitErr = <-exited:
		case <-time.After(c.closeTimeout()):
			_ = cmd.Process.Kill()
			waitErr = <-exited
		}
		c.mu.Lock()
		if waitErr != nil {
			c.exitErr = fmt.Errorf("execution transport cleanup: %w: %s", waitErr, diagnostic.String())
		}
		c.mu.Unlock()
		if waitErr == nil {
			waitErr = io.ErrUnexpectedEOF
		}
		c.fail(fmt.Errorf("execution transport disconnected; the last operation may have completed: %w: %s", waitErr, diagnostic.String()))

		close(c.done)
	}()
	return nil
}

func (c *Client) fail(err error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	if c.failed == nil {
		c.failed = err
	}
	for id, drained := range c.retiring {
		close(drained)
		delete(c.retiring, id)
	}
	for id, ch := range c.pending {
		ch <- Response{ID: id, Error: c.failed.Error(), transportFailure: true}
		delete(c.pending, id)
	}
}

func (c *Client) write(request Request) error {
	request.Token = c.token
	data, err := json.Marshal(request)
	if err != nil {
		return err
	}
	if len(data)+1 > MaxFrameBytes {
		return errors.New("execution request exceeds transport limit")
	}
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	_, err = c.stdin.Write(append(data, '\n'))
	return err
}

func (c *Client) Call(ctx context.Context, method string, data any) (json.RawMessage, error) {
	return c.CallWithHandler(ctx, method, data, nil)
}

// CallOperation carries the identity chosen by the host execution owner.
// A worker without receipts receives one ordinary request; its lost result stays
// unknown. Reconnection and lookup are explicit; this method never resends work.
func (c *Client) CallOperation(ctx context.Context, operationID, method string, data any, handler func(context.Context, json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	if operationID == "" {
		return nil, errors.New("durable operation identity is required")
	}
	return c.call(ctx, operationID, method, data, handler)
}

func (c *Client) CallWithHandler(ctx context.Context, method string, data any, handler func(context.Context, json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	return c.call(ctx, "", method, data, handler)
}

func (c *Client) call(ctx context.Context, operationID, method string, data any, handler func(context.Context, json.RawMessage) (json.RawMessage, error)) (json.RawMessage, error) {
	failure := func(kind string, err error) error {
		if operationID == "" {
			return err
		}
		message := "this execution attempt was not started: " + err.Error()
		if kind == OperationUnknown {
			message = "execution outcome is unknown; do not repeat the operation: " + err.Error()
		}
		return &OperationError{Kind: kind, ID: operationID, Message: message, Cause: err}
	}
	if err := ctx.Err(); err != nil {
		return nil, failure(OperationNotStarted, err)
	}
	ctx, cancelScope := context.WithCancel(ctx)
	defer cancelScope()
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, failure(OperationNotStarted, err)
	}
	operationDigest := ""
	if operationID != "" {
		operationDigest, err = OperationDigest(method, payload)
		if err != nil {
			return nil, failure(OperationNotStarted, err)
		}
	}
	// A cancelled caller returns promptly, but reuse must wait for the worker's
	// terminal acknowledgement before another program reuses actor state.
	// Process controls remain available to release blocked I/O during cleanup.
	for {
		c.mu.Lock()
		if c.failed != nil {
			err = c.failed
			c.mu.Unlock()
			return nil, failure(OperationNotStarted, err)
		}
		var drained chan struct{}
		if method == "run_code" {
			for _, pending := range c.retiring {
				drained = pending
				break
			}
		}
		if drained == nil {
			break
		}
		c.mu.Unlock()
		select {
		case <-drained:
		case <-ctx.Done():
			return nil, failure(OperationNotStarted, ctx.Err())
		}
	}
	if err = c.startLocked(); err != nil {
		c.mu.Unlock()
		return nil, failure(OperationNotStarted, err)
	}
	c.sequence++
	id := strconv.FormatUint(c.sequence, 10)
	ch := make(chan Response, 1)
	c.pending[id] = ch
	c.handlers[id] = handler
	c.contexts[id] = ctx
	c.mu.Unlock()
	defer func() { c.mu.Lock(); delete(c.handlers, id); delete(c.contexts, id); c.mu.Unlock() }()
	written := make(chan error, 1)
	go func() {
		request := Request{ID: id, Method: method, Data: payload}
		if !c.noOperationReceipts {
			request.OperationID, request.OperationDigest = operationID, operationDigest
		}
		written <- c.write(request)
	}()
	select {
	case err = <-written:
	case <-ctx.Done():
		_ = c.stdin.Close()
		err = ctx.Err()
	}
	if err != nil {
		c.fail(err)
		return nil, failure(OperationUnknown, err)
	}
	select {
	case response := <-ch:
		if response.Error != "" {
			if response.transportFailure {
				return nil, failure(OperationUnknown, errors.New(response.Error))
			}
			if response.ErrorKind != "" {
				return nil, &OperationError{Kind: response.ErrorKind, ID: operationID, Message: response.Error}
			}
			return nil, errors.New(response.Error)
		}
		return response.Data, nil
	case <-ctx.Done():
		c.mu.Lock()
		if method == "run_code" && c.pending[id] != nil {
			c.retiring[id] = make(chan struct{})
		} else {
			delete(c.pending, id)
		}
		c.mu.Unlock()
		// Cancellation is addressed to the existing operation, never a replacement run.
		go func() {
			if err := c.write(Request{ID: id, Method: "cancel"}); err != nil {
				c.fail(err)
			}
		}()
		return nil, failure(OperationUnknown, ctx.Err())
	}
}

func (c *Client) Close() error {
	c.mu.Lock()
	if c.failed == nil {
		c.failed = errors.New("execution transport is closed")
	}
	in, cmd, done := c.stdin, c.cmd, c.done
	c.mu.Unlock()
	if in == nil {
		return nil
	}
	_ = in.Close()
	select {
	case <-done:
	case <-time.After(c.closeTimeout()):
		_ = cmd.Process.Kill()
		<-done
	}
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.exitErr
}

func (c *Client) closeTimeout() time.Duration {
	if c.closeGrace > 0 {
		return c.closeGrace
	}
	return 3 * time.Second
}

func (c *Client) Failed() bool {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.failed != nil
}

// Keep startup diagnostics bounded even when a backend continuously logs.
type transportDiagnostic struct {
	mu   sync.Mutex
	tail string
}

func (d *transportDiagnostic) Write(p []byte) (int, error) {
	d.mu.Lock()
	defer d.mu.Unlock()
	d.tail += string(p)
	if len(d.tail) > 8192 {
		d.tail = d.tail[len(d.tail)-8192:]
	}
	return len(p), nil
}
func (d *transportDiagnostic) String() string {
	d.mu.Lock()
	defer d.mu.Unlock()
	return strings.TrimSpace(d.tail)
}
