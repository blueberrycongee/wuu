package providers

import (
	"context"
	"errors"
	"reflect"
	"testing"
	"testing/synctest"
	"time"
)

func TestModelReceiptScopeRetainsAdmittedTailAfterSeal(t *testing.T) {
	ctx, scope := NewModelReceiptScope(context.Background())
	producerCtx, endProducer, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if ModelReceiptScopeFromContext(producerCtx) != scope {
		t.Fatal("producer lost the owning receipt scope")
	}
	endProducer(nil)
	select {
	case <-scope.Done():
		t.Fatal("producer completion ended the still-open logical scope")
	default:
	}
	producerCtx, endProducer, err = BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope.Seal()
	scope.Seal()
	if _, _, err := BeginModelReceipt(ctx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("sealed root admitted new work: %v", err)
	}
	// An admitted producer can hand off receipt observation after logical
	// cancellation, before releasing its own ticket.
	canceledCtx, cancel := context.WithCancel(producerCtx)
	cancel()
	tailCtx, endTail, err := BeginModelReceipt(canceledCtx)
	if err != nil {
		t.Fatal(err)
	}
	if tailCtx.Err() != context.Canceled {
		t.Fatalf("tail handoff changed cancellation: %v", tailCtx.Err())
	}
	endProducer(nil)
	endProducer(nil)
	if _, _, err := BeginModelReceipt(producerCtx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("ended producer admitted new work: %v", err)
	}
	select {
	case <-scope.Done():
		t.Fatal("parent completion skipped the outstanding receipt tail")
	default:
	}
	endTail(nil)
	if err := scope.Wait(context.Background()); err != nil {
		t.Fatal(err)
	}
	if _, _, err := BeginModelReceipt(tailCtx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("completed tail reopened the scope: %v", err)
	}
}

func TestModelReceiptScopeTimeoutDoesNotCompleteProducer(t *testing.T) {
	ctx, scope := NewModelReceiptScope(context.Background())
	_, end, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope.Seal()
	deadlineCtx, cancel := context.WithTimeout(context.Background(), 0)
	defer cancel()
	if err := scope.Wait(deadlineCtx); !errors.Is(err, context.DeadlineExceeded) {
		t.Fatalf("unfinished producer wait = %v", err)
	}
	select {
	case <-scope.Done():
		t.Fatal("wait deadline falsely completed the producer")
	default:
	}
	transportErr := errors.New("original RPC transport failed")
	end(transportErr)
	end(errors.New("duplicate completion"))
	if err := scope.Wait(context.Background()); !errors.Is(err, transportErr) {
		t.Fatalf("completion lost transport failure: %v", err)
	}
}

func TestModelReceiptWithoutScopePreservesContext(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	child, end, err := BeginModelReceipt(ctx)
	if err != nil || child != ctx || ModelReceiptScopeFromContext(child) != nil {
		t.Fatalf("unscoped call acquired a receipt owner: child=%v err=%v", child, err)
	}
	end(nil)
	end(nil)
}

func TestModelReceiptScopeAbortStopsObservationWithoutCompletingNativeProducer(t *testing.T) {
	modelCtx, cancelModel := context.WithCancel(context.Background())
	ctx, scope := NewModelReceiptScope(modelCtx)
	producerCtx, endNative, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	_, endRPC, err := BeginModelReceipt(ctx)
	if err != nil {
		t.Fatal(err)
	}
	scope.Seal()
	cancelModel()
	if err := scope.ObservationContext().Err(); err != nil {
		t.Fatalf("model cancellation stopped original-RPC observation: %v", err)
	}
	ownerErr := errors.New("owner receipt deadline expired")
	scope.Abort(ownerErr)
	scope.Abort(errors.New("later abort"))
	if cause := context.Cause(scope.ObservationContext()); cause != ownerErr {
		t.Fatalf("observation cause = %v, want original %v", cause, ownerErr)
	}
	if _, _, err := BeginModelReceipt(producerCtx); !errors.Is(err, ErrModelReceiptScopeClosed) {
		t.Fatalf("aborted observation window admitted new work: %v", err)
	}
	endRPC(context.Cause(scope.ObservationContext()))
	if err := scope.Wait(context.Background()); !errors.Is(err, ownerErr) {
		t.Fatalf("aborted wait = %v", err)
	}
	select {
	case <-scope.Done():
		t.Fatal("ending RPC observation falsely completed the native producer")
	default:
	}
	endNative(nil)
	<-scope.Done()
	if err := scope.Wait(context.Background()); !errors.Is(err, ownerErr) {
		t.Fatalf("eventual producer completion erased the incomplete observation: %v", err)
	}
}

