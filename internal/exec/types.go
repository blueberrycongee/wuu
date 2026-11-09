package exec

import (
	"context"
	"errors"
	"fmt"
	"io"
	"time"

	"github.com/blueberrycongee/wuu/internal/appserver"
	"github.com/blueberrycongee/wuu/internal/execution"
)

// The exit-code contract lives in the execution package so the code persisted
// in a Run's manifest and the code this process exits with share one source.
const (
	ExitOK                 = execution.ExitOK
	ExitTurnFailed         = execution.ExitTurnFailed
	ExitInvalidInput       = execution.ExitInvalidInput
	ExitPermissionDenied   = execution.ExitPermissionDenied
	ExitTimeout            = execution.ExitTimeout
	ExitInterrupted        = execution.ExitInterrupted
	ExitProtocol           = execution.ExitProtocol
	ExitProviderModelError = execution.ExitProviderModelError
	ExitToolFailed         = execution.ExitToolFailed
	ExitConflict           = execution.ExitConflict
)

type ExitError struct {
	Code int
	Err  error
}

func (e *ExitError) Error() string {
	if e == nil {
		return ""
	}
	if e.Err == nil {
		return fmt.Sprintf("exit code %d", e.Code)
	}
	return e.Err.Error()
}

func (e *ExitError) Unwrap() error {
	if e == nil {
		return nil
	}
	return e.Err
}

func WithExitCode(code int, err error) error {
	if err == nil {
		return nil
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) {
		return err
	}
	return &ExitError{Code: code, Err: err}
}

func ExitCode(err error) int {
	if err == nil {
		return ExitOK
	}
	var exitErr *ExitError
	if errors.As(err, &exitErr) && exitErr.Code != 0 {
		return exitErr.Code
	}
	return ExitTurnFailed
}

type Options struct {
	Prompt            string
	ImagePaths        []string
	ImageOriginal     bool
	FilePaths         []string
	Attachments       Attachments
	Workdir           string
	ConfigPath        string
	AgentProfile      string
	IgnoreUserConfig  bool
	Env               []string
	MaxTurns          int
	Provider          string
	Model             string
	Effort            string
	Variant           string
	PermissionMode    string
	NoTools           bool
	JSON              bool
	Ephemeral         bool
	Timeout           time.Duration
	OutputLastMessage string
	OutputSchemaPath  string
	ResumeID          string
	ResumeLast        bool
	ForkID            string
	// Stdout is not closed by Run. On cancellation, one blocked Write may
	// outlive Run; callers must unblock it before reusing a non-concurrent writer.
	Stdout     io.Writer
	Stderr     io.Writer
	Controller Controller
}

// Controller is the app-server control surface for one exec invocation. An
// invocation is driven as a single Run; the app-server owns turn fan-out
// (structured-output retries, automatic continuations) inside that Run.
type Controller interface {
	Initialize(context.Context) (appserver.InitializeResult, error)
	StartThread(context.Context, bool) (appserver.Thread, error)
	ResumeThread(context.Context, string) (appserver.Thread, error)
	ForkThread(context.Context, string) (appserver.Thread, error)
	StartRun(context.Context, appserver.RunStartParams) (appserver.Run, error)
	InterruptRun(context.Context, string, string) (appserver.Run, error)
	Shutdown(context.Context) error
	Notifications() <-chan Notification
}

type Attachments struct {
	Images []appserver.TurnStartImage
	Files  []appserver.TurnStartFile
}

func (a Attachments) Empty() bool {
	return len(a.Images) == 0 && len(a.Files) == 0
}

type TurnInput struct {
	Prompt string
	Images []appserver.TurnStartImage
	Files  []appserver.TurnStartFile
}
