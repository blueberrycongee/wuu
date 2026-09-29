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
	"sync"
	"time"
)

const ProtocolVersion = 1
const MaxFrameBytes = 16 * 1024 * 1024

type Request struct {
	ID     string          `json:"id"`
	Method string          `json:"method"`
	Data   json.RawMessage `json:"data,omitempty"`
}

type Response struct {
	ID    string          `json:"id"`
	Data  json.RawMessage `json:"data,omitempty"`
	Error string          `json:"error,omitempty"`
}

// Client multiplexes a private stdio connection. A transport failure is terminal:
// tools with unknown outcomes must never be replayed automatically.
type Client struct {
	argv     []string
	env      []string
	mu       sync.Mutex
	writeMu  sync.Mutex
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	pending  map[string]chan Response
	sequence uint64
	failed   error
	done     chan struct{}
}

func NewClient(argv, env []string) *Client {
	return &Client{argv: append([]string(nil), argv...), env: append([]string(nil), env...), pending: make(map[string]chan Response)}
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
			c.mu.Lock()
			ch := c.pending[response.ID]
			delete(c.pending, response.ID)
			c.mu.Unlock()
			if ch != nil {
				ch <- response
			}
		}
		err := scan.Err()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		c.fail(fmt.Errorf("execution transport disconnected; the last operation may have completed: %w", err))
		_ = in.Close()
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
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
	for id, ch := range c.pending {
		ch <- Response{ID: id, Error: c.failed.Error()}
		delete(c.pending, id)
	}
}

func (c *Client) write(request Request) error {
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
	if err := ctx.Err(); err != nil {
		return nil, err
	}
	payload, err := json.Marshal(data)
	if err != nil {
		return nil, err
	}
	c.mu.Lock()
	if err = c.startLocked(); err != nil {
		c.mu.Unlock()
		return nil, err
	}
	c.sequence++
	id := strconv.FormatUint(c.sequence, 10)
	ch := make(chan Response, 1)
	c.pending[id] = ch
	c.mu.Unlock()
	if err = c.write(Request{ID: id, Method: method, Data: payload}); err != nil {
		c.fail(err)
		return nil, err
	}
	select {
	case response := <-ch:
		if response.Error != "" {
			return nil, errors.New(response.Error)
		}
		return response.Data, nil
	case <-ctx.Done():
		c.mu.Lock()
		delete(c.pending, id)
		c.mu.Unlock()
		// Cancellation is addressed to the existing operation, never a replacement run.
		go func() {
			if err := c.write(Request{ID: id, Method: "cancel"}); err != nil {
				c.fail(err)
			}
		}()
		return nil, ctx.Err()
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
	case <-time.After(3 * time.Second):
		_ = cmd.Process.Kill()
		<-done
	}
	return nil
}
