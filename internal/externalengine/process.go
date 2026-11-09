package externalengine

import (
	"bufio"
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"regexp"
	"strings"
	"sync"
	"time"
	"unicode/utf8"
)

const maxFrameBytes = 16 * 1024 * 1024

// An engine reports its real failures on stderr — an unconfigured provider, a
// refused login, a crash — while the ACP stream carries only a generic error.
// The tail is bounded and redacted because it is quoted back into the
// transcript, where an agent's startup logging could otherwise copy a login
// code or an API key.
const (
	stderrTailLines     = 6
	stderrTailLineBytes = 700
)

var (
	// Query strings carry the OAuth codes a login flow logs.
	stderrURLQuery = regexp.MustCompile(`\?[^\s"']+`)
	// Long opaque runs are credentials. 40+ characters keeps ordinary
	// diagnoses readable: UUIDs (36) and paths (slashes are not matched) stay.
	stderrOpaqueRun = regexp.MustCompile(`[A-Za-z0-9_\-.]{40,}`)
)

// child owns exactly one subprocess and its pipes. Stdout is drained before
// Wait; cancellation closes pipes and kills the process tree to unblock both
// readers and writers, including an agent that stops reading its stdin.
type child struct {
	cmd      *exec.Cmd
	stdin    io.WriteCloser
	stdout   io.ReadCloser
	frames   chan frame
	stop     chan struct{}
	readDone chan struct{}
	writeMu  sync.Mutex
	once     sync.Once
	stderr   stderrTail
}

// stderrTail records the last stderr lines of one engine process. Writes are
// accepted for the lifetime of the process and never block the engine.
type stderrTail struct {
	mu      sync.Mutex
	lines   []string
	partial []byte
}

func (s *stderrTail) Write(data []byte) (int, error) {
	s.mu.Lock()
	defer s.mu.Unlock()
	s.partial = append(s.partial, data...)
	for {
		index := bytes.IndexByte(s.partial, '\n')
		if index < 0 {
			break
		}
		s.pushLocked(string(s.partial[:index]))
		s.partial = append(s.partial[:0], s.partial[index+1:]...)
	}
	// An engine that streams progress without newlines must not grow this
	// buffer without bound; the newest bytes of the line are the useful ones.
	if len(s.partial) > stderrTailLineBytes {
		s.partial = append(s.partial[:0], s.partial[len(s.partial)-stderrTailLineBytes:]...)
	}
	return len(data), nil
}

func (s *stderrTail) pushLocked(line string) {
	line = strings.TrimSpace(redactStderr(line))
	if line == "" {
		return
	}
	line = truncateUTF8(line, stderrTailLineBytes)
	s.lines = append(s.lines, line)
	if len(s.lines) > stderrTailLines {
		s.lines = append(s.lines[:0], s.lines[len(s.lines)-stderrTailLines:]...)
	}
}

// Summary returns the captured tail, oldest line first, or "" when the engine
// wrote nothing to stderr.
func (s *stderrTail) Summary() string {
	s.mu.Lock()
	defer s.mu.Unlock()
	return strings.Join(append([]string(nil), s.lines...), "\n")
}

func redactStderr(line string) string {
	line = stderrURLQuery.ReplaceAllString(line, "?<redacted>")
	return stderrOpaqueRun.ReplaceAllString(line, "<redacted>")
}

func truncateUTF8(text string, limit int) string {
	if len(text) <= limit {
		return text
	}
	text = text[:limit]
	for len(text) > 0 && !utf8.ValidString(text) {
		text = text[:len(text)-1]
	}
	return text
}

type frame struct {
	data []byte
	err  error
}

func startChild(binary string, args []string, cwd string, env []string) (*child, error) {
	p := &child{frames: make(chan frame, 16), stop: make(chan struct{}), readDone: make(chan struct{})}
	cmd := exec.Command(binary, args...)
	cmd.Dir = cwd
	// Engines are verbose on stderr and silent about it on the protocol; keep a
	// bounded tail so a turn failure can explain itself.
	cmd.Stderr = &p.stderr
	if env != nil {
		cmd.Env = env
	}
	cmd.WaitDelay = 2 * time.Second
	configureChild(cmd)
	stdin, err := cmd.StdinPipe()
	if err != nil {
		return nil, err
	}
	stdout, err := cmd.StdoutPipe()
	if err != nil {
		_ = stdin.Close()
		return nil, err
	}
	if err := cmd.Start(); err != nil {
		_ = stdin.Close()
		_ = stdout.Close()
		return nil, fmt.Errorf("start engine: %w", err)
	}
	p.cmd, p.stdin, p.stdout = cmd, stdin, stdout
	go func() {
		defer close(p.readDone)
		defer close(p.frames)
		scanner := bufio.NewScanner(stdout)
		scanner.Buffer(make([]byte, 64*1024), maxFrameBytes)
		for scanner.Scan() {
			data := append([]byte(nil), scanner.Bytes()...)
			select {
			case p.frames <- frame{data: data}:
			case <-p.stop:
				return
			}
		}
		err := scanner.Err()
		if err == nil {
			err = io.ErrUnexpectedEOF
		}
		select {
		case p.frames <- frame{err: fmt.Errorf("engine output ended: %w", err)}:
		case <-p.stop:
		}
	}()
	return p, nil
}

func (p *child) write(ctx context.Context, value any) error {
	data, err := json.Marshal(value)
	if err != nil {
		return err
	}
	if len(data) >= maxFrameBytes {
		return errors.New("engine request exceeds frame size limit")
	}
	done := make(chan error, 1)
	go func() {
		p.writeMu.Lock()
		defer p.writeMu.Unlock()
		_, err := p.stdin.Write(append(data, '\n'))
		done <- err
	}()
	select {
	case err := <-done:
		return err
	case <-ctx.Done():
		p.close()
		<-done
		return ctx.Err()
	case <-p.stop:
		<-done
		return errors.New("engine process closed")
	}
}

// stderrSummary reports the engine's buffered stderr tail, empty when the
// engine wrote nothing. It is safe to call while the process still runs.
func (p *child) stderrSummary() string {
	if p == nil {
		return ""
	}
	return p.stderr.Summary()
}

func (p *child) close() {
	p.once.Do(func() {
		close(p.stop)
		// Wait has not reaped this PID yet, so a group kill cannot target a
		// different process which reused the original child's identity.
		killChild(p.cmd)
		_ = p.stdin.Close()
		_ = p.stdout.Close()
		<-p.readDone
		_ = p.cmd.Wait()
	})
}
