// Package claudeengine integrates the Claude Code CLI as an external agent
// engine behind the agentengine seam. The CLI runs in headless stream-json
// mode: one child per Wuu turn including agent follow-ups, NDJSON on stdio, session resume
// via --resume <session-id>.
package claudeengine

import (
	"bufio"
	"context"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"strings"
	"sync"
	"syscall"
	"time"
)

// maxLineBytes bounds a single NDJSON line. Tool results can be large.
const maxLineBytes = 32 * 1024 * 1024

// Transport is the NDJSON framing layer over one claude CLI child process.
// It owns the child lifecycle and the interrupt sequence (stdin EOF, then
// SIGINT, then SIGTERM, then SIGKILL).
type Transport struct {
	cmd *exec.Cmd

	mu           sync.Mutex
	stdin        io.WriteCloser
	closed       bool
	closeErr     error
	closeOnce    sync.Once
	terminateErr error

	gracePeriod time.Duration

	lineHandlers   []func(string)
	stderrHandlers []func(string)
	closeHandlers  []func(string)

	lineBuffer []string
	armed      bool
}

var (
	sigInterrupt = os.Interrupt
	sigTerminate = syscall.SIGTERM
)

// TransportOptions configures the claude child process.
type TransportOptions struct {
	// BinaryPath is the absolute path to the claude executable.
	BinaryPath string
	// Args are the full CLI arguments (headless stream-json flags).
	Args []string
	// CWD for the child; empty inherits the parent.
	CWD string
	// Env for the child; nil inherits the parent environment.
	Env []string
	// GracePeriod is how long to wait after stdin EOF before SIGINT.
	// Defaults to 500ms.
	GracePeriod time.Duration
}

// NewTransport spawns the claude child process.
func NewTransport(opts TransportOptions) (*Transport, error) {
	if strings.TrimSpace(opts.BinaryPath) == "" {
		return nil, errors.New("claude binary path is required")
	}
	cmd := exec.Command(opts.BinaryPath, opts.Args...)
	cmd.Dir = opts.CWD
	cmd.Env = opts.Env
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stdin pipe: %w", err)
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stdout pipe: %w", err)
	}
	stderr, err := cmd.StderrPipe()
	if err != nil {
		return nil, fmt.Errorf("claude stderr pipe: %w", err)
	}
	if err := cmd.Start(); err != nil {
		return nil, fmt.Errorf("start claude: %w", err)
	}
	t := &Transport{cmd: cmd, stdin: stdin}
	if opts.GracePeriod <= 0 {
		opts.GracePeriod = 500 * time.Millisecond
	}
	t.gracePeriod = opts.GracePeriod
	t.readLoop(stdout)
	t.stderrLoop(stderr)
	return t, nil
}

func (t *Transport) readLoop(r io.Reader) {
	go func() {
		reader := bufio.NewReaderSize(r, 64*1024)
		for {
			line, err := readLine(reader)
			if len(line) > 0 {
				t.mu.Lock()
				if !t.armed {
					t.lineBuffer = append(t.lineBuffer, line)
				} else {
					handlers := make([]func(string), len(t.lineHandlers))
					copy(handlers, t.lineHandlers)
					t.mu.Unlock()
					for _, h := range handlers {
						h(line)
					}
					continue
				}
				t.mu.Unlock()
			}
			if err != nil {
				if !errors.Is(err, io.EOF) {
					t.fireClose(fmt.Sprintf("read stdout: %v", err))
				} else {
					t.fireClose("claude exited")
				}
				return
			}
		}
	}()
}

func (t *Transport) stderrLoop(r io.Reader) {
	go func() {
		reader := bufio.NewReaderSize(r, 64*1024)
		for {
			line, err := readLine(reader)
			if len(line) > 0 {
				t.mu.Lock()
				handlers := make([]func(string), len(t.stderrHandlers))
				copy(handlers, t.stderrHandlers)
				t.mu.Unlock()
				for _, h := range handlers {
					h(line)
				}
			}
			if err != nil {
				return
			}
		}
	}()
}

