package appserver

import (
	"github.com/blueberrycongee/wuu/internal/executionauth"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"testing"
)

func TestExecutionRunGrantCannotRetargetSameSession(t *testing.T) {
	scope := executionauth.Scope{Kind: "exact-run", Consumer: "personal", SessionID: "worker", RunID: "accepted-run"}
	for _, input := range []struct {
		request           string
		optional, allowed bool
	}{{"accepted-run", false, true}, {"another-run", false, false}, {"", false, false}, {"", true, true}, {"another-run", true, false}} {
		got, err := boundExecutionRequest(scope, input.request, input.optional)
		if (err == nil) != input.allowed || err == nil && got != scope.RunID {
			t.Fatalf("request=%q optional=%v: %q %v", input.request, input.optional, got, err)
		}
	}
	for _, input := range []struct {
		turn, queue string
		allowed     bool
	}{{"", "", true}, {"bound-turn", "", true}, {"foreign-turn", "", false}, {"", "foreign-queue", false}} {
		params, err := boundExecutionCancellation(scope, pluginhost.SessionCancelParams{SessionID: "worker", TurnID: input.turn, QueueID: input.queue}, &pluginhost.SessionTurnInspection{RequestID: scope.RunID, TurnID: "bound-turn", State: pluginhost.TurnLifecycleRunning})
		if (err == nil) != input.allowed || err == nil && params.TurnID != "bound-turn" {
			t.Fatalf("cancel=%+v error=%v", params, err)
		}
	}
	if _, err := boundExecutionCancellation(scope, pluginhost.SessionCancelParams{SessionID: "worker"}, nil); err == nil {
		t.Fatal("missing receipt authorized session-wide cancellation")
	}
	if _, err := boundExecutionCancellation(scope, pluginhost.SessionCancelParams{SessionID: "worker"}, &pluginhost.SessionTurnInspection{RequestID: "foreign-run", TurnID: "foreign-turn"}); err == nil {
		t.Fatal("another run receipt authorized cancellation")
	}
}