// Advisory progress is deliberately dropped; only required checkpoints reach
// writes. The hook can model a committed write whose response was lost.
type receiptScopeJournal struct {
	recordingInferenceJournal
	writes   []InferenceSubmissionJournalRecord
	contexts []context.Context
	write    func(context.Context, InferenceSubmissionJournalRecord) error
}

func (*receiptScopeJournal) RecordSubmissionProgress(InferenceSubmissionJournalRecord) {}

func (j *receiptScopeJournal) UpsertSubmission(ctx context.Context, record InferenceSubmissionJournalRecord) error {
	j.writes = append(j.writes, record)
	j.contexts = append(j.contexts, ctx)
	if j.write != nil {
		return j.write(ctx, record)
	}
	return nil
}

func receiptScopeRequest(t *testing.T, ctx context.Context, journal *receiptScopeJournal, request ChatRequest) (ChatRequest, InferenceSubmission) {
	t.Helper()
	request.Model = "receipt-test"
	request.Messages = []ChatMessage{{Role: "user", Content: "fixture"}}
	request, err := EnsureInferenceAttemptContext(WithInferenceJournal(ctx, journal), request, InferenceOperationAgentRound, InferenceProfileInteractive)
	if err != nil {
		t.Fatal(err)
	}
	submission, err := request.Attempt.RecordSubmission(InferenceSubmissionMeta{Transport: "http", Mode: "stream"})
	if err != nil {
		t.Fatal(err)
	}
	submission.CompleteFailure(NormalizedFailure{Category: FailureCanceled})
	if err := request.Attempt.Complete(InferenceOutcomeCanceled, NormalizedFailure{Category: FailureCanceled}); err != nil {
		t.Fatal(err)
	}
	if err := request.Execution.Complete(InferenceOutcomeCanceled, NormalizedFailure{Category: FailureCanceled}); err != nil {
		t.Fatal(err)
	}
	return request, submission
}

