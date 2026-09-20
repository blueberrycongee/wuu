package externalengine

import (
	"bufio"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os/exec"
	"sync"
	"time"
)

const maxFrameBytes = 16 * 1024 * 1024

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
}

type frame struct {
	data []byte
	err  error
}

func startChild(binary string, args []string, cwd string, env []string) (*child, error) {
	cmd := exec.Command(binary, args...)
	cmd.Dir, cmd.Stderr = cwd, io.Discard
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
	p := &child{cmd: cmd, stdin: stdin, stdout: stdout, frames: make(chan frame, 16), stop: make(chan struct{}), readDone: make(chan struct{})}
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
