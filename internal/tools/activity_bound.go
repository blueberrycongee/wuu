package tools

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
)

// activitySpec identifies the lease one activity-bound tool action runs under.
// It is the small, family-neutral part of the CUA MCP wrapper and the embedded
// browser tool that both callers share verbatim.
type activitySpec struct {
	Kind     activity.Kind
	ThreadID string
	Workdir  string
	PluginID string
	Target   string
}

// activityHooks injects the parts of an activity-bound action that legitimately
// differ between callers (CUA vs browser). The lease lifecycle itself — Acquire,
// lease cancellation and guarded result publication, the target
// re-Update, the state/preview/error Update, and the ActivityRef attach — is
// owned by runActivityBoundAction and identical for every caller, which is the
// whole point of extracting it: the two families cannot drift on the discipline
// that keeps a user Stop/Takeover authoritative.
type activityHooks struct {
	// run performs the actual work under the lease and returns the tool result,
	// an optional sequence status ("policy_paused"/"control_revoked"/... — empty
	// for a single action), and an execution error.
	run func(ctx context.Context, session activity.Session, lease activity.Lease) (toolresult.Result, string, error)
	// controlState maps a completed result onto the activity control state. The
	// policy_paused → waiting_confirmation override is applied generically after
	// this hook, so callers never have to reproduce it.
	controlState func(result toolresult.Result) activity.State
	// identity extracts (process_id, window_id) to persist; nil leaves them
	// untouched (browser tabs carry no OS window identity).
	identity func(result toolresult.Result) (int, uint32)
	// interaction extracts a live-preview interaction hint; nil publishes none.
	interaction func(result toolresult.Result) *activity.Interaction
	// preview returns a file:// preview URI for the session, or "" for none.
	// nil skips preview publication.
	preview func(session activity.Session, result toolresult.Result) (string, error)
}

// runActivityBoundAction acquires (or reuses) the caller's activity lease, runs
// the action under it, and publishes the outcome. It is the shared spine of the
// CUA MCP wrapper (executeActivityBoundToolResult) and the embedded browser tool
// (executeBrowserToolResult).
//
// The critical, non-negotiable discipline lives here so neither caller can lose
// it: A revoked lease cancels the action and rejects subsequent updates while
// preserving any partial execution evidence. Acquire's ErrStopped/ErrControlRevoked
// are returned verbatim (never silently rebuilt).
func (t *Toolkit) runActivityBoundAction(ctx context.Context, spec activitySpec, hooks activityHooks) (toolresult.Result, error) {
	if t == nil || t.activityRegistry == nil {
		return toolresult.Result{}, errors.New("activity registry is unavailable")
	}
	session, lease, err := t.activityRegistry.Acquire(activity.StartOptions{
		Kind:     spec.Kind,
		ThreadID: spec.ThreadID,
		Workdir:  spec.Workdir,
		PluginID: spec.PluginID,
		Target:   spec.Target,
	})
	if err != nil {
		// ErrStopped / ErrControlRevoked flow straight back to the caller, which
		// translates them for the model. Rebuilding a stopped session behind the
		// user's back would defeat the Stop button (registry hard invariant).
		return toolresult.Result{}, err
	}
	if err := t.activityRegistry.CheckControl(spec.ThreadID, session.ID, lease.Token); err != nil {
		return toolresult.Result{}, err
	}
	// Re-target the single per-(thread,plugin) session when the caller addresses
	// a different target than the reused session holds (CUA switches apps; the
	// browser switches tabs). ClearWindowIdentity drops the stale OS-window
	// binding; browser tabs carry none, so it is a no-op for them.
	if spec.Target != "" && spec.Target != session.Target {
		updated, updateErr := t.activityRegistry.UpdateWithLease(lease, activity.UpdateOptions{
			Target:              spec.Target,
			ClearWindowIdentity: true,
		})
		if updateErr != nil {
			return toolresult.Result{}, updateErr
		}
		session = updated
	}

	actionCtx, cancel, err := t.activityRegistry.BindControl(ctx, lease)
	if err != nil {
		return toolresult.Result{}, err
	}
	defer cancel()
	result, sequenceStatus, callErr := hooks.run(actionCtx, session, lease)

	// Preserve partial evidence, but prevent chaining another action after takeover.
	if controlErr := t.activityRegistry.CheckControl(spec.ThreadID, session.ID, lease.Token); controlErr != nil {
		result.IsError = true
		result.Content = append(result.Content, toolresult.ContentPart{Type: toolresult.ContentTypeText,
			Text: "Control revoked. The action may have partially executed; do not replay it. Observe again after control is released."})
		return result, controlErr
	}

	state := activity.StateBackgroundControlled
	if hooks.controlState != nil {
		state = hooks.controlState(result)
	}
	if sequenceStatus == "policy_paused" {
		state = activity.StateWaitingConfirmation
	}
	update := activity.UpdateOptions{State: state}
	if hooks.identity != nil {
		update.ProcessID, update.WindowID = hooks.identity(result)
	}
	if callErr == nil && hooks.preview != nil {
		previewURI, previewErr := hooks.preview(session, result)
		if previewErr != nil {
			callErr = previewErr
		} else {
			update.Preview = previewURI
		}
	}
	if callErr != nil {
		update.State = activity.StateError
		update.Error = callErr.Error()
	} else {
		update.ClearError = true
		if hooks.interaction != nil {
			update.Interaction = hooks.interaction(result)
		}
	}
	if updated, updateErr := t.activityRegistry.UpdateWithLease(lease, update); updateErr == nil {
		session = updated
	} else if callErr == nil {
		callErr = fmt.Errorf("update activity: %w", updateErr)
	}
	result.Activity = &toolresult.ActivityRef{
		ID:         session.ID,
		Kind:       string(session.Kind),
		State:      string(session.State),
		ThreadID:   session.ThreadID,
		PreviewURI: session.Preview,
	}
	return result, callErr
}

