package providers

import (
	"context"
	"errors"
	"testing"
)

func TestStreamRecoverySharedBudgetCountsOnlyStartedAttempts(t *testing.T) {
	workflow := testInferenceWorkflow(WorkflowBudgetSpec{MaxAttempts: LimitedBudget(2)})
	ctx := WithInferenceWorkflow(context.Background(), workflow)
	// Another operation already spent one attempt from the same workflow.
	prior, err := EnsureInferenceAttemptContext(ctx, reliableTestRequest(), InferenceOperationAuxiliary, InferenceProfileBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	if err := prior.Attempt.Complete(InferenceOutcomeSucceeded, NormalizedFailure{}); err != nil {
		t.Fatal(err)
	}
	cause := NewProviderStreamError("server_error", "Temporary upstream failure")
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{{events: []StreamEvent{{Type: EventError, Error: cause}}}}}
	ch, err := newReliableTestClient(inner, nil).StreamChat(ctx, reliableTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	events := nonLifecycleEvents(collectReliableEvents(t, ch))
	if len(events) != 1 {
		t.Fatalf("events = %+v", events)
	}
	var terminal *StreamRecoveryError
	if !errors.As(events[0].Error, &terminal) || !errors.Is(events[0].Error, cause) {
		t.Fatalf("error = %v", events[0].Error)
	}
	if got := terminal.Recovery; got.AttemptCount != 1 || got.RetryCount != 0 || got.SubmissionCount != 1 || got.StopReason != "workflow_budget_exceeded" || got.BudgetDimension != "attempts" {
		t.Fatalf("recovery = %+v", got)
	}
	if inner.callCount != 1 || workflow.SpendSnapshot().Attempts != 2 {
		t.Fatal("exceeded shared attempt budget")
	}
}

func TestStreamRecoveryReportsUnsafeReplayWithoutStartingRetry(t *testing.T) {
	cause := NewProviderStreamError("server_error", "Temporary upstream failure")
	call := ToolCall{ID: "write-1", Name: "write_file", Arguments: `{}`}
	inner := &reliableStreamMockClient{attempts: []reliableStreamAttempt{{events: []StreamEvent{
		{Type: EventToolUseEnd, ToolCall: &call}, {Type: EventError, Error: cause},
	}}}}
	guards := 0
	client := newReliableTestClient(inner, nil, WithStreamReplayGuard(func(evidence StreamRetryContext) error {
		guards++
		if len(evidence.FinalizedToolCalls) != 1 || evidence.FinalizedToolCalls[0].ID != call.ID {
			t.Errorf("guard evidence = %+v", evidence)
		}
		return errors.New("tool was durably admitted")
	}))
	ch, err := client.StreamChat(context.Background(), reliableTestRequest())
	if err != nil {
		t.Fatal(err)
	}
	events := nonLifecycleEvents(collectReliableEvents(t, ch))
	if len(events) != 2 {
		t.Fatalf("events = %+v", events)
	}
	var terminal *StreamRecoveryError
	if !errors.As(events[1].Error, &terminal) || !errors.Is(events[1].Error, cause) {
		t.Fatalf("error = %v", events[1].Error)
	}
	if got := terminal.Recovery; got.RetryCount != 0 || got.StopReason != "replay_unsafe" || got.FailureCategory != FailureReplayUnsafe {
		t.Fatalf("recovery = %+v", got)
	}
	if guards != 1 || inner.callCount != 1 {
		t.Fatalf("guards=%d attempts=%d", guards, inner.callCount)
	}
}