func TestModelReceiptScopeCheckpointsEveryExecutionAfterQuiescence(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, scope := NewModelReceiptScope(context.Background())
		producerCtx, endProducer, err := BeginModelReceipt(ctx)
		if err != nil {
			t.Fatal(err)
		}
		defer endProducer(nil)
		journal := &receiptScopeJournal{}
		// An existing execution must register through Ensure's fast path.
		first, err := EnsureInferenceExecutionContext(context.Background(), ChatRequest{}, InferenceOperationAgentRound, InferenceProfileInteractive)
		if err != nil {
			t.Fatal(err)
		}
		first, firstSubmission := receiptScopeRequest(t, ctx, journal, first)
		// A new operation replaces a caller's old execution pointer.
		compact, compactSubmission := receiptScopeRequest(t, ctx, journal, ChatRequest{Execution: first.Execution, Operation: NewInferenceOperation(InferenceOperationCompaction, InferenceProfileContinuationCritical)})
		last, lastSubmission := receiptScopeRequest(t, ctx, journal, ChatRequest{})
		for _, req := range []ChatRequest{first, compact, last} {
			for _, repeatCtx := range []context.Context{ctx, producerCtx} {
				if _, err := EnsureInferenceExecutionContext(repeatCtx, req, InferenceOperationAgentRound, InferenceProfileInteractive); err != nil {
					t.Fatal(err)
				}
			}
		}
		journal.writes, journal.contexts = nil, nil
		scope.Seal()
		finishCtx, cancel := context.WithTimeout(context.Background(), time.Minute)
		defer cancel()
		result := make(chan error, 1)
		go func() { result <- scope.CheckpointSubmissions(finishCtx) }()
		synctest.Wait()
		if len(journal.writes) != 0 {
			t.Fatal("checkpoint crossed a live physical producer")
		}
		select {
		case err := <-result:
			t.Fatalf("checkpoint returned before quiescence: %v", err)
		default:
		}
		// Root Seal does not end the admitted producer's right to hand off work.
		late, lateSubmission := receiptScopeRequest(t, producerCtx, journal, ChatRequest{Operation: NewInferenceOperation(InferenceOperationCompaction, InferenceProfileContinuationCritical)})
		journal.writes, journal.contexts = nil, nil
		logicalEvents := append([]string(nil), journal.events...)
		submissions := []InferenceSubmission{firstSubmission, compactSubmission, lastSubmission, lateSubmission}
		requests := []ChatRequest{first, compact, last, late}
		for i, submission := range submissions {
			submission.ObserveUsage(&TokenUsage{InputTokens: 10 + i, OutputTokens: 2 + i})
		}
		endProducer(nil)
		synctest.Wait()
		if err := <-result; err != nil {
			t.Fatal(err)
		}
		if len(journal.writes) != len(submissions) {
			t.Fatalf("final writes=%d, want one per actual execution", len(journal.writes))
		}
		for i, got := range journal.writes {
			want := journalSubmission(requests[i].Operation.ID, requests[i].Execution.Snapshot().Submissions[0])
			if !reflect.DeepEqual(got, want) || got.ID != submissions[i].ID || got.CostState != InferenceCostKnown || got.ReportedUsage.InputTokens != 10+i || got.Outcome != InferenceSubmissionFailed {
				t.Errorf("final receipt %d lost identity, order or late usage: %+v", i, got)
			}
			if journal.contexts[i] != finishCtx {
				t.Error("checkpoint replaced the owner's context")
			}
		}
		if !reflect.DeepEqual(journal.events, logicalEvents) {
			t.Fatal("final checkpoint repeated logical transitions")
		}
	})
}

func TestModelReceiptScopeRejectsExecutionAdmissionAfterTicketEnd(t *testing.T) {
	for _, mode := range []string{"root-sealed", "producer-ended", "aborted"} {
		t.Run(mode, func(t *testing.T) {
			ctx, scope := NewModelReceiptScope(context.Background())
			registered, err := EnsureInferenceExecutionContext(ctx, ChatRequest{}, InferenceOperationAgentRound, InferenceProfileInteractive)
			if err != nil {
				t.Fatal(err)
			}
			switch mode {
			case "root-sealed":
				scope.Seal()
			case "producer-ended":
				child, end, err := BeginModelReceipt(ctx)
				if err != nil {
					t.Fatal(err)
				}
				ctx = child
				end(nil)
			case "aborted":
				scope.Abort(context.DeadlineExceeded)
			}
			defer scope.Seal()
			journal := &receiptScopeJournal{}
			for _, request := range []ChatRequest{registered, {}} {
				if _, err := EnsureInferenceAttemptContext(WithInferenceJournal(ctx, journal), request, InferenceOperationAgentRound, InferenceProfileInteractive); !errors.Is(err, ErrModelReceiptScopeClosed) {
					t.Errorf("%s admitted an execution: %v", mode, err)
				}
			}
			if len(journal.events) != 0 || len(journal.writes) != 0 {
				t.Fatal("rejected admission reached durable preparation")
			}
		})
	}
}

