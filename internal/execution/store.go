package execution

import (
	"errors"
	"fmt"
	"github.com/blueberrycongee/wuu/internal/stringutil"
	"strings"
	"time"
)

var (
	ErrNotFound = errors.New("execution run not found")
	ErrConflict = errors.New("execution run state conflict")
)

func normalizeRequest(request Request) Request {
	request.Mode = Mode(strings.TrimSpace(string(request.Mode)))
	request.SourceThreadID = strings.TrimSpace(request.SourceThreadID)
	request.Requested = normalizeSelection(request.Requested)
	request.AgentProfile = strings.TrimSpace(request.AgentProfile)
	return request
}

func normalizeRuntime(runtime RuntimeManifest) RuntimeManifest {
	runtime.Resolved = normalizeSelection(runtime.Resolved)
	runtime.ProtocolVersion = strings.TrimSpace(runtime.ProtocolVersion)
	runtime.CoreVersion = strings.TrimSpace(runtime.CoreVersion)
	runtime.CoreCommit = strings.TrimSpace(runtime.CoreCommit)
	return runtime
}

func normalizeSelection(selection Selection) Selection {
	selection.Provider = strings.TrimSpace(selection.Provider)
	selection.Model = strings.TrimSpace(selection.Model)
	selection.Variant = strings.TrimSpace(selection.Variant)
	selection.Effort = strings.TrimSpace(selection.Effort)
	selection.PermissionMode = strings.TrimSpace(selection.PermissionMode)
	return selection
}

func normalizeWorkspace(workspace WorkspaceRef) WorkspaceRef {
	workspace.ID = strings.TrimSpace(workspace.ID)
	workspace.Root = strings.TrimSpace(workspace.Root)
	return workspace
}

func validateManifest(params CreateParams) error {
	if params.RuntimeID == "" {
		return errors.New("execution runtime id is required")
	}
	if params.ThreadID == "" {
		return errors.New("execution thread id is required")
	}
	switch params.Request.Mode {
	case ModeStart, ModeReview:
		if params.Request.SourceThreadID != "" {
			return fmt.Errorf("%s execution request cannot have a source thread", params.Request.Mode)
		}
	case ModeResume, ModeFork:
		if params.Request.SourceThreadID == "" {
			return fmt.Errorf("%s execution request requires a source thread", params.Request.Mode)
		}
	default:
		return fmt.Errorf("invalid execution mode %q", params.Request.Mode)
	}
	if params.Workspace.Root == "" {
		return errors.New("execution workspace root is required")
	}
	if params.Runtime.Resolved.Provider == "" || params.Runtime.Resolved.Model == "" {
		return errors.New("resolved execution provider and model are required")
	}
	if params.Runtime.ProtocolVersion == "" {
		return errors.New("execution protocol version is required")
	}
	if params.Request.MaxTurns < 0 {
		return errors.New("execution max turns cannot be negative")
	}
	if params.Request.TimeoutMS < 0 {
		return errors.New("execution timeout cannot be negative")
	}
	if params.Request.ImageCount < 0 || params.Request.FileCount < 0 {
		return errors.New("execution attachment counts cannot be negative")
	}
	return nil
}

func matchesList(run Run, opts ListOptions) bool {
	return (opts.WorkspaceID == "" || run.Workspace.ID == opts.WorkspaceID) &&
		(opts.WorkspaceRoot == "" || run.Workspace.Root == opts.WorkspaceRoot) &&
		(opts.ThreadID == "" || run.ThreadID == opts.ThreadID) &&
		(opts.Status == "" || run.Status == opts.Status)
}

func stateConflict(id string, status Status, operation string) error {
	return fmt.Errorf("%w: cannot %s run %q in status %q", ErrConflict, operation, id, status)
}

func sanitizeError(runErr Error) Error {
	runErr.Code = strings.TrimSpace(runErr.Code)
	runErr.Category = strings.TrimSpace(runErr.Category)
	runErr.Provider = strings.TrimSpace(runErr.Provider)
	runErr.Message = strings.Join(strings.Fields(runErr.Message), " ")
	const maxErrorBytes = 2000
	runErr.Message = stringutil.Truncate(runErr.Message, maxErrorBytes, "")
	return runErr
}

func normalizedTime(value time.Time) time.Time {
	if value.IsZero() {
		return time.Now().UTC()
	}
	return value.UTC()
}

func toMillis(value time.Time) int64 {
	return value.UTC().UnixMilli()
}

func timeMillis(value *time.Time) int64 {
	if value == nil || value.IsZero() {
		return 0
	}
	return toMillis(*value)
}

func fromMillis(value int64) time.Time {
	return time.UnixMilli(value).UTC()
}

func optionalTime(value int64) *time.Time {
	if value == 0 {
		return nil
	}
	return timePtr(fromMillis(value))
}

func timePtr(value time.Time) *time.Time {
	copy := value.UTC()
	return &copy
}

func cloneResult(result Result) Result {
	return result
}

func cloneRun(run Run) Run {
	copy := run
	copy.Request = normalizeRequest(run.Request)
	copy.Runtime = normalizeRuntime(run.Runtime)
	copy.Workspace = normalizeWorkspace(run.Workspace)
	copy.Turns = append([]TurnRef(nil), run.Turns...)
	if run.Result != nil {
		result := cloneResult(*run.Result)
		copy.Result = &result
	}
	if run.Error != nil {
		runErr := *run.Error
		copy.Error = &runErr
	}
	if run.StartedAt != nil {
		copy.StartedAt = timePtr(*run.StartedAt)
	}
	if run.CompletedAt != nil {
		copy.CompletedAt = timePtr(*run.CompletedAt)
	}
	return copy
}
