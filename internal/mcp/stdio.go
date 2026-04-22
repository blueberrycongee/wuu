package mcp

import (
	"bufio"
	"context"
	"encoding/json"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

// StdioTransport runs an MCP server as a subprocess and communicates over
// stdin/stdout. This is the most common transport for local MCP servers.
type StdioTransport struct {
	cmd    *exec.Cmd
	stdin  io.WriteCloser
	stdout io.ReadCloser
	stderr io.ReadCloser
	mu     sync.Mutex
	enc    *json.Encoder
	dec    *json.Decoder
}

// NewStdioTransport starts command as an MCP stdio server.
func NewStdioTransport(command string, args ...string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	// Forward stderr to a log file so it doesn't corrupt the TUI.
	go func() {
		// Best-effort drain; if logging isn't set up, discard.
		io.Copy(io.Discard, stderr)
	}()
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start command: %w", err)
	}
	t := &StdioTransport{
		cmd:    cmd,
		stdin:  stdin,
		stdout: stdout,
		stderr: stderr,
		enc:    json.NewEncoder(stdin),
		dec:    json.NewDecoder(bufio.NewReader(stdout)),
	}
	return t, nil
}

func (t *StdioTransport) Send(ctx context.Context, req Request) error {
	t.mu.Lock()
	defer t.mu.Unlock()
	return t.enc.Encode(req)
}

func (t *StdioTransport) Receive(ctx context.Context) (Response, error) {
	// Use a goroutine so we can respect context cancellation.
	type result struct {
		resp Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		var resp Response
		err := t.dec.Decode(&resp)
		ch <- result{resp, err}
	}()
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case r := <-ch:
		return r.resp, r.err
	}
}

func (t *StdioTransport) Close() error {
	// Graceful shutdown: close stdin, wait briefly, then kill.
	_ = t.stdin.Close()
	done := make(chan error, 1)
	go func() { done <- t.cmd.Wait() }()
	select {
	case <-done:
	case <-time.After(5 * time.Second):
		_ = t.cmd.Process.Kill()
	}
	return nil
}
