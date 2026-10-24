package providers

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"
	"time"
)

type rejectedLineageJournal struct {
	InferenceJournal
}

func (rejectedLineageJournal) PrepareOperation(InferenceOperationJournalRecord) error {
	return errors.New("journal unavailable")
}

func TestInferenceLineageDoesNotAdvanceAfterJournalBindFailure(t *testing.T) {
	ctx := WithInferenceWorkflow(context.Background(), testInferenceWorkflow(WorkflowBudgetSpec{}))
	root, err := EnsureInferenceAttemptContext(ctx, ChatRequest{}, InferenceOperationAgentRound, InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	ctx, lineage := BeginInferenceOperationLineage(WithInferenceJournal(ctx, rejectedLineageJournal{}), root.Operation.ID)
	_, err = EnsureInferenceAttemptContext(ctx, ChatRequest{}, InferenceOperationCompaction, InferenceProfileContinuationCritical)
	if err == nil || !strings.Contains(err.Error(), "journal unavailable") {
		t.Fatalf("expected journal preparation failure, got %v", err)
	}
	if got := lineage.LastOperationID(); got != root.Operation.ID {
		t.Fatalf("failed operation became lineage parent: %q", got)
	}
}

func TestInferenceLineageRetryPreservesOperationParentWithoutJournal(t *testing.T) {
	ctx := WithInferenceWorkflow(context.Background(), testInferenceWorkflow(WorkflowBudgetSpec{}))
	root, err := EnsureInferenceAttemptContext(ctx, ChatRequest{}, InferenceOperationAgentRound, InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	ctx, lineage := BeginInferenceOperationLineage(ctx, root.Operation.ID)
	req, err := EnsureInferenceAttemptContext(ctx, ChatRequest{}, InferenceOperationCompaction, InferenceProfileContinuationCritical)
	if err != nil {
		t.Fatal(err)
	}
	for i := 0; i < 2; i++ {
		req, err = BeginInferenceAttemptContext(ctx, req, InferenceOperationCompaction, InferenceProfileContinuationCritical)
		if err != nil {
			t.Fatal(err)
		}
		if req.Operation.ParentOperationID != root.Operation.ID || lineage.LastOperationID() != req.Operation.ID {
			t.Fatalf("retry changed parent or lineage: operation=%+v tail=%q", req.Operation, lineage.LastOperationID())
		}
	}
}

// failAfterFirstSubmissionJournal accepts the pre-send submission checkpoint and
// then fails every subsequent (streaming) UpsertSubmission. It does NOT implement
// InferenceProgressJournal, so streaming updates fall back to the synchronous
// UpsertSubmission path — the strictest test of "a streaming journal failure must
// degrade bookkeeping without aborting the stream."
type failAfterFirstSubmissionJournal struct {
	upserts int
}

func (j *failAfterFirstSubmissionJournal) PrepareOperation(InferenceOperationJournalRecord) error {
	return nil
}
func (j *failAfterFirstSubmissionJournal) PrepareAttempt(InferenceAttemptJournalRecord) error {
	return nil
}
func (j *failAfterFirstSubmissionJournal) UpsertSubmission(context.Context, InferenceSubmissionJournalRecord) error {
	j.upserts++
	if j.upserts == 1 {
		return nil
	}
	return errors.New("streaming submission journal failed")
}
func (j *failAfterFirstSubmissionJournal) MarkAttemptFirstEvent(string, string, string, time.Time) error {
	return nil
}
func (j *failAfterFirstSubmissionJournal) CompleteAttempt(InferenceAttemptTerminalRecord) error {
	return nil
}
func (j *failAfterFirstSubmissionJournal) PrepareRecoveryAttempt(context.Context, InferenceRecoveryAttemptJournalRecord) error {
	return nil
}
func (j *failAfterFirstSubmissionJournal) CompleteOperation(InferenceOperationTerminalRecord) error {
	return nil
}
func (j *failAfterFirstSubmissionJournal) CompleteWorkflow(InferenceWorkflowTerminalRecord) error {
	return nil
}

func TestStreamingJournalFailureDegradesButDoesNotAbortStream(t *testing.T) {
	execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
	journal := &failAfterFirstSubmissionJournal{}
	execution.journal = journal

	attempt := execution.BeginAttempt()
	if _, err := attempt.RecordSubmission(InferenceSubmissionMeta{Provider: "openai", Transport: "websocket"}); err != nil {
		t.Fatalf("pre-send submission checkpoint must succeed: %v", err)
	}

	// Every streaming observation's journal write fails. None may return an
	// error (forwardAttempt treats a non-nil return as fatal and kills the
	// stream) or poison the execution.
	for _, ev := range []StreamEvent{
		{Type: EventContentDelta, Content: "hel"},
		{Type: EventThinkingDelta, Content: "think"},
		{Type: EventContentDelta, Content: "lo"},
		{Type: EventDone, Usage: &TokenUsage{InputTokens: 4, OutputTokens: 2}},
	} {
		if err := attempt.ObserveStreamEvent(ev); err != nil {
			t.Fatalf("event %v aborted the stream: %v", ev.Type, err)
		}
	}

	if err := attempt.JournalError(); err != nil {
		t.Fatalf("streaming journal failure poisoned journalErr: %v", err)
	}
	if execution.JournalDegraded() == nil {
		t.Fatal("expected the streaming journal failure to be recorded as a degradation")
	}

	// In-memory accounting still completed end to end despite the failing journal.
	// OutputBytes accumulates every observed delta kind (content + thinking).
	wantBytes := len("hel") + len("think") + len("lo")
	subs := execution.Snapshot().Submissions
	if len(subs) != 1 || subs[0].Outcome != InferenceSubmissionSucceeded || subs[0].OutputBytes != wantBytes {
		t.Fatalf("submission = %+v", subs)
	}
}

type submissionCheckpointJournal struct {
	recordingInferenceJournal
	submissions      map[string]InferenceSubmissionJournalRecord
	beforeTransition func()
	upserts          int
}

func (j *submissionCheckpointJournal) UpsertSubmission(ctx context.Context, record InferenceSubmissionJournalRecord) error {
	j.upserts++
	if err := j.recordingInferenceJournal.UpsertSubmission(ctx, record); err != nil {
		return err
	}
	if j.submissions == nil {
		j.submissions = make(map[string]InferenceSubmissionJournalRecord)
	}
	j.submissions[record.ID] = record
	return nil
}

func (j *submissionCheckpointJournal) CompleteAttempt(record InferenceAttemptTerminalRecord) error {
	j.beforeTransition()
	return j.recordingInferenceJournal.CompleteAttempt(record)
}

func (j *submissionCheckpointJournal) CompleteOperation(record InferenceOperationTerminalRecord) error {
	j.beforeTransition()
	return j.recordingInferenceJournal.CompleteOperation(record)
}

func (j *submissionCheckpointJournal) PrepareRecoveryAttempt(ctx context.Context, record InferenceRecoveryAttemptJournalRecord) error {
	j.beforeTransition()
	return j.recordingInferenceJournal.PrepareRecoveryAttempt(ctx, record)
}

type droppedSubmissionProgressJournal struct {
	*submissionCheckpointJournal
}

// A dropped async update must not substitute for a required checkpoint.
func (*droppedSubmissionProgressJournal) RecordSubmissionProgress(InferenceSubmissionJournalRecord) {}

func completeSubmissionBoundary(boundary string, execution *InferenceExecution, attempt InferenceAttempt) error {
	switch boundary {
	case "attempt":
		return attempt.Complete(InferenceOutcomeFailed, NormalizedFailure{Category: FailureIncompleteStream})
	case "operation":
		return execution.Complete(InferenceOutcomeFailed, NormalizedFailure{Category: FailureIncompleteStream})
	default:
		_, err := attempt.PrepareRecoveryAttempt(context.Background(), RecoveryPlan{Action: RecoveryReplaySame}, time.Time{})
		return err
	}
}

func TestInferenceTerminalCheckpointPersistsCurrentSubmission(t *testing.T) {
	for _, boundary := range []string{"attempt", "operation", "recovery"} {
		t.Run(boundary, func(t *testing.T) {
			journal := &submissionCheckpointJournal{}
			execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
			execution.journal = &droppedSubmissionProgressJournal{journal}
			attempt := execution.BeginAttempt()
			submission, err := attempt.RecordSubmission(InferenceSubmissionMeta{Provider: "openai", Transport: "http", RequestBytes: 400})
			if err != nil {
				t.Fatal(err)
			}
			submission.ObserveOutput("partial answer")
			submission.CompleteFailure(NormalizedFailure{Category: FailureIncompleteStream})
			submission.ObserveUsage(&TokenUsage{InputTokens: 80, OutputTokens: 4})
			other, err := execution.BeginAttempt().RecordSubmission(InferenceSubmissionMeta{Transport: "http"})
			if err != nil {
				t.Fatal(err)
			}
			otherInitial := journal.submissions[other.ID]
			other.ObserveOutput("still running")
			current := execution.Snapshot().Submissions
			if journal.submissions[submission.ID].Outcome != InferenceSubmissionInFlight {
				t.Fatal("fixture persisted advisory terminal progress")
			}
			transitions := 0
			journal.beforeTransition = func() {
				transitions++
				want := journalSubmission(execution.operation.ID, current[0])
				if got := journal.submissions[submission.ID]; !reflect.DeepEqual(got, want) {
					t.Errorf("logical %s crossed an unpersisted physical terminal: got %+v, want %+v", boundary, got, want)
				}
				wantOther := otherInitial
				if boundary == "operation" {
					// Operation completion persists every current observation without
					// inventing a terminal receipt for a still-running transport.
					wantOther = journalSubmission(execution.operation.ID, current[1])
				}
				if got := journal.submissions[other.ID]; !reflect.DeepEqual(got, wantOther) {
					t.Errorf("%s persisted the wrong other-attempt snapshot: got %+v, want %+v", boundary, got, wantOther)
				}
			}
			if err := completeSubmissionBoundary(boundary, execution, attempt); err != nil {
				t.Fatal(err)
			}
			if transitions != 1 {
				t.Fatalf("logical transitions = %d, want 1", transitions)
			}
		})
	}
}

func TestInferenceTerminalCheckpointFailureStopsAdvancement(t *testing.T) {
	for _, boundary := range []string{"attempt", "operation", "recovery"} {
		for _, writeErr := range []error{errors.New("submission commit failed"), context.Canceled, context.DeadlineExceeded} {
			t.Run(boundary+"/"+writeErr.Error(), func(t *testing.T) {
				transitions := 0
				journal := &submissionCheckpointJournal{beforeTransition: func() { transitions++ }}
				execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
				execution.journal = &droppedSubmissionProgressJournal{journal}
				attempt := execution.BeginAttempt()
				submission, err := attempt.RecordSubmission(InferenceSubmissionMeta{Provider: "openai", Transport: "http"})
				if err != nil {
					t.Fatal(err)
				}
				submission.CompleteFailure(NormalizedFailure{Category: FailureIncompleteStream})
				before := execution.Snapshot()
				journal.failSubmission = writeErr
				if err := completeSubmissionBoundary(boundary, execution, attempt); err != writeErr {
					t.Errorf("required checkpoint error = %v, want original %v", err, writeErr)
				}
				if err := execution.JournalError(); err != writeErr {
					t.Errorf("JournalError = %v, want original %v", err, writeErr)
				}
				if after := execution.Snapshot(); after.Attempts != before.Attempts || !reflect.DeepEqual(after.Workflow, before.Workflow) {
					t.Errorf("failed checkpoint advanced recovery accounting: before=%+v after=%+v", before, after)
				}
				// A later healthy writer cannot retry an uncertain commit or
				// resume this operation after its required checkpoint failed.
				journal.failSubmission = nil
				if err := execution.Complete(InferenceOutcomeFailed, NormalizedFailure{}); err != writeErr {
					t.Errorf("later operation completion error = %v, want %v", err, writeErr)
				}
				if next, err := execution.BeginAttemptChecked(); err != writeErr || next.Valid() {
					t.Errorf("next attempt = %+v, error = %v", next, err)
				}
				if transitions != 0 || journal.upserts != 2 {
					t.Errorf("logical transitions=%d submission writes=%d, want 0/2", transitions, journal.upserts)
				}
			})
		}
	}
}

func TestInferenceDeltaJournalFailureDoesNotPoisonCheckpoints(t *testing.T) {
	journal := &submissionCheckpointJournal{beforeTransition: func() {}}
	first := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
	first.journal = journal
	attempt := first.BeginAttempt()
	if _, err := attempt.RecordSubmission(InferenceSubmissionMeta{Transport: "http"}); err != nil {
		t.Fatal(err)
	}
	deltaErr := errors.New("advisory delta unavailable")
	journal.failSubmission = deltaErr
	if err := attempt.ObserveStreamEvent(StreamEvent{Type: EventContentDelta, Content: "hello"}); err != nil {
		t.Fatalf("advisory failure aborted stream: %v", err)
	}
	if first.JournalDegraded() != deltaErr || first.JournalError() != nil {
		t.Fatalf("advisory error promoted to required failure: degraded=%v required=%v", first.JournalDegraded(), first.JournalError())
	}
	journal.failSubmission = nil
	second := NewInferenceExecution(NewInferenceOperation(InferenceOperationTitle, InferenceProfileBestEffort))
	second.journal = journal
	secondAttempt := second.BeginAttempt()
	if _, err := secondAttempt.RecordSubmission(InferenceSubmissionMeta{Transport: "http"}); err != nil {
		t.Fatal(err)
	}
	for _, a := range []InferenceAttempt{attempt, secondAttempt} {
		if err := a.ObserveStreamEvent(StreamEvent{Type: EventDone}); err != nil {
			t.Fatal(err)
		}
		if err := a.Complete(InferenceOutcomeSucceeded, NormalizedFailure{}); err != nil {
			t.Fatal(err)
		}
		if err := a.execution.Complete(InferenceOutcomeSucceeded, NormalizedFailure{}); err != nil {
			t.Fatalf("advisory failure poisoned operation %s: %v", a.execution.operation.ID, err)
		}
	}
}

func TestInferenceExecutionTracksAttemptsAndSubmissions(t *testing.T) {
	op := NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive)
	execution := NewInferenceExecution(op)
	first := execution.BeginAttempt()
	firstSubmission, err := first.RecordSubmission(InferenceSubmissionMeta{
		Provider: "openai", Protocol: "responses", Transport: "websocket", Mode: "stream",
	})
	if err != nil {
		t.Fatal(err)
	}
	second := execution.BeginAttempt()
	secondSubmission, err := second.RecordSubmission(InferenceSubmissionMeta{
		Provider: "openai", Protocol: "responses", Transport: "http", Mode: "fallback", Reason: "websocket_failed",
	})
	if err != nil {
		t.Fatal(err)
	}
	if first.ID == second.ID || first.Ordinal != 1 || second.Ordinal != 2 {
		t.Fatalf("attempts = %+v / %+v", first, second)
	}
	if firstSubmission.ID == secondSubmission.ID || firstSubmission.AttemptID != first.ID || secondSubmission.Ordinal != 2 {
		t.Fatalf("submissions = %+v / %+v", firstSubmission, secondSubmission)
	}
	snapshot := execution.Snapshot()
	if snapshot.Operation.ID != op.ID || snapshot.Attempts != 2 || len(snapshot.Submissions) != 2 {
		t.Fatalf("snapshot = %+v", snapshot)
	}
	if got := second.SubmissionSnapshot(); len(got) != 1 || got[0].Transport != "http" {
		t.Fatalf("second attempt submissions = %+v", got)
	}
	if _, err := first.RecordSubmission(InferenceSubmissionMeta{Transport: "http"}); err == nil {
		t.Fatal("second submission on one attempt was accepted")
	}
}

func TestEnsureInferenceAttemptPreservesOuterAttempt(t *testing.T) {
	req, err := BeginInferenceAttemptContext(context.Background(), ChatRequest{}, InferenceOperationTitle, InferenceProfileBestEffort)
	if err != nil {
		t.Fatal(err)
	}
	got, err := EnsureInferenceAttemptContext(context.Background(), req, InferenceOperationAuxiliary, InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	if got.Attempt.ID != req.Attempt.ID || got.Execution != req.Execution || got.Operation.Kind != InferenceOperationTitle {
		t.Fatalf("attempt changed across provider boundary: %+v -> %+v", req.Attempt, got.Attempt)
	}
}

func TestInferenceSubmissionTracksCostConfidenceAndOutcome(t *testing.T) {
	execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
	attempt := execution.BeginAttempt()
	submission, err := attempt.RecordSubmission(InferenceSubmissionMeta{Provider: "openai", Transport: "http"})
	if err != nil {
		t.Fatal(err)
	}
	initial := execution.Snapshot().Submissions[0]
	if initial.Outcome != InferenceSubmissionInFlight || initial.CostState != InferenceCostUnknownBillable {
		t.Fatalf("initial submission = %+v", initial)
	}

	submission.ObserveOutput("partial output")
	submission.CompleteFailure(NormalizedFailure{Category: FailureIncompleteStream})
	failed := execution.Snapshot().Submissions[0]
	if failed.Outcome != InferenceSubmissionFailed || failed.FailureCategory != FailureIncompleteStream || failed.CostState != InferenceCostUnknownBillable || failed.EstimatedUsage == nil || failed.EstimatedUsage.OutputTokens == 0 {
		t.Fatalf("failed submission = %+v", failed)
	}

	// Late provider usage is more authoritative than a local estimate even
	// after the terminal transport outcome was recorded.
	submission.ObserveUsage(&TokenUsage{InputTokens: 10, OutputTokens: 4})
	known := execution.Snapshot().Submissions[0]
	if known.CostState != InferenceCostKnown || known.ReportedUsage == nil || known.ReportedUsage.InputTokens != 10 {
		t.Fatalf("known submission = %+v", known)
	}
	if _, err := execution.BeginAttempt().RecordSubmission(InferenceSubmissionMeta{Provider: "openai", Transport: "http"}); err != nil {
		t.Fatal(err)
	}
	estimated, err := execution.BeginAttempt().RecordSubmission(InferenceSubmissionMeta{Provider: "openai", Transport: "http"})
	if err != nil {
		t.Fatal(err)
	}
	estimated.ObserveOutput("estimated output")
	estimated.ObserveEstimatedUsage(&TokenUsage{InputTokens: 8, OutputTokens: 1})
	estimated.CompleteFailure(NormalizedFailure{Category: FailureIncompleteStream})
	summary := execution.Snapshot().CostSummary()
	if summary.KnownSubmissions != 1 || summary.EstimatedSubmissions != 1 || summary.UnknownBillableSubmissions != 1 || summary.KnownUsage.InputTokens != 10 || summary.KnownUsage.OutputTokens != 4 || summary.EstimatedUsage.InputTokens != 8 {
		t.Fatalf("cost summary = %+v", summary)
	}
}

func TestInferenceAttemptAttributesStreamEventsToLatestSubmission(t *testing.T) {
	execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
	websocketAttempt := execution.BeginAttempt()
	websocket, err := websocketAttempt.RecordSubmission(InferenceSubmissionMeta{Transport: "websocket"})
	if err != nil {
		t.Fatal(err)
	}
	websocket.CompleteFallback(NormalizeFailure(errors.New("websocket unavailable")))
	httpAttempt := execution.BeginAttempt()
	if _, err := httpAttempt.RecordSubmission(InferenceSubmissionMeta{Transport: "http", Mode: "fallback"}); err != nil {
		t.Fatal(err)
	}
	httpAttempt.ObserveStreamEvent(StreamEvent{Type: EventContentDelta, Content: "hello"})
	httpAttempt.ObserveStreamEvent(StreamEvent{Type: EventDone, Usage: &TokenUsage{InputTokens: 3, OutputTokens: 1}})

	submissions := execution.Snapshot().Submissions
	if len(submissions) != 2 || submissions[0].Outcome != InferenceSubmissionFallback || submissions[1].Outcome != InferenceSubmissionSucceeded || submissions[1].CostState != InferenceCostKnown || submissions[1].OutputBytes != len("hello") {
		t.Fatalf("submissions = %+v", submissions)
	}
}

func TestUsageLessTerminalSubmissionSettlesAsEstimated(t *testing.T) {
	execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))

	// A stream that completes successfully but whose endpoint never reports
	// usage must settle as estimated via the request-size input seed, not
	// occupy the workflow's unknown-billable budget forever.
	success, err := execution.BeginAttempt().RecordSubmission(InferenceSubmissionMeta{
		Provider: "openai", Transport: "http", RequestBytes: 4000,
	})
	if err != nil {
		t.Fatal(err)
	}
	success.ObserveOutput("streamed answer text")
	success.CompleteSuccess(nil)
	settled := execution.Snapshot().Submissions[0]
	if settled.CostState != InferenceCostEstimated {
		t.Fatalf("usage-less success cost state = %v, want estimated", settled.CostState)
	}
	if settled.EstimatedUsage == nil || settled.EstimatedUsage.InputTokens != 1000 || settled.EstimatedUsage.OutputTokens == 0 {
		t.Fatalf("estimated usage = %+v, want input seed and output estimate", settled.EstimatedUsage)
	}

	// An abandoned stream with the input seed settles the same way.
	abandoned, err := execution.BeginAttempt().RecordSubmission(InferenceSubmissionMeta{
		Provider: "openai", Transport: "http", RequestBytes: 400,
	})
	if err != nil {
		t.Fatal(err)
	}
	abandoned.Abandon()
	if got := execution.Snapshot().Submissions[1].CostState; got != InferenceCostEstimated {
		t.Fatalf("abandoned cost state = %v, want estimated", got)
	}

	// Reported usage still supersedes the estimate.
	known, err := execution.BeginAttempt().RecordSubmission(InferenceSubmissionMeta{
		Provider: "openai", Transport: "http", RequestBytes: 400,
	})
	if err != nil {
		t.Fatal(err)
	}
	known.CompleteSuccess(&TokenUsage{InputTokens: 123, OutputTokens: 45})
	if got := execution.Snapshot().Submissions[2]; got.CostState != InferenceCostKnown || got.ReportedUsage.InputTokens != 123 {
		t.Fatalf("known submission = %+v", got)
	}

	summary := execution.Snapshot().CostSummary()
	if summary.UnknownBillableSubmissions != 0 || summary.EstimatedSubmissions != 2 || summary.KnownSubmissions != 1 {
		t.Fatalf("cost summary = %+v, want zero unknown-billable", summary)
	}
}