func readLine(r *bufio.Reader) (string, error) {
	var sb strings.Builder
	for {
		chunk, err := r.ReadSlice('\n')
		if len(chunk) > 0 {
			line := strings.TrimSuffix(string(chunk), "\n")
			line = strings.TrimSuffix(line, "\r")
			if sb.Len()+len(line) > maxLineBytes {
				return "", fmt.Errorf("claude line exceeds %d bytes", maxLineBytes)
			}
			sb.WriteString(line)
		}
		if err == nil {
			return sb.String(), nil
		}
		if errors.Is(err, bufio.ErrBufferFull) {
			continue
		}
		return sb.String(), err
	}
}

// WriteLine sends one NDJSON line to the child.
func (t *Transport) WriteLine(ctx context.Context, line string) error {
	t.mu.Lock()
	if t.closed {
		err := t.closeErr
		t.mu.Unlock()
		if err == nil {
			err = errors.New("claude transport is closed")
		}
		return err
	}
	done := make(chan error, 1)
	go func() {
		_, err := io.WriteString(t.stdin, line+"\n")
		done <- err
	}()
	t.mu.Unlock()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		return ctx.Err()
	}
}

// OnLine registers a stdout line handler. The first registration drains the
// spawn-time buffer.
func (t *Transport) OnLine(h func(string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.lineHandlers = append(t.lineHandlers, h)
	if !t.armed {
		t.armed = true
		if len(t.lineBuffer) > 0 {
			buffered := append([]string(nil), t.lineBuffer...)
			t.lineBuffer = nil
			for _, line := range buffered {
				for _, hh := range t.lineHandlers {
					hh(line)
				}
			}
		}
	}
}

// OnStderr registers a diagnostics handler for stderr lines.
func (t *Transport) OnStderr(h func(string)) {
	t.mu.Lock()
	defer t.mu.Unlock()
	t.stderrHandlers = append(t.stderrHandlers, h)
}

// OnClose registers a handler for transport termination.
func (t *Transport) OnClose(h func(reason string)) {
	t.mu.Lock()
	if t.closed {
		reason := "claude transport closed"
		if t.closeErr != nil {
			reason = t.closeErr.Error()
		}
		t.mu.Unlock()
		h(reason)
		return
	}
	t.closeHandlers = append(t.closeHandlers, h)
	t.mu.Unlock()
}

func (t *Transport) fireClose(reason string) {
	t.mu.Lock()
	if t.closed {
		t.mu.Unlock()
		return
	}
	t.closed = true
	t.closeErr = errors.New(reason)
	handlers := make([]func(string), len(t.closeHandlers))
	copy(handlers, t.closeHandlers)
	t.mu.Unlock()
	for _, h := range handlers {
		h(reason)
	}
}

// Close terminates the child gracefully: stdin EOF first, then SIGINT after
// a grace period, then SIGTERM, then SIGKILL. The whole process group is
// targeted so tool subprocesses do not survive.
func (t *Transport) Close() error {
	t.closeOnce.Do(func() {
		t.terminateErr = t.closeWithGrace()
	})
	return t.terminateErr
}

func (t *Transport) closeWithGrace() error {
	t.mu.Lock()
	t.closed = true
	stdin := t.stdin
	grace := t.gracePeriod
	t.mu.Unlock()

	_ = stdin.Close()
	wait := make(chan error, 1)
	go func() { wait <- t.cmd.Wait() }()
	select {
	case <-wait:
		return nil
	case <-time.After(grace):
	}
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Signal(sigInterrupt)
	}
	select {
	case <-wait:
		return nil
	case <-time.After(grace):
	}
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Signal(sigTerminate)
	}
	select {
	case <-wait:
		return nil
	case <-time.After(2 * time.Second):
	}
	if t.cmd.Process != nil {
		_ = t.cmd.Process.Kill()
	}
	<-wait
	return errors.New("claude did not exit after SIGTERM; killed")
}
