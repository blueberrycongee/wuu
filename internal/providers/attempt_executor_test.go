package providers

import (
	"context"
	"errors"
	"strings"
	"sync"
	"testing"
	"time"
)

type recordingInferenceJournal struct {
	mu             sync.Mutex
	events         []string
	operation      InferenceOperationJournalRecord
	failPrepare    error
	failSubmission error
	failRecovery   error
	onRecovery     func()
}

func (j *recordingInferenceJournal) record(event string) {
	j.mu.Lock()
	j.events = append(j.events, event)
	j.mu.Unlock()
}

func (j *recordingInferenceJournal) PrepareOperation(record InferenceOperationJournalRecord) error {
	j.record("operation:prepared")
	j.operation = record
	return nil
}

func (j *recordingInferenceJournal) PrepareAttempt(InferenceAttemptJournalRecord) error {
	j.record("attempt:prepared")
	return j.failPrepare
}

func (j *recordingInferenceJournal) UpsertSubmission(_ context.Context, record InferenceSubmissionJournalRecord) error {
	j.record("submission:" + string(record.Outcome))
	return j.failSubmission
}

func (j *recordingInferenceJournal) MarkAttemptFirstEvent(string, string, string, time.Time) error {
	j.record("attempt:first_event")
	return nil
}

func (j *recordingInferenceJournal) CompleteAttempt(record InferenceAttemptTerminalRecord) error {
	j.record("attempt:" + string(record.Outcome))
	return nil
}

func (j *recordingInferenceJournal) PrepareRecoveryAttempt(ctx context.Context, record InferenceRecoveryAttemptJournalRecord) error {
	if j.onRecovery != nil {
		j.onRecovery()
	}
	if err := ctx.Err(); err != nil {
		return err
	}
	if j.failRecovery != nil {
		return j.failRecovery
	}
	j.record("recovery:" + string(record.Recovery.Action))
	j.record("attempt:prepared")
	return nil
}

func (j *recordingInferenceJournal) CompleteOperation(record InferenceOperationTerminalRecord) error {
	j.record("operation:" + string(record.Outcome))
	return nil
}
func (j *recordingInferenceJournal) CompleteWorkflow(ctx context.Context, record InferenceWorkflowTerminalRecord) error {
	j.record("complete_workflow")
	return nil
}

type journalWireClient struct {
	calls int
	sent  bool
}

type refreshThenReplayClient struct {
	calls     int
	refreshes int
}

type alwaysRetryableClient struct {
	calls int
}

type cancelAfterRefreshClient struct {
	cancel context.CancelFunc
	calls  int
}

func (c *alwaysRetryableClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	c.calls++
	return ChatResponse{}, &HTTPError{StatusCode: 503, Body: "unavailable"}
}

func (c *refreshThenReplayClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	c.calls++
	return ChatResponse{}, MarkAuthRefreshable(&HTTPError{StatusCode: 401, Body: "expired"})
}

func (c *refreshThenReplayClient) ApplyInferenceRecovery(context.Context, RecoveryPlan) error {
	c.refreshes++
	return nil
}

func (c *cancelAfterRefreshClient) Chat(context.Context, ChatRequest) (ChatResponse, error) {
	c.calls++
	return ChatResponse{}, MarkAuthRefreshable(&HTTPError{StatusCode: 401, Body: "expired"})
}

func (c *cancelAfterRefreshClient) ApplyInferenceRecovery(context.Context, RecoveryPlan) error {
	c.cancel()
	return nil
}

func (c *journalWireClient) Chat(_ context.Context, req ChatRequest) (ChatResponse, error) {
	c.calls++
	submission, err := req.Attempt.RecordSubmission(InferenceSubmissionMeta{
		Provider: "test", Protocol: "test", Transport: "memory", Mode: "unary",
	})
	if err != nil {
		return ChatResponse{}, err
	}
	c.sent = true
	usage := &TokenUsage{InputTokens: 4, OutputTokens: 2}
	submission.CompleteSuccess(usage)
	return ChatResponse{Content: "ok", Usage: usage}, nil
}

