package sdk

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"strconv"
	"strings"
	"sync"
)

type protocolResponseError struct {
	Code    string `json:"code"`
	Message string `json:"message"`
}

type protocolResponse struct {
	result json.RawMessage
	err    *protocolResponseError
}

type protocolEnvelope struct {
	ID     json.RawMessage        `json:"id,omitempty"`
	Method string                 `json:"method,omitempty"`
	Params json.RawMessage        `json:"params,omitempty"`
	Result json.RawMessage        `json:"result,omitempty"`
	Error  *protocolResponseError `json:"error,omitempty"`
}

type protocolClient struct {
	in      io.Reader
	out     io.Writer
	writeMu sync.Mutex

	mu      sync.Mutex
	nextID  int64
	pending map[string]chan protocolResponse
	readErr error

	events chan Event
	done   chan struct{}
}

func newProtocolClient(in io.Reader, out io.Writer) *protocolClient {
	c := &protocolClient{
		in:      in,
		out:     out,
		pending: make(map[string]chan protocolResponse),
		// forwardEvents never waits for consumers: it dispatches to their
		// bounded queues. Keep payload buffering in that one layer.
		events: make(chan Event),
		done:   make(chan struct{}),
	}
	go c.readLoop()
	return c
}

func (c *protocolClient) call(ctx context.Context, method string, params any, result any) error {
	if strings.TrimSpace(method) == "" {
		return errors.New("method is required")
	}
	if ctx == nil {
		return errors.New("context is required")
	}
	id := c.nextRequestID()
	rawID := json.RawMessage(strconv.Quote(id))
	response := make(chan protocolResponse, 1)

	c.mu.Lock()
	c.pending[string(rawID)] = response
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
	if err := c.writeJSON(protocolEnvelope{ID: rawID, Method: method, Params: rawParams}); err != nil {
		return err
	}

	select {
	case response := <-response:
		if response.err != nil {
			return &ProtocolError{Code: response.err.Code, Message: response.err.Message}
		}
		if result != nil && len(response.result) > 0 {
			if err := json.Unmarshal(response.result, result); err != nil {
				return fmt.Errorf("decode %s result: %w", method, err)
			}
		}
		return nil
	case <-c.done:
		err := c.err()
		if err == nil {
			err = io.EOF
		}
		return fmt.Errorf("app-server protocol closed before %s response: %w", method, err)
	case <-ctx.Done():
		return ctx.Err()
	}
}

func (c *protocolClient) nextRequestID() string {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.nextID++
	return fmt.Sprintf("sdk-%d", c.nextID)
}

func (c *protocolClient) readLoop() {
	decoder := json.NewDecoder(c.in)
	var readErr error
	for {
		var message protocolEnvelope
		if err := decoder.Decode(&message); err != nil {
			if !errors.Is(err, io.EOF) {
				readErr = err
			}
			break
		}
		if strings.TrimSpace(message.Method) != "" && len(message.ID) > 0 {
			readErr = fmt.Errorf("unexpected app-server request %q", strings.TrimSpace(message.Method))
			break
		}
		if strings.TrimSpace(message.Method) != "" {
			c.events <- Event{Method: message.Method, Params: cloneRaw(message.Params)}
			continue
		}

		c.mu.Lock()
		response := c.pending[string(message.ID)]
		c.mu.Unlock()
		if response != nil {
			response <- protocolResponse{result: message.Result, err: message.Error}
		}
	}

	c.mu.Lock()
	c.readErr = readErr
	for key, response := range c.pending {
		delete(c.pending, key)
		response <- protocolResponse{err: &protocolResponseError{Code: "closed", Message: "app-server protocol closed"}}
	}
	c.mu.Unlock()
	close(c.events)
	close(c.done)
}

func (c *protocolClient) err() error {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.readErr
}

func (c *protocolClient) writeJSON(value any) error {
	c.writeMu.Lock()
	defer c.writeMu.Unlock()
	data, err := json.Marshal(value)
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

func cloneRaw(raw json.RawMessage) json.RawMessage {
	return append(json.RawMessage(nil), raw...)
}
