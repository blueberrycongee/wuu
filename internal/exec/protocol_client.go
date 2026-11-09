package exec

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"

	"github.com/blueberrycongee/wuu/internal/appserver"
)

type Notification struct {
	Method string
	Params json.RawMessage
	// Err terminates delivery when the local event subscription fails.
	Err error
}

type protocolResponse struct {
	result json.RawMessage
	err    *appserver.ResponseError
}

type ProtocolError struct {
	Code    string
	Message string
}

func (e *ProtocolError) Error() string {
	if e == nil {
		return ""
	}
	if strings.TrimSpace(e.Code) == "" {
		return e.Message
	}
	return fmt.Sprintf("%s: %s", e.Code, e.Message)
}

type protocolEnvelope struct {
	ID     json.RawMessage          `json:"id,omitempty"`
	Method string                   `json:"method,omitempty"`
	Params json.RawMessage          `json:"params,omitempty"`
	Result json.RawMessage          `json:"result,omitempty"`
	Error  *appserver.ResponseError `json:"error,omitempty"`
}

type ProtocolClient struct {
	in            io.Reader
	out           io.Writer
	writeMu       sync.Mutex
	mu            sync.Mutex
	nextID        int64
	pending       map[string]chan protocolResponse
	notifications chan Notification
	readDone      chan error
}

func NewProtocolClient(in io.Reader, out io.Writer) *ProtocolClient {
	c := &ProtocolClient{
		in:            in,
		out:           out,
		pending:       make(map[string]chan protocolResponse),
		notifications: make(chan Notification, 256),
		readDone:      make(chan error, 1),
	}
	go c.readLoop()
	return c
}

func (c *ProtocolClient) Notifications() <-chan Notification {
	return c.notifications
}

func (c *ProtocolClient) Call(ctx context.Context, method string, params any, result any) error {
	if strings.TrimSpace(method) == "" {
		return errors.New("method is required")
	}
	id := c.nextRequestID()
	rawID := json.RawMessage(strconv.Quote(id))
	respCh := make(chan protocolResponse, 1)

	c.mu.Lock()
	c.pending[string(rawID)] = respCh
	c.mu.Unlock()
	defer func() {
		c.mu.Lock()
		delete(c.pending, string(rawID))
		c.mu.Unlock()
	}()

	rawParams, err := marshalOptionalParams(params)
	if err != nil {
		return err
	}
	req := protocolEnvelope{ID: rawID, Method: method, Params: rawParams}
	if err := c.writeJSON(req); err != nil {
		return err
	}

	select {
	case resp := <-respCh:
		if resp.err != nil {
			return &ProtocolError{Code: resp.err.Code, Message: resp.err.Message}
		}
		if result != nil && len(resp.result) > 0 {
			if err := json.Unmarshal(resp.result, result); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		return nil
	case err := <-c.readDone:
		if err == nil {
			err = io.EOF
		}
		return fmt.Errorf("app-server protocol closed before %s response: %w", method, err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *ProtocolClient) nextRequestID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return fmt.Sprintf("exec-%d", c.nextID)
}

func (c *ProtocolClient) readLoop() {
	dec := json.NewDecoder(c.in)
	var readErr error
	for {
		var msg protocolEnvelope
		if err := dec.Decode(&msg); err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
		if strings.TrimSpace(msg.Method) != "" && len(msg.ID) > 0 {
			readErr = fmt.Errorf(
				"unexpected app-server request %q",
				strings.TrimSpace(msg.Method),
			)
			break
		}
		if strings.TrimSpace(msg.Method) != "" {
			c.notifications <- Notification{Method: msg.Method, Params: msg.Params}
			continue
		}
		key := string(msg.ID)
		c.mu.Lock()
		respCh := c.pending[key]
		c.mu.Unlock()
		if respCh != nil {
			respCh <- protocolResponse{result: msg.Result, err: msg.Error}
		}
	}

	c.mu.Lock()
	for key, respCh := range c.pending {
		delete(c.pending, key)
		respCh <- protocolResponse{err: &appserver.ResponseError{Code: "closed", Message: "app-server protocol closed"}}
	}
	c.mu.Unlock()
	close(c.notifications)
	c.readDone <- readErr
	close(c.readDone)
}

func (c *ProtocolClient) writeJSON(v any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	data, err := json.Marshal(v)
	if err != nil {
		return err
	}
	if _, err := c.out.Write(append(data, '\n')); err != nil {
		return fmt.Errorf("write app-server request: %w", err)
	}
	return nil
}

func marshalOptionalParams(params any) (json.RawMessage, error) {
	if params == nil {
		return nil, nil
	}
	data, err := json.Marshal(params)
	if err != nil {
		return nil, fmt.Errorf("marshal params: %w", err)
	}
	return data, nil
}
