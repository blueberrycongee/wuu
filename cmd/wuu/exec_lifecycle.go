package main

import (
	"context"
	"os"
	"os/signal"
	"syscall"
	"time"

	wuuexec "github.com/blueberrycongee/wuu/internal/exec"
)

func execContext(timeout time.Duration) (context.Context, context.CancelFunc) {
	ctx, stop := signal.NotifyContext(context.Background(), os.Interrupt, syscall.SIGTERM)
	// Let writes to a disconnected stdout return EPIPE so Run can settle and
	// report ExitProtocol rather than Go terminating immediately on SIGPIPE.
	brokenPipe := make(chan os.Signal, 1)
	signal.Notify(brokenPipe, syscall.SIGPIPE)
	stopSignals := func() { signal.Stop(brokenPipe); stop() }
	if timeout <= 0 {
		return ctx, stopSignals
	}
	ctx, cancel := context.WithTimeout(ctx, timeout)
	return ctx, func() { cancel(); stopSignals() }
}

func runExecWithInput(cfg execCLIConfig, args []string, resumeID string, resumeLast bool, forkID string) error {
	// The CLI deadline includes stdin. A timeout supplied inside JSON can only
	// take effect after that input has been read and decoded.
	ctx, stop := execContext(valueOfDurationFlag(cfg.timeout))
	defer stop()
	type resolvedInput struct {
		prompt string
		input  *execInputPayload
		err    error
	}
	ready := make(chan resolvedInput, 1)
	// stdin may be an uninterruptible file descriptor. Keep at most one reader;
	// the command can exit on cancellation without waiting for the producer's EOF.
	go func() {
		prompt, input, err := resolveExecPromptAndInput(cfg, args, hasExecAttachments(cfg))
		ready <- resolvedInput{prompt, input, err}
	}()
	var resolved resolvedInput
	select {
	case <-ctx.Done():
		return execInputContextError(ctx)
	case resolved = <-ready:
	}
	if ctx.Err() != nil {
		return execInputContextError(ctx)
	}
	if resolved.err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, resolved.err)
	}
	opts, err := execOptionsFromCLI(cfg, resolved.prompt, resumeID, resumeLast, resolved.input)
	if err != nil {
		return wuuexec.WithExitCode(wuuexec.ExitInvalidInput, err)
	}
	opts.ForkID = forkID
	return wuuexec.Run(ctx, opts)
}

func execInputContextError(ctx context.Context) error {
	code := wuuexec.ExitInterrupted
	if ctx.Err() == context.DeadlineExceeded {
		code = wuuexec.ExitTimeout
	}
	return wuuexec.WithExitCode(code, ctx.Err())
}