func TestModelReceiptScopeWaitFailureDoesNotCheckpoint(t *testing.T) {
	for _, mode := range []string{"unsealed", "producer-timeout", "aborted", "completion-error"} {
		t.Run(mode, func(t *testing.T) {
			ctx, scope := NewModelReceiptScope(context.Background())
			journal := &receiptScopeJournal{}
			receiptScopeRequest(t, ctx, journal, ChatRequest{})
			journal.writes = nil
			_, end, err := BeginModelReceipt(ctx)
			if err != nil {
				t.Fatal(err)
			}
			defer end(nil)
			defer scope.Seal()
			waitCtx := context.Background()
			var want error = context.DeadlineExceeded
			if mode != "unsealed" {
				scope.Seal()
			}
			switch mode {
			case "unsealed", "producer-timeout":
				var cancel context.CancelFunc
				waitCtx, cancel = context.WithTimeout(waitCtx, 0)
				defer cancel()
			case "aborted":
				scope.Abort(want)
			case "completion-error":
				want = errors.New("original receipt transport failed")
				end(want)
			}
			if err := scope.CheckpointSubmissions(waitCtx); !errors.Is(err, want) {
				t.Fatalf("checkpoint error=%v, want %v", err, want)
			}
			if len(journal.writes) != 0 {
				t.Fatal("failed join performed final writes")
			}
			if mode == "unsealed" {
				_, childEnd, err := BeginModelReceipt(ctx)
				if err != nil {
					t.Fatalf("checkpoint implicitly sealed its caller: %v", err)
				}
				childEnd(nil)
			}
			if mode != "completion-error" {
				select {
				case <-scope.Done():
					t.Fatal("failed wait fabricated producer completion")
				default:
				}
			}
		})
	}
}

func TestModelReceiptScopeCheckpointKeepsFailedWritesAndFinishesHealthyExecutions(t *testing.T) {
	ctx, scope := NewModelReceiptScope(context.Background())
	failed, next, healthy := &receiptScopeJournal{}, &receiptScopeJournal{}, &receiptScopeJournal{}
	first, _ := receiptScopeRequest(t, ctx, failed, ChatRequest{})
	second, _ := receiptScopeRequest(t, ctx, next, ChatRequest{})
	_, _ = receiptScopeRequest(t, ctx, healthy, ChatRequest{})
	for _, journal := range []*receiptScopeJournal{failed, next, healthy} {
		journal.writes = nil
	}
	lostResponse := errors.New("first committed write response lost")
	otherLostResponse := errors.New("second committed write response lost")
	failed.write = func(context.Context, InferenceSubmissionJournalRecord) error { return lostResponse }
	next.write = func(context.Context, InferenceSubmissionJournalRecord) error { return otherLostResponse }
	if err := first.Execution.CheckpointSubmissions(context.Background()); err != lostResponse {
		t.Fatal(err)
	}
	scope.Seal()
	for pass := 1; pass <= 2; pass++ {
		err := scope.CheckpointSubmissions(context.Background())
		if !errors.Is(err, lostResponse) || !errors.Is(err, otherLostResponse) {
			t.Fatalf("lost required failure: %v", err)
		}
		if first.Execution.JournalError() != lostResponse || second.Execution.JournalError() != otherLostResponse {
			t.Fatal("required error identity changed")
		}
		if len(failed.writes) != 1 || len(next.writes) != 1 {
			t.Fatal("final checkpoint replayed an uncertain write")
		}
		if len(healthy.writes) != pass {
			t.Fatal("failed execution prevented a healthy execution checkpoint")
		}
	}
}

func TestModelReceiptScopeCheckpointUsesOneOwnerDeadline(t *testing.T) {
	synctest.Test(t, func(t *testing.T) {
		ctx, scope := NewModelReceiptScope(context.Background())
		journal := &receiptScopeJournal{}
		first, _ := receiptScopeRequest(t, ctx, journal, ChatRequest{})
		receiptScopeRequest(t, ctx, journal, ChatRequest{})
		journal.writes = nil
		finishCtx, cancel := context.WithTimeout(context.Background(), time.Second)
		defer cancel()
		journal.write = func(got context.Context, _ InferenceSubmissionJournalRecord) error {
			if got != finishCtx {
				t.Error("checkpoint reset or replaced the owner deadline context")
			}
			<-got.Done()
			return got.Err()
		}
		scope.Seal()
		if err := scope.CheckpointSubmissions(finishCtx); !errors.Is(err, context.DeadlineExceeded) {
			t.Fatalf("owner timeout lost: %v", err)
		}
		if first.Execution.JournalError() != context.DeadlineExceeded || len(journal.writes) != 1 {
			t.Fatal("expired owner budget admitted another checkpoint or lost required failure")
		}
		if scope.ObservationContext().Err() != nil {
			t.Fatal("checkpoint took over the owner's Abort policy")
		}
	})
}

