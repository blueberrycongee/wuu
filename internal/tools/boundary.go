package tools

import (
	"fmt"

	"github.com/blueberrycongee/wuu/internal/providers"
)

// WorkspaceBoundary selects ordinary workspace authority. Mandatory protected
// paths remain enforced by Env in every mode. This boundary answers
// exactly one question per tool call: is this action allowed to happen?
//
// The answer is derived from reachability, never from trust. There is no
// approval, no human-in-the-loop, no per-tool trust policy. A call is either
// allowed or denied with a clear, model-readable reason.
type WorkspaceBoundary struct {
	Enforce        bool
	AllowMutations bool
	Guards         []Guard
}

// StandardBoundary is the default for every agent run and project session:
// confined to reachable roots, full authority inside, no approval.
func StandardBoundary() WorkspaceBoundary {
	return WorkspaceBoundary{Enforce: true, AllowMutations: true}
}

// ReadOnlyBoundary denies every mutation while still confining reads.
func ReadOnlyBoundary() WorkspaceBoundary {
	return WorkspaceBoundary{Enforce: true, AllowMutations: false}
}

// UnconfinedBoundary lifts ordinary path confinement, never protected paths.
// It is the rare escape hatch for
// tasks that must operate outside registered workspace roots.
func UnconfinedBoundary() WorkspaceBoundary {
	return WorkspaceBoundary{Enforce: false, AllowMutations: true}
}

// Guard is the extension point for hard allow/deny checks. Guards must never
// block on user input and must never return an approval/ask outcome.
type Guard interface {
	Check(info ToolInfo, call providers.ToolCall) error
}

// Check runs the non-path part of the decision: the read-only gate and the
// guard chain. Path confinement itself is enforced by Env.ResolvePath.
func (b WorkspaceBoundary) Check(info ToolInfo, call providers.ToolCall) error {
	if !b.AllowMutations && mutates(info) {
		return boundaryError{
			ToolName:        info.Name,
			Reason:          "this agent is read-only; it may observe but not modify",
			ModelNextAction: "use read-only tools, or tell the user this agent cannot make changes",
		}
	}
	for _, guard := range b.Guards {
		if guard == nil {
			continue
		}
		if err := guard.Check(info, call); err != nil {
			return err
		}
	}
	return nil
}

func mutates(info ToolInfo) bool {
	if info.ReadOnly {
		return false
	}
	switch info.Kind {
	case ToolKindFile, ToolKindShell, ToolKindTest, ToolKindGit,
		ToolKindAgent, ToolKindProcess, ToolKindSchedule,
		ToolKindMCP, ToolKindBrowser, ToolKindPlugin:
		// ToolKindBrowser is mutating unless the specific call classifies
		// read-only (info.ReadOnly short-circuits above): a read-only session
		// must block navigate/click/type but still allow observe/screenshot.
		return true
	default:
		return false
	}
}

type boundaryError struct {
	ToolName        string
	Reason          string
	ModelNextAction string
}

func (e boundaryError) Error() string {
	return fmt.Sprintf(
		"tool %q denied by workspace boundary: error_kind=boundary_denied reason=%q model_next_action=%q",
		e.ToolName,
		e.Reason,
		e.ModelNextAction,
	)
}
