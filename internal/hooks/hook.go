package hooks

import (
	"bytes"
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"strings"
	"time"

	"github.com/blueberrycongee/wuu/internal/shellpath"
)

// Hook is the interface for executable hooks. CommandHook is the initial
// implementation; future hook types (prompt, http, agent) implement the
// same interface and can be added without changing the dispatcher.
type Hook interface {
	Type() string
	Execute(ctx context.Context, input *Input) (*Output, error)
}

// CommandHook runs a shell command, sends the hook input as JSON on stdin,
// and parses JSON from stdout. Exit code 2 signals a blocking error.
type CommandHook struct {
	Command string
	Timeout time.Duration
}

// Type returns the discriminator for this hook variant.
func (h *CommandHook) Type() string { return "command" }

// Execute runs the command with the serialized input piped to stdin.
func (h *CommandHook) Execute(ctx context.Context, input *Input) (*Output, error) {
	timeout := h.Timeout
	if timeout <= 0 {
		timeout = 30 * time.Second
	}
	runCtx, cancel := context.WithTimeout(ctx, timeout)
	defer cancel()

	shell, err := shellpath.Sh()
	if err != nil {
		return nil, fmt.Errorf("resolve hook shell: %w", err)
	}
	cmd := exec.CommandContext(runCtx, shell.Path, shell.CommandArgs(h.Command)...)
	cmd.Env = shellpath.CommandEnv(os.Environ())
	configureHookProcess(cmd)
	// Descendants can retain the pipes even after the shell exits. Bound that
	// drain separately so a canceled or finished hook cannot strand the caller.
	cmd.WaitDelay = time.Second

	inputJSON, err := json.Marshal(input)
	if err != nil {
		return nil, fmt.Errorf("marshal hook input: %w", err)
	}
	cmd.Stdin = bytes.NewReader(inputJSON)

	var stdout, stderr hookOutputBuffer
	cmd.Stdout = &stdout
	cmd.Stderr = &stderr

	runErr := cmd.Run()
	if err := runCtx.Err(); err != nil {
		return nil, fmt.Errorf("hook %q interrupted: %w", h.Command, err)
	}
	if stdout.exceeded || stderr.exceeded {
		return nil, fmt.Errorf("hook %q output exceeds %d bytes per stream", h.Command, maxHookOutputBytes)
	}

	exitCode := 0
	if runErr != nil {
		var exitErr *exec.ExitError
		if errors.As(runErr, &exitErr) {
			exitCode = exitErr.ExitCode()
		} else {
			return nil, fmt.Errorf("hook %q failed: %w", h.Command, runErr)
		}
	}

	out, parseErr := ParseOutput(stdout.buf.Bytes(), exitCode)
	if parseErr != nil {
		return nil, fmt.Errorf("parse hook output: %w", parseErr)
	}

	if out.IsBlocked() {
		reason := out.Reason
		if reason == "" {
			reason = strings.TrimSpace(stderr.buf.String())
		}
		if reason == "" {
			reason = fmt.Sprintf("hook %q blocked", h.Command)
		}
		return out, fmt.Errorf("%w: %s", ErrBlocked, reason)
	}

	// Non-zero exit code that didn't translate to a block decision is a
	// real hook failure, not a block signal.
	if exitCode != 0 && exitCode != 2 {
		return out, fmt.Errorf("hook %q failed (exit %d): %s",
			h.Command, exitCode, strings.TrimSpace(stderr.buf.String()))
	}

	return out, nil
}

const maxHookOutputBytes = 1 << 20

// Keep draining after the limit to avoid pipe deadlocks, but never interpret a
// truncated JSON response as a complete decision.
type hookOutputBuffer struct {
	buf      bytes.Buffer
	exceeded bool
}

func (b *hookOutputBuffer) Write(p []byte) (int, error) {
	n := len(p)
	remaining := maxHookOutputBytes - b.buf.Len()
	if n > remaining {
		b.exceeded = true
		p = p[:remaining]
	}
	_, _ = b.buf.Write(p)
	return n, nil
}

// IsBlocked reports whether err wraps ErrBlocked.
func IsBlocked(err error) bool {
	return errors.Is(err, ErrBlocked)
}