func TestExecuteChatCommitsWriteAheadLifecycle(t *testing.T) {
	journal := &recordingInferenceJournal{}
	client := &journalWireClient{}
	ctx := WithInferenceJournal(context.Background(), journal)
	resp, err := ExecuteChat(ctx, client, ChatRequest{
		Model:    "test",
		Messages: []ChatMessage{{Role: "user", Content: "private prompt must not be journaled"}},
	}, InferenceOperationMemory, InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if resp.Content != "ok" || !client.sent || client.calls != 1 {
		t.Fatalf("response=%q sent=%v calls=%d", resp.Content, client.sent, client.calls)
	}
	want := []string{
		"operation:prepared",
		"attempt:prepared",
		"submission:in_flight",
		"submission:succeeded",
		"submission:succeeded",
		"attempt:succeeded",
		"operation:succeeded",
		"complete_workflow",
	}
	if strings.Join(journal.events, "|") != strings.Join(want, "|") {
		t.Fatalf("journal events = %v, want %v", journal.events, want)
	}
	if len(journal.operation.RequestHash) != 64 || strings.Contains(journal.operation.RequestHash, "private") {
		t.Fatalf("request identity is not a digest: %q", journal.operation.RequestHash)
	}
}

func TestExecuteChatFailsClosedBeforeClientOnPrepareError(t *testing.T) {
	journal := &recordingInferenceJournal{failPrepare: errors.New("journal unavailable")}
	client := &journalWireClient{}
	_, err := ExecuteChat(WithInferenceJournal(context.Background(), journal), client, ChatRequest{
		Model: "test", Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	}, InferenceOperationAgentRound, InferenceProfileInteractive)
	if err == nil || !strings.Contains(err.Error(), "journal unavailable") {
		t.Fatalf("error = %v", err)
	}
	if client.calls != 0 || client.sent {
		t.Fatalf("client reached after prepare failure: calls=%d sent=%v", client.calls, client.sent)
	}
}

func TestExecuteChatFailsClosedAtSubmissionBoundary(t *testing.T) {
	for _, tc := range []struct {
		name       string
		journal    *recordingInferenceJournal
		wantEvents string
	}{
		{
			name:       "submission write",
			journal:    &recordingInferenceJournal{failSubmission: errors.New("submission journal failed")},
			wantEvents: "operation:prepared|attempt:prepared|submission:in_flight",
		},
	} {
		t.Run(tc.name, func(t *testing.T) {
			client := &journalWireClient{}
			_, err := ExecuteChat(WithInferenceJournal(context.Background(), tc.journal), client, ChatRequest{
				Model: "test", Messages: []ChatMessage{{Role: "user", Content: "hello"}},
			}, InferenceOperationAgentRound, InferenceProfileInteractive)
			if err == nil {
				t.Fatal("expected journal error")
			}
			if client.calls != 1 || client.sent {
				t.Fatalf("wire crossed after journal failure: calls=%d sent=%v", client.calls, client.sent)
			}
			got := strings.Join(tc.journal.events[:strings.Count(tc.wantEvents, "|")+1], "|")
			if got != tc.wantEvents {
				t.Fatalf("events prefix = %q, want %q (all=%v)", got, tc.wantEvents, tc.journal.events)
			}
		})
	}
}

func TestExecuteChatCancellationDuringRecoveryDoesNotCreateAttempt(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	journal := &recordingInferenceJournal{onRecovery: cancel}
	client := &refreshThenReplayClient{}

	_, err := ExecuteChat(WithInferenceJournal(ctx, journal), client, ChatRequest{
		Model: "test", Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	}, InferenceOperationCompaction, InferenceProfileContinuationCritical)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteChat error = %v, want context canceled", err)
	}
	if client.calls != 1 || client.refreshes != 0 {
		t.Fatalf("client calls/refreshes = %d/%d, want 1/0", client.calls, client.refreshes)
	}
	want := "operation:prepared|attempt:prepared|attempt:failed|operation:canceled|complete_workflow"
	if got := strings.Join(journal.events, "|"); got != want {
		t.Fatalf("journal events = %q, want %q", got, want)
	}
}

