package mcp

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"time"
)

// StdioTransport runs an MCP server as a subprocess and communicates over
// stdin/stdout. This is the most common transport for local MCP servers.
type StdioTransport struct {
	cmd          *exec.Cmd
	processGroup *processGroup
	stdin        io.WriteCloser
	stdout       io.ReadCloser
	stderr       io.ReadCloser
	// sendSlot serializes writes to stdin. It is a channel rather than a mutex
	// so a caller can stop waiting for it when its context ends.
	sendSlot  chan struct{}
	reader    *bufio.Reader
	stderrMu  sync.Mutex
	stderrBuf bytes.Buffer
	closeOnce sync.Once
	closeErr  error
}

// NewStdioTransport starts command as an MCP stdio server.
func NewStdioTransport(command string, args ...string) (*StdioTransport, error) {
	return NewStdioTransportWithEnv(command, args, nil)
}

// NewStdioTransportWithEnv starts command as an MCP stdio server with
// additional environment variables overlaid on the current process env.
func NewStdioTransportWithEnv(command string, args []string, env map[string]string) (*StdioTransport, error) {
	cmd := exec.Command(command, args...)
	processGroup := configureProcessGroup(cmd)
	cmd.Env = mergeProcessEnv(os.Environ(), env)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		_ = closeProcessGroup(processGroup)
		return nil, fmt.Errorf("stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		_ = closeProcessGroup(processGroup)
		return nil, fmt.Errorf("stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = closeProcessGroup(processGroup)
		return nil, fmt.Errorf("stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		_ = closeProcessGroup(processGroup)
		return nil, fmt.Errorf("start command: %w", err)
	}
	if err := startProcessGroup(cmd, processGroup); err != nil {
		_ = killProcessTree(cmd, processGroup)
		_ = cmd.Process.Kill()
		_ = cmd.Wait()
		_ = stdin.Close()
		_ = stdout.Close()
		_ = stderr.Close()
		_ = closeProcessGroup(processGroup)
		return nil, fmt.Errorf("start process group: %w", err)
	}
	t := &StdioTransport{
		cmd:          cmd,
		processGroup: processGroup,
		stdin:        stdin,
		stdout:       stdout,
		stderr:       stderr,
		sendSlot:     make(chan struct{}, 1),
		reader:       bufio.NewReader(stdout),
	}
	go t.captureStderr()
	return t, nil
}

func (t *StdioTransport) captureStderr() {
	buf := make([]byte, 4096)
	for {
		n, err := t.stderr.Read(buf)
		if n > 0 {
			t.stderrMu.Lock()
			t.stderrBuf.Write(buf[:n])
			if t.stderrBuf.Len() > maxMCPBodyExcerptBytes {
				data := append([]byte(nil), t.stderrBuf.Bytes()[t.stderrBuf.Len()-maxMCPBodyExcerptBytes:]...)
				t.stderrBuf.Reset()
				_, _ = t.stderrBuf.Write(data)
			}
			t.stderrMu.Unlock()
		}
		if err != nil {
			return
		}
	}
}

func (t *StdioTransport) stderrTail() string {
	t.stderrMu.Lock()
	defer t.stderrMu.Unlock()
	return strings.TrimSpace(t.stderrBuf.String())
}

func mergeProcessEnv(base []string, overlay map[string]string) []string {
	if len(overlay) == 0 {
		out := make([]string, len(base))
		copy(out, base)
		return out
	}
	seen := make(map[string]int, len(base)+len(overlay))
	out := make([]string, 0, len(base)+len(overlay))
	for _, item := range base {
		key, _, ok := strings.Cut(item, "=")
		if !ok {
			out = append(out, item)
			continue
		}
		seen[key] = len(out)
		out = append(out, item)
	}
	for key, value := range overlay {
		if key == "" {
			continue
		}
		item := key + "=" + value
		if idx, ok := seen[key]; ok {
			out[idx] = item
			continue
		}
		seen[key] = len(out)
		out = append(out, item)
	}
	return out
}

func (t *StdioTransport) Send(ctx context.Context, req Request) error {
	frame, err := json.Marshal(req)
	if err != nil {
		return fmt.Errorf("marshal message: %w", err)
	}
	frame = append(frame, '\n')
	select {
	case t.sendSlot <- struct{}{}:
	case <-ctx.Done():
		return ctx.Err()
	}
	written := make(chan error, 1)
	go func() {
		_, err := t.stdin.Write(frame)
		<-t.sendSlot
		written <- err
	}()
	select {
	case err := <-written:
		return err
	case <-ctx.Done():
	}
	select {
	case err := <-written:
		return err
	default:
	}
	// The server stopped reading and the write may have left half a frame, so
	// this stream cannot carry another message. Closing the connection also
	// unblocks the pending write; the reader then reports the exit.
	go func() { _ = t.Close() }()
	return ctx.Err()
}

func (t *StdioTransport) Receive(ctx context.Context) (Response, error) {
	// Use a goroutine so we can respect context cancellation.
	type result struct {
		resp Response
		err  error
	}
	ch := make(chan result, 1)
	go func() {
		data, err := readStdioMessage(t.reader)
		var resp Response
		if err == nil {
			err = json.Unmarshal(data, &resp)
		}
		if err != nil {
			if detail := t.stderrTail(); detail != "" {
				err = fmt.Errorf("%w; server stderr: %s", err, detail)
			}
		}
		ch <- result{resp, err}
	}()
	select {
	case <-ctx.Done():
		return Response{}, ctx.Err()
	case r := <-ch:
		return r.resp, r.err
	}
}

func readStdioMessage(reader *bufio.Reader) ([]byte, error) {
	var message []byte
	for {
		part, err := reader.ReadSlice('\n')
		if len(part) > 0 {
			if len(message)+len(part) > maxMCPMessageBytes {
				return nil, fmt.Errorf("stdio MCP message exceeds %d bytes", maxMCPMessageBytes)
			}
			message = append(message, part...)
		}
		if err == bufio.ErrBufferFull {
			continue
		}
		if err != nil {
			return nil, err
		}
		return bytes.TrimSpace(message), nil
	}
}

func (t *StdioTransport) Close() error {
	t.closeOnce.Do(func() {
		// Graceful shutdown: close stdin, wait briefly, then kill and reap.
		_ = t.stdin.Close()
		defer func() {
			_ = t.stdout.Close()
			_ = t.stderr.Close()
		}()
		done := make(chan error, 1)
		go func() { done <- t.cmd.Wait() }()
		defer func() {
			if err := closeProcessGroup(t.processGroup); err != nil && t.closeErr == nil {
				t.closeErr = err
			}
		}()
		select {
		case <-done:
		case <-time.After(5 * time.Second):
			if err := killProcessTree(t.cmd, t.processGroup); err != nil {
				if !errors.Is(err, os.ErrProcessDone) {
					t.closeErr = err
					return
				}
			}
			<-done
		}
	})
	return t.closeErr
}