// riskSequenceHooks injects the per-family parts of a risk-gated batch: how each
// step's arguments are augmented before dispatch, how a step's result feeds any
// running control accumulation, an optional per-step side effect published after
// a successful step, and how the aggregate result is shaped.
type riskSequenceHooks struct {
	// augment mutates a decoded step before dispatch (CUA inherits app /
	// foreground_policy from the envelope; the browser inherits the tab_id).
	augment func(step map[string]any)
	// observe records caller-specific accumulation from a step result (CUA
	// tracks the highest control mechanism reached). May be nil.
	observe func(stepArgs string, result toolresult.Result)
	// afterStep publishes a per-step side effect after a successful step (CUA
	// updates the live-preview interaction). It returns a terminal status
	// ("control_revoked"/"partial") plus error to abort, or ("", nil) to
	// continue. May be nil.
	afterStep func(index int, result toolresult.Result) (string, error)
	// build assembles the aggregate result for a terminal status. Required.
	build func(status string, completed []map[string]any, nextStep int, lastImage *toolresult.ContentPart) toolresult.Result
}

// runRiskSequence executes a 1-64 step batch where every step self-declares a
// risk level. It is the shared "risk stripping + policy_paused state machine"
// behind executeCUASequence and the browser sequence action: a non-safe step
// without confirmed:true pauses the whole batch (err==nil, status=policy_paused)
// so the activity flips to waiting_confirmation, and the risk/confirmed keys are
// stripped before the inner tool ever sees them.
func (t *Toolkit) runRiskSequence(ctx context.Context, tool Tool, threadID, activityID, leaseToken, label string, steps []map[string]any, hooks riskSequenceHooks) (toolresult.Result, string, error) {
	if len(steps) == 0 || len(steps) > 64 {
		return toolresult.Result{}, "failed", fmt.Errorf("%s requires 1 to 64 steps", label)
	}
	completed := make([]map[string]any, 0, len(steps))
	var lastImage *toolresult.ContentPart
	for index, source := range steps {
		if err := t.activityRegistry.CheckControl(threadID, activityID, leaseToken); err != nil {
			return hooks.build("control_revoked", completed, index, lastImage), "control_revoked", err
		}
		step := make(map[string]any, len(source)+2)
		for key, value := range source {
			step[key] = value
		}
		action, _ := step["action"].(string)
		if action == "" || action == "sequence" {
			return hooks.build("failed", completed, index, lastImage), "failed", fmt.Errorf("%s step %d has an invalid action", label, index)
		}
		risk, _ := step["risk"].(string)
		if risk != "safe" && risk != "external_side_effect" && risk != "destructive" {
			return hooks.build("failed", completed, index, lastImage), "failed", fmt.Errorf("%s step %d must declare risk", label, index)
		}
		confirmed, _ := step["confirmed"].(bool)
		if risk != "safe" && !confirmed {
			return hooks.build("policy_paused", completed, index, lastImage), "policy_paused", nil
		}
		delete(step, "risk")
		delete(step, "confirmed")
		if hooks.augment != nil {
			hooks.augment(step)
		}
		encoded, err := json.Marshal(step)
		if err != nil {
			return hooks.build("failed", completed, index, lastImage), "failed", err
		}
		stepResult, err := t.executeKnownToolResultWithRepeatPolicy(ctx, providers.ToolCall{Name: tool.Name(), Arguments: string(encoded)}, tool, label == "CUA sequence" && cuaObservationAction(string(encoded)))
		if hooks.observe != nil {
			hooks.observe(string(encoded), stepResult)
		}
		if err != nil || stepResult.IsError {
			completed = append(completed, sequenceStepEvidence(index, action, "failed", stepResult))
			if err == nil {
				err = errors.New(stepResult.TextProjection())
			}
			return hooks.build("partial", completed, index+1, lastImage), "partial", err
		}
		for i := range stepResult.Content {
			if stepResult.Content[i].Type == toolresult.ContentTypeImage {
				part := stepResult.Content[i]
				lastImage = &part
			}
		}
		completed = append(completed, sequenceStepEvidence(index, action, "completed", stepResult))
		if hooks.afterStep != nil {
			if status, aerr := hooks.afterStep(index, stepResult); status != "" {
				return hooks.build(status, completed, index+1, lastImage), status, aerr
			}
		}
	}
	return hooks.build("completed", completed, len(steps), lastImage), "completed", nil
}

// Keep receipts and observations in batches as well as individual invocations.
// A failed step may already have delivered input; its evidence is needed to recover.
func sequenceStepEvidence(index int, action, status string, result toolresult.Result) map[string]any {
	entry := map[string]any{"index": index, "action": action, "status": status, "text": result.TextProjection()}
	if json.Valid(result.StructuredContent) {
		entry["result"] = result.StructuredContent
	}
	return entry
}