func TestExecuteChatCompletesOperationWhenWorkflowRecoveryBudgetIsExhausted(t *testing.T) {
	// Replay budget enforcement lives in the durable journal; when it rejects
	// the recovery, the executor must terminalize the operation instead of
	// retrying forever.
	budgetErr := &WorkflowBudgetExceededError{
		WorkflowID: "iwf-no-replay", Dimension: WorkflowBudgetSamePayloadReplays, Limit: 0, Used: 0, Requested: 1,
	}
	journal := &recordingInferenceJournal{failRecovery: budgetErr}
	workflow := newInferenceWorkflowWithIdentity("iwf-no-replay", InferenceProfileInteractive, WorkflowBudgetSpec{}, time.Now())
	ctx := WithInferenceWorkflow(WithInferenceJournal(context.Background(), journal), workflow)
	client := &alwaysRetryableClient{}

	_, err := ExecuteChat(ctx, client, ChatRequest{
		Model: "test", Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	}, InferenceOperationAgentRound, InferenceProfileInteractive)
	var exceeded *WorkflowBudgetExceededError
	if !errors.As(err, &exceeded) || exceeded.Dimension != WorkflowBudgetSamePayloadReplays {
		t.Fatalf("ExecuteChat error = %v", err)
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want 1", client.calls)
	}
	want := "operation:prepared|attempt:prepared|attempt:failed|operation:failed"
	if got := strings.Join(journal.events, "|"); got != want {
		t.Fatalf("journal events = %q, want %q", got, want)
	}
}

func TestExecuteChatHonorsFrozenOperationAttemptLimit(t *testing.T) {
	journal := &recordingInferenceJournal{}
	client := &alwaysRetryableClient{}
	op := NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive)
	op.AttemptLimit = 1

	_, err := ExecuteChat(WithInferenceJournal(context.Background(), journal), client, ChatRequest{
		Model: "test", Messages: []ChatMessage{{Role: "user", Content: "hello"}}, Operation: op,
	}, InferenceOperationAgentRound, InferenceProfileInteractive)
	if err == nil {
		t.Fatal("ExecuteChat succeeded after retryable failure")
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want frozen single attempt", client.calls)
	}
	want := "operation:prepared|attempt:prepared|attempt:failed|operation:failed|complete_workflow"
	if got := strings.Join(journal.events, "|"); got != want {
		t.Fatalf("journal events = %q, want %q", got, want)
	}
}

func TestExecuteChatTerminalizesPreparedAttemptWhenCanceledAfterRecovery(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	journal := &recordingInferenceJournal{}
	client := &cancelAfterRefreshClient{cancel: cancel}

	_, err := ExecuteChat(WithInferenceJournal(ctx, journal), client, ChatRequest{
		Model: "test", Messages: []ChatMessage{{Role: "user", Content: "hello"}},
	}, InferenceOperationAgentRound, InferenceProfileInteractive)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("ExecuteChat error = %v, want context canceled", err)
	}
	if client.calls != 1 {
		t.Fatalf("client calls = %d, want no send from prepared recovery attempt", client.calls)
	}
	want := "operation:prepared|attempt:prepared|attempt:failed|recovery:refresh_credential|attempt:prepared|attempt:canceled|operation:canceled|complete_workflow"
	if got := strings.Join(journal.events, "|"); got != want {
		t.Fatalf("journal events = %q, want %q", got, want)
	}
}

func TestInferenceRequestHashChangesOnlyWithWirePayload(t *testing.T) {
	req := ChatRequest{Model: "test", Messages: []ChatMessage{{Role: "user", Content: "hello"}}}
	first, err := InferenceRequestHash(req)
	if err != nil {
		t.Fatal(err)
	}
	req.Operation = NewInferenceOperation(InferenceOperationTitle, InferenceProfileBestEffort)
	req.StepIndex = 9
	second, err := InferenceRequestHash(req)
	if err != nil {
		t.Fatal(err)
	}
	if first != second {
		t.Fatalf("runtime metadata changed request hash: %q != %q", first, second)
	}
	req.Messages[0].Content = "changed"
	third, err := InferenceRequestHash(req)
	if err != nil {
		t.Fatal(err)
	}
	if third == first {
		t.Fatal("wire payload mutation did not change request hash")
	}
}

func TestExecuteChatSelectedPolicyStopsRecovery(t *testing.T) {
	client := &alwaysRetryableClient{}
	calls := 0
	ctx := WithRecoveryPlanner(context.Background(), func(context.Context, StreamRecoveryInput) (RecoveryPlan, time.Duration, error) {
		calls++
		return RecoveryPlan{Action: RecoveryStop}, 0, nil
	})
	_, err := ExecuteChat(ctx, client, ChatRequest{Model: "test"}, InferenceOperationAuxiliary, InferenceProfileInteractive)
	if err == nil || client.calls != 1 || calls != 1 {
		t.Fatalf("model=%d policy=%d err=%v", client.calls, calls, err)
	}
}