func TestModelReceiptScopeNestedExecutionOwnership(t *testing.T) {
	for _, childFails := range []bool{false, true} {
		t.Run(map[bool]string{false: "success", true: "child-error"}[childFails], func(t *testing.T) {
			parentCtx, parent := NewModelReceiptScope(context.Background())
			parentJournal, childJournal := &receiptScopeJournal{}, &receiptScopeJournal{}
			request, _ := receiptScopeRequest(t, parentCtx, parentJournal, ChatRequest{})
			borrowedCtx, endBorrowed, err := BeginModelReceipt(parentCtx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := EnsureInferenceExecutionContext(borrowedCtx, request, InferenceOperationAgentRound, InferenceProfileInteractive); err != nil {
				t.Fatal(err)
			}
			endBorrowed(nil)
			bridgeCtx, endChild, err := BeginModelReceipt(parentCtx)
			if err != nil {
				t.Fatal(err)
			}
			defer endChild(nil)
			childCtx, child := NewModelReceiptScope(bridgeCtx)
			receiptScopeRequest(t, childCtx, childJournal, ChatRequest{})
			parentJournal.writes, childJournal.writes = nil, nil
			parent.Seal()
			expired, cancel := context.WithTimeout(context.Background(), 0)
			defer cancel()
			if err := parent.CheckpointSubmissions(expired); !errors.Is(err, context.DeadlineExceeded) {
				t.Fatal("parent did not retain the independent child")
			}
			if len(parentJournal.writes)+len(childJournal.writes) != 0 {
				t.Fatal("parent checkpoint crossed unfinished child")
			}
			failure := errors.New("child final receipt failed")
			if childFails {
				childJournal.write = func(context.Context, InferenceSubmissionJournalRecord) error { return failure }
			}
			child.Seal()
			childErr := child.CheckpointSubmissions(context.Background())
			if childFails != errors.Is(childErr, failure) || len(childJournal.writes) != 1 {
				t.Fatalf("child checkpoint=%v writes=%d", childErr, len(childJournal.writes))
			}
			endChild(childErr)
			parentErr := parent.CheckpointSubmissions(context.Background())
			if childFails {
				if !errors.Is(parentErr, failure) || len(parentJournal.writes) != 0 {
					t.Fatalf("parent lost child's incomplete result: %v", parentErr)
				}
			} else if parentErr != nil || len(parentJournal.writes) != 1 {
				t.Fatalf("parent checkpoint=%v writes=%d", parentErr, len(parentJournal.writes))
			}
			if len(childJournal.writes) != 1 {
				t.Fatal("parent repeated the independent child's required write")
			}
		})
	}
}

func TestModelReceiptScopeUnboundExecutionAndNoScopeCompatibility(t *testing.T) {
	for _, ctx := range []context.Context{nil, context.Background()} {
		if _, err := EnsureInferenceExecutionContext(ctx, ChatRequest{}, InferenceOperationAuxiliary, InferenceProfileInteractive); err != nil {
			t.Fatal(err)
		}
	}
	ctx, scope := NewModelReceiptScope(context.Background())
	if _, err := EnsureInferenceExecutionContext(ctx, ChatRequest{}, InferenceOperationCompaction, InferenceProfileContinuationCritical); err != nil {
		t.Fatal(err)
	}
	scope.Seal()
	if err := scope.CheckpointSubmissions(context.Background()); err != nil {
		t.Fatalf("unbound probe required a journal: %v", err)
	}
}
