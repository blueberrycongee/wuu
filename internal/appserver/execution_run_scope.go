package appserver

import (
	"context"
	"errors"
	"strings"

	"github.com/blueberrycongee/wuu/internal/access"
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// Only the host-restored token can narrow a delegated session operation. The
// grant scope encoded in an arbitrary access proof is not an admission token.
func executionRunScope(ctx context.Context, pluginID, sessionID string) (executionauth.Scope, bool, error) {
	if access.DelegationFromContext(ctx) == nil {
		return executionauth.Scope{}, false, nil
	}
	scope, ok := runtime.ExecutionAuthorizationScope(ctx)
	if !ok || scope.Kind != "exact-run" || scope.RunID == "" || scope.SessionID != strings.TrimSpace(sessionID) {
		return executionauth.Scope{}, true, errors.New("delegated session operation requires its exact admitted run")
	}
	state, err := runtime.RetainProductInvocationState(ctx)
	if err != nil {
		return executionauth.Scope{}, true, err
	}
	defer state.Close()
	services, err := state.Services()
	if err != nil {
		return executionauth.Scope{}, true, err
	}
	for _, item := range services.Snapshot.ActivePlugins {
		if item.ID == strings.TrimSpace(pluginID) && item.SubjectID == scope.Consumer {
			return scope, true, nil
		}
	}
	return executionauth.Scope{}, true, errors.New("execution authorization belongs to another plugin subject")
}
func boundExecutionRequest(scope executionauth.Scope, requested string, allowMissing bool) (string, error) {
	requested = strings.TrimSpace(requested)
	if scope.Kind != "exact-run" || scope.RunID == "" || requested != scope.RunID && !(allowMissing && requested == "") {
		return "", errors.New("execution authorization does not cover this request_id")
	}
	return scope.RunID, nil
}

// Cancellation resolves one durable/live accepted receipt, never a session-wide
// stop. Queue-to-running races remain harmless because the exact ID is preserved.
func boundExecutionCancellation(scope executionauth.Scope, params pluginhost.SessionCancelParams, turn *pluginhost.SessionTurnInspection) (pluginhost.SessionCancelParams, error) {
	if turn == nil || turn.RequestID != scope.RunID {
		return params, errors.New("execution authorization has no matching accepted receipt")
	}
	if params.TurnID != "" && params.TurnID != turn.TurnID || params.QueueID != "" && params.QueueID != turn.QueueID {
		return params, errors.New("execution authorization does not cover this turn or queue")
	}
	if params.TurnID == "" && params.QueueID == "" {
		if turn.State == pluginhost.TurnLifecycleQueued && turn.QueueID != "" {
			params.QueueID = turn.QueueID
		} else {
			params.TurnID = turn.TurnID
		}
	}
	if params.TurnID == "" && params.QueueID == "" {
		return params, errors.New("delegated cancellation cannot stop a whole session")
	}
	return params, nil
}
