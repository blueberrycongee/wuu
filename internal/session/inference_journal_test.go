package session

import (
	"bytes"
	"context"
	"crypto/sha256"
	"database/sql"
	"encoding/hex"
	"encoding/json"
	"errors"
	"fmt"
	"io"
	"os"
	"os/exec"
	"path/filepath"
	"sort"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/providers"
)

func budgetedInferenceOperationRecord(
	op providers.InferenceOperation,
	workflowID string,
	spec providers.WorkflowBudgetSpec,
	hash string,
	at time.Time,
) providers.InferenceOperationJournalRecord {
	op.WorkflowID = workflowID
	return providers.InferenceOperationJournalRecord{
		Operation: op,
		Workflow: providers.InferenceWorkflowJournalRecord{
			ID: workflowID, Profile: op.WorkloadProfile, Budget: spec, StartedAt: at,
		},
		RequestHash: hash,
		At:          at,
	}
}

func requireWorkflowBudgetDimension(t *testing.T, err error, dimension providers.WorkflowBudgetDimension) {
	t.Helper()
	var exceeded *providers.WorkflowBudgetExceededError
	if !errors.As(err, &exceeded) || exceeded.Dimension != dimension {
		t.Fatalf("budget error = %v, want dimension %q", err, dimension)
	}
}

func TestInferenceJournalPersistsMetadataOnlyLifecycle(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-test")
		now := time.Date(2026, 7, 13, 10, 0, 0, 0, time.UTC)
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := "5f3c4c7d31302a01d05b4d8726f34f743f0e97c65c91af35dc0704fdb8e9f35d"
		attemptID := op.AttemptID(1)
		submissionID := op.ID + "-s1"

		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: op.ID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now.Add(time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}
		submission := providers.InferenceSubmissionJournalRecord{
			OperationID: op.ID, AttemptID: attemptID, ID: submissionID, Ordinal: 1, AttemptOrdinal: 1,
			Provider: "openai", Protocol: "responses", Transport: "http", Mode: "stream",
			StartedAt: now.Add(3 * time.Millisecond), Outcome: providers.InferenceSubmissionInFlight,
			CostState: providers.InferenceCostUnknownBillable,
		}
		if err := journal.UpsertSubmission(submission); err != nil {
			t.Fatal(err)
		}
		if err := journal.MarkAttemptFirstEvent(op.ID, attemptID, submissionID, now.Add(4*time.Millisecond)); err != nil {
			t.Fatal(err)
		}
		submission.Outcome = providers.InferenceSubmissionSucceeded
		submission.CostState = providers.InferenceCostKnown
		submission.ReportedUsage = &providers.TokenUsage{InputTokens: 12, OutputTokens: 4, CacheReadTokens: 3}
		submission.OutputBytes = 16
		submission.CompletedAt = now.Add(5 * time.Millisecond)
		if err := journal.UpsertSubmission(submission); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
			OperationID: op.ID, AttemptID: attemptID, Outcome: providers.InferenceOutcomeSucceeded, At: now.Add(6 * time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{
			OperationID: op.ID, Outcome: providers.InferenceOutcomeSucceeded, At: now.Add(7 * time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}

		var owner, storedHash, status, terminal string
		storedDocument0 := readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", op.ID)
		owner = string(storedDocument0.OwnerID)
		storedHash = string(storedDocument0.RequestHash)
		status = string(storedDocument0.Status)
		terminal = string(storedDocument0.TerminalOutcome)
		if owner != "thread-test" || storedHash != hash || status != "succeeded" || terminal != "succeeded" {
			t.Fatalf("operation = owner %q hash %q status %q terminal %q", owner, storedHash, status, terminal)
		}
		var phase string
		var dispatchingAt, sentAt, firstEventAt, terminalAt int64
		storedDocument1 := readSessionDocument[inferenceAttemptDocument](t, store, "inference_attempts", attemptID)
		phase = string(storedDocument1.Phase)
		dispatchingAt = int64(storedDocument1.DispatchingAt)
		sentAt = int64(storedDocument1.SentAt)
		firstEventAt = int64(storedDocument1.FirstEventAt)
		terminalAt = int64(storedDocument1.TerminalAt)
		if phase != "terminal" || dispatchingAt != 0 || sentAt == 0 || firstEventAt == 0 || terminalAt == 0 {
			t.Fatalf("attempt = phase %q times %d/%d/%d/%d", phase, dispatchingAt, sentAt, firstEventAt, terminalAt)
		}
		storedSubmission := readSessionDocument[providers.InferenceSubmissionJournalRecord](t, store, "inference_submissions", submissionID)
		if storedSubmission.ReportedUsage == nil {
			t.Fatal("reported usage missing")
		}
		outcome, costState := string(storedSubmission.Outcome), string(storedSubmission.CostState)
		input, output, cacheRead, outputBytes := storedSubmission.ReportedUsage.InputTokens, storedSubmission.ReportedUsage.OutputTokens, storedSubmission.ReportedUsage.CacheReadTokens, storedSubmission.OutputBytes
		if outcome != "succeeded" || costState != "known" || input != 12 || output != 4 || cacheRead != 3 || outputBytes != 16 {
			t.Fatalf("submission = %q %q usage=%d/%d/%d bytes=%d", outcome, costState, input, output, cacheRead, outputBytes)
		}
		if err := filepath.WalkDir(store.documents.(*sessionTestDocuments).dir, func(path string, entry os.DirEntry, err error) error {
			if err != nil {
				return err
			}
			info, err := entry.Info()
			if err != nil {
				return err
			}
			if !info.IsDir() && info.Mode().Perm()&0o077 != 0 {
				t.Errorf("journal state permissions for %s = %o, want no group/other access", path, info.Mode().Perm())
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
	})
}

func readInferenceSubmissionProgress(t *testing.T, store *Store, submissionID string) (outputBytes int, outcome, costState string) {
	t.Helper()
	record := readSessionDocument[providers.InferenceSubmissionJournalRecord](t, store, "inference_submissions", submissionID)
	return record.OutputBytes, string(record.Outcome), string(record.CostState)
}

func TestInferenceJournalCoalescesStreamingProgressAndFlushesAtBarrier(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-progress")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-progress")
		progress, ok := journal.(providers.InferenceProgressJournal)
		if !ok {
			t.Fatal("session journal must implement providers.InferenceProgressJournal")
		}

		now := time.Now().UTC()
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		attemptID := op.AttemptID(1)
		submissionID := op.ID + "-s1"
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{
			Operation: op, RequestHash: testInferenceHash("progress"), At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: op.ID, AttemptID: attemptID, Ordinal: 1, RequestHash: testInferenceHash("progress"), At: now,
		}); err != nil {
			t.Fatal(err)
		}
		base := providers.InferenceSubmissionJournalRecord{
			OperationID: op.ID, AttemptID: attemptID, ID: submissionID, Ordinal: 1, AttemptOrdinal: 1,
			Provider: "openai", Protocol: "responses", Transport: "websocket", Mode: "stream",
			StartedAt: now, Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable,
		}
		// Pre-send durable checkpoint (synchronous).
		if err := journal.UpsertSubmission(base); err != nil {
			t.Fatal(err)
		}

		// A burst of streaming estimates. Each is enqueued off the caller's
		// goroutine and coalesced by submission id, so only the latest survives.
		for _, bytes := range []int{5, 20, 40, 64} {
			rec := base
			rec.CostState = providers.InferenceCostEstimated
			rec.OutputBytes = bytes
			rec.EstimatedUsage = &providers.TokenUsage{OutputTokens: bytes / 4}
			progress.RecordSubmissionProgress(rec)
		}
		// Deterministically flush the coalesced batch (the background ticker would
		// otherwise do this within inferenceJournalProgressFlushInterval).
		runtime.flushSubmissionProgress(false)

		outputBytes, outcome, costState := readInferenceSubmissionProgress(t, store, submissionID)
		if outcome != string(providers.InferenceSubmissionInFlight) || costState != string(providers.InferenceCostEstimated) || outputBytes != 64 {
			t.Fatalf("after coalesced flush: outcome=%q cost=%q bytes=%d, want in_flight/estimated/64", outcome, costState, outputBytes)
		}

		// Terminal submission estimate is enqueued async; the CompleteAttempt barrier
		// must land it before recording the attempt terminal.
		final := base
		final.Outcome = providers.InferenceSubmissionSucceeded
		final.CostState = providers.InferenceCostKnown
		final.ReportedUsage = &providers.TokenUsage{InputTokens: 10, OutputTokens: 20}
		final.OutputBytes = 80
		final.CompletedAt = now.Add(time.Millisecond)
		progress.RecordSubmissionProgress(final)
		if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
			OperationID: op.ID, AttemptID: attemptID, Outcome: providers.InferenceOutcomeSucceeded, At: now.Add(2 * time.Millisecond),
		}); err != nil {
			t.Fatal(err)
		}

		outputBytes, outcome, costState = readInferenceSubmissionProgress(t, store, submissionID)
		if outcome != "succeeded" || costState != "known" || outputBytes != 80 {
			t.Fatalf("after terminal barrier: outcome=%q cost=%q bytes=%d, want succeeded/known/80", outcome, costState, outputBytes)
		}
		if err := runtime.pendingProgressErr(); err != nil {
			t.Fatalf("unexpected progress flush degradation: %v", err)
		}
	})
}

type capturedInferenceProgressJournal struct {
	providers.InferenceJournal
	records chan providers.InferenceSubmissionJournalRecord
}

func (j *capturedInferenceProgressJournal) RecordSubmissionProgress(record providers.InferenceSubmissionJournalRecord) {
	j.records <- record
}

func TestInferenceJournalPreservesTerminalBeforeDelayedProgress(t *testing.T) {
	for _, succeeded := range []bool{false, true} {
		name := "canceled"
		if succeeded {
			name = "succeeded"
		}
		t.Run(name, func(t *testing.T) {
			forEachSessionStore(t, func(t *testing.T, store *Store) {
				runtime, err := store.NewInferenceJournalRuntime("workspace-delayed-progress")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = runtime.Close() })
				journal := runtime.ForOwner("thread-delayed-progress")
				progress := journal.(providers.InferenceProgressJournal)
				captured := &capturedInferenceProgressJournal{
					InferenceJournal: journal,
					records:          make(chan providers.InferenceSubmissionJournalRecord, 2),
				}
				ctx := providers.WithInferenceJournal(context.Background(), captured)
				request, err := providers.EnsureInferenceAttemptContext(ctx, providers.ChatRequest{Model: "fixture"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
				if err != nil {
					t.Fatal(err)
				}
				submission, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{
					Provider: "openai", Protocol: "chat_completions", Transport: "http", Mode: "stream", RequestBytes: 16,
				})
				if err != nil {
					t.Fatal(err)
				}
				// Capture real execution snapshots. Delivery may be reordered after
				// updateSubmission releases its lock and before progress is queued.
				submission.ObserveOutput("held output")
				older := <-captured.records
				outcome := providers.InferenceOutcomeCanceled
				failure := providers.NormalizeFailure(context.Canceled)
				if succeeded {
					submission.CompleteSuccess(&providers.TokenUsage{InputTokens: 10, OutputTokens: 20})
					outcome, failure = providers.InferenceOutcomeSucceeded, providers.NormalizedFailure{}
				} else {
					submission.CompleteFailure(failure)
				}
				terminal := <-captured.records
				// Keep the periodic writer from making the terminal durable before
				// the delayed snapshot arrives. No ticker timing decides this test.
				func() {
					runtime.progressFlushMu.Lock()
					defer runtime.progressFlushMu.Unlock()
					progress.RecordSubmissionProgress(terminal)
					progress.RecordSubmissionProgress(older)
				}()
				if err := request.Attempt.Complete(outcome, failure); err != nil {
					t.Fatal(err)
				}
				if err := request.Execution.Complete(outcome, failure); err != nil {
					t.Fatal(err)
				}
				if err := runtime.pendingProgressErr(); err != nil {
					t.Fatalf("ordering case unexpectedly failed a progress write: %v", err)
				}
				receipt := readSessionDocument[providers.InferenceSubmissionJournalRecord](t, store, "inference_submissions", submission.ID)
				if receipt.Outcome != terminal.Outcome || !receipt.CompletedAt.Equal(terminal.CompletedAt) || receipt.FailureCategory != terminal.FailureCategory {
					t.Errorf("delayed progress replaced terminal receipt: got %+v, want outcome=%s completed=%s failure=%s", receipt, terminal.Outcome, terminal.CompletedAt, terminal.FailureCategory)
				}
				if receipt.CostState != terminal.CostState || receipt.OutputBytes != terminal.OutputBytes {
					t.Errorf("delayed progress regressed cost evidence: got %+v, want cost=%s bytes=%d", receipt, terminal.CostState, terminal.OutputBytes)
				}
				if succeeded {
					if receipt.ReportedUsage == nil || receipt.ReportedUsage.InputTokens != 10 || receipt.ReportedUsage.OutputTokens != 20 {
						t.Errorf("delayed progress lost reported usage: %+v", receipt.ReportedUsage)
					}
				} else if receipt.ReportedUsage != nil || receipt.EstimatedUsage == nil || *receipt.EstimatedUsage != *terminal.EstimatedUsage {
					t.Errorf("delayed progress changed canceled usage evidence: %+v", receipt)
				}
				submission.ObserveUsage(&providers.TokenUsage{InputTokens: 12, OutputTokens: 24})
				progress.RecordSubmissionProgress(<-captured.records)
				runtime.flushSubmissionProgress(false)
				receipt = readSessionDocument[providers.InferenceSubmissionJournalRecord](t, store, "inference_submissions", submission.ID)
				if receipt.Outcome != terminal.Outcome || !receipt.CompletedAt.Equal(terminal.CompletedAt) || receipt.CostState != providers.InferenceCostKnown || receipt.ReportedUsage == nil || receipt.ReportedUsage.InputTokens != 12 || receipt.ReportedUsage.OutputTokens != 24 {
					t.Errorf("late usage did not refine the terminal receipt: %+v", receipt)
				}
			})
		})
	}
}

func TestInferenceJournalPendingProgressRejectsMismatchedEvidence(t *testing.T) {
	for _, mismatch := range []string{"owner", "metadata", "terminal-outcome"} {
		t.Run(mismatch, func(t *testing.T) {
			forEachSessionStore(t, func(t *testing.T, store *Store) {
				runtime, err := store.NewInferenceJournalRuntime("workspace-progress-identity")
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = runtime.Close() })
				journal := runtime.ForOwner("thread-progress-owner")
				progress := journal.(providers.InferenceProgressJournal)
				captured := &capturedInferenceProgressJournal{InferenceJournal: journal, records: make(chan providers.InferenceSubmissionJournalRecord, 1)}
				ctx := providers.WithInferenceJournal(context.Background(), captured)
				request, err := providers.EnsureInferenceAttemptContext(ctx, providers.ChatRequest{Model: "fixture"}, providers.InferenceOperationAuxiliary, providers.InferenceProfileInteractive)
				if err != nil {
					t.Fatal(err)
				}
				submission, err := request.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "openai", Protocol: "chat_completions", Transport: "http", Mode: "stream", RequestBytes: 16})
				if err != nil {
					t.Fatal(err)
				}
				failure := providers.NormalizeFailure(context.Canceled)
				submission.CompleteFailure(failure)
				terminal := <-captured.records
				invalid, invalidProgress := terminal, progress
				switch mismatch {
				case "owner":
					invalidProgress = runtime.ForOwner("another-owner").(providers.InferenceProgressJournal)
				case "metadata":
					invalid.AttemptID += "-another-attempt"
				case "terminal-outcome":
					invalid.Outcome = providers.InferenceSubmissionSucceeded
				}
				func() {
					runtime.progressFlushMu.Lock()
					defer runtime.progressFlushMu.Unlock()
					progress.RecordSubmissionProgress(terminal)
					invalidProgress.RecordSubmissionProgress(invalid)
				}()
				if err := request.Attempt.Complete(providers.InferenceOutcomeCanceled, failure); err != nil {
					t.Fatal(err)
				}
				if err := request.Execution.Complete(providers.InferenceOutcomeCanceled, failure); err != nil {
					t.Fatal(err)
				}
				if runtime.pendingProgressErr() == nil {
					t.Error("mismatched pending evidence was not diagnosed")
				}
				receipt := readSessionDocument[providers.InferenceSubmissionJournalRecord](t, store, "inference_submissions", submission.ID)
				if receipt.Outcome != terminal.Outcome || !receipt.CompletedAt.Equal(terminal.CompletedAt) || receipt.FailureCategory != terminal.FailureCategory || receipt.AttemptID != terminal.AttemptID || receipt.CostState != terminal.CostState {
					t.Errorf("mismatched pending evidence displaced the owner's terminal receipt: %+v", receipt)
				}
			})
		})
	}
}

func TestInferenceJournalCompletesWorkflowOnlyAfterOperationsAreTerminal(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-workflow-terminal")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-terminal")
		now := time.Now().UTC()
		workflowID := "iwf-terminal"
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
			op, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash("terminal"), now,
		)); err != nil {
			t.Fatal(err)
		}
		record := providers.InferenceWorkflowTerminalRecord{
			WorkflowID: workflowID, Outcome: providers.InferenceOutcomeSucceeded, At: now.Add(time.Second),
		}
		if err := journal.CompleteWorkflow(record); err == nil {
			t.Fatal("workflow completed while an operation was active")
		}
		if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{
			OperationID: op.ID, Outcome: providers.InferenceOutcomeSucceeded, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteWorkflow(record); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteWorkflow(record); err != nil {
			t.Fatalf("idempotent workflow completion: %v", err)
		}
		conflict := record
		conflict.Outcome = providers.InferenceOutcomeFailed
		if err := journal.CompleteWorkflow(conflict); err == nil {
			t.Fatal("workflow terminal outcome changed")
		}

		var status string
		var terminalAt int64
		storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		status = string(storedDocument0.Status)
		terminalAt = int64(storedDocument0.TerminalAt)
		if status != string(providers.InferenceOutcomeSucceeded) || terminalAt == 0 {
			t.Fatalf("workflow terminal state = %q/%d", status, terminalAt)
		}
	})
}

func TestInferenceJournalSettlesLateUsageAfterWorkflowCompletion(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-late-usage")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-late-usage")
		now := time.Now().UTC()
		workflowID := "iwf-late-usage"
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := testInferenceHash("late-usage")
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(op, workflowID, providers.WorkflowBudgetSpec{}, hash, now)); err != nil {
			t.Fatal(err)
		}
		attemptID := op.AttemptID(1)
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: op.ID, WorkflowID: workflowID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		submission := providers.InferenceSubmissionJournalRecord{
			OperationID: op.ID, AttemptID: attemptID, ID: op.ID + "-s1", Ordinal: 1, AttemptOrdinal: 1,
			Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable, StartedAt: now,
		}
		if err := journal.UpsertSubmission(submission); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
			OperationID: op.ID, AttemptID: attemptID, Outcome: providers.InferenceOutcomeSucceeded, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{
			OperationID: op.ID, Outcome: providers.InferenceOutcomeSucceeded, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteWorkflow(providers.InferenceWorkflowTerminalRecord{
			WorkflowID: workflowID, Outcome: providers.InferenceOutcomeSucceeded, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		submission.Outcome = providers.InferenceSubmissionSucceeded
		submission.CostState = providers.InferenceCostKnown
		submission.ReportedUsage = &providers.TokenUsage{InputTokens: 9, OutputTokens: 3}
		submission.CompletedAt = now.Add(time.Second)
		if err := journal.UpsertSubmission(submission); err != nil {
			t.Fatalf("late usage settlement: %v", err)
		}

		var status string
		var known, unknown, tokens int
		storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		status = string(storedDocument0.Status)
		known = int(storedDocument0.KnownSubmissions)
		unknown = int(storedDocument0.UnknownBillable)
		tokens = int(storedDocument0.KnownUsageTokens)
		if status != string(providers.InferenceOutcomeSucceeded) || known != 1 || unknown != 0 || tokens != 12 {
			t.Fatalf("late workflow settlement = status=%q known=%d unknown=%d tokens=%d", status, known, unknown, tokens)
		}
	})
}

func TestInferenceJournalEnforcesSharedReplayAndSubmissionBudget(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-budget")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-budget")
		now := time.Now().UTC()
		workflowID := "iwf-shared-budget"
		spec := providers.WorkflowBudgetSpec{
			MaxSamePayloadReplays:         providers.LimitedBudget(1),
			MaxSubmissions:                providers.LimitedBudget(1),
			MaxUnknownBillableSubmissions: providers.LimitedBudget(1),
		}

		first := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		first.WorkflowID = workflowID
		firstHash := testInferenceHash("workflow-first")
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(first, workflowID, spec, firstHash, now)); err != nil {
			t.Fatal(err)
		}
		firstAttempt := first.AttemptID(1)
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: first.ID, WorkflowID: workflowID, AttemptID: firstAttempt, Ordinal: 1, RequestHash: firstHash, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
			OperationID: first.ID, AttemptID: firstAttempt, Outcome: providers.InferenceOutcomeFailed,
			Failure: providers.InferenceJournalFailure{Category: providers.FailureNetwork}, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareRecoveryAttempt(context.Background(), providers.InferenceRecoveryAttemptJournalRecord{
			Recovery: providers.InferenceRecoveryJournalRecord{
				OperationID: first.ID, AttemptID: firstAttempt, Action: providers.RecoveryReplaySame, At: now,
			},
			NextAttempt: providers.InferenceAttemptJournalRecord{
				OperationID: first.ID, WorkflowID: workflowID, AttemptID: first.AttemptID(2), Ordinal: 2, RequestHash: firstHash, At: now,
			},
		}); err != nil {
			t.Fatal(err)
		}

		second := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		second.WorkflowID = workflowID
		secondHash := testInferenceHash("workflow-second")
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(second, workflowID, spec, secondHash, now)); err != nil {
			t.Fatal(err)
		}
		secondAttempt := second.AttemptID(1)
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: second.ID, WorkflowID: workflowID, AttemptID: secondAttempt, Ordinal: 1, RequestHash: secondHash, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		err = journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: second.ID, WorkflowID: workflowID, AttemptID: second.AttemptID(2), Ordinal: 2, RequestHash: secondHash, At: now,
		})
		requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetSamePayloadReplays)

		if err := journal.UpsertSubmission(providers.InferenceSubmissionJournalRecord{
			OperationID: first.ID, AttemptID: first.AttemptID(2), ID: first.ID + "-s1", Ordinal: 1, AttemptOrdinal: 2,
			Provider: "test", Transport: "http", Outcome: providers.InferenceSubmissionInFlight,
			CostState: providers.InferenceCostUnknownBillable, StartedAt: now,
		}); err != nil {
			t.Fatal(err)
		}
		err = journal.UpsertSubmission(providers.InferenceSubmissionJournalRecord{
			OperationID: second.ID, AttemptID: secondAttempt, ID: second.ID + "-s1", Ordinal: 1, AttemptOrdinal: 1,
			Provider: "test", Transport: "http", Outcome: providers.InferenceSubmissionInFlight,
			CostState: providers.InferenceCostUnknownBillable, StartedAt: now,
		})
		requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetSubmissions)

		var operations, attempts, submissions, replays, unknown int
		storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		operations = int(storedDocument0.Used[providers.WorkflowBudgetOperations])
		attempts = int(storedDocument0.Used[providers.WorkflowBudgetAttempts])
		submissions = int(storedDocument0.Used[providers.WorkflowBudgetSubmissions])
		replays = int(storedDocument0.Used[providers.WorkflowBudgetSamePayloadReplays])
		unknown = int(storedDocument0.UnknownBillable)
		if operations != 2 || attempts != 3 || submissions != 1 || replays != 1 || unknown != 1 {
			t.Fatalf("workflow counters = %d/%d/%d/%d/%d", operations, attempts, submissions, replays, unknown)
		}
		var secondPhase string
		storedDocument1 := readSessionDocument[inferenceAttemptDocument](t, store, "inference_attempts", secondAttempt)
		secondPhase = string(storedDocument1.Phase)
		if secondPhase != "prepared" {
			t.Fatalf("budget-denied submission changed attempt phase to %q", secondPhase)
		}
	})
}

func TestInferenceJournalReplayBudgetExemptsUnansweredNetworkFailure(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-replay-exemption")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-replay-exemption")
		now := time.Now().UTC()

		// Each case gets its own workflow with a zero replay allowance, so any
		// charged replay must fail admission.
		setup := func(t *testing.T, name string, markFirstEvent bool, failure providers.InferenceJournalFailure) (providers.InferenceOperation, string, string) {
			workflowID := "iwf-" + name
			spec := providers.WorkflowBudgetSpec{MaxSamePayloadReplays: providers.LimitedBudget(0)}
			op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
			hash := testInferenceHash(name)
			if err := journal.PrepareOperation(budgetedInferenceOperationRecord(op, workflowID, spec, hash, now)); err != nil {
				t.Fatal(err)
			}
			attemptID := op.AttemptID(1)
			if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
				OperationID: op.ID, WorkflowID: workflowID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now,
			}); err != nil {
				t.Fatal(err)
			}
			if markFirstEvent {
				submissionID := op.ID + "-s1"
				if err := journal.UpsertSubmission(providers.InferenceSubmissionJournalRecord{
					OperationID: op.ID, AttemptID: attemptID, ID: submissionID, Ordinal: 1, AttemptOrdinal: 1,
					Provider: "anthropic", Protocol: "messages", Transport: "http", Mode: "stream",
					StartedAt: now, Outcome: providers.InferenceSubmissionInFlight,
					CostState: providers.InferenceCostUnknownBillable,
				}); err != nil {
					t.Fatal(err)
				}
				if err := journal.MarkAttemptFirstEvent(op.ID, attemptID, submissionID, now.Add(time.Millisecond)); err != nil {
					t.Fatal(err)
				}
			}
			if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
				OperationID: op.ID, AttemptID: attemptID, Outcome: providers.InferenceOutcomeFailed,
				Failure: failure, At: now.Add(2 * time.Millisecond),
			}); err != nil {
				t.Fatal(err)
			}
			return op, workflowID, hash
		}

		replay := func(op providers.InferenceOperation, workflowID, hash string) error {
			return journal.PrepareRecoveryAttempt(context.Background(), providers.InferenceRecoveryAttemptJournalRecord{
				Recovery: providers.InferenceRecoveryJournalRecord{
					OperationID: op.ID, AttemptID: op.AttemptID(1), Action: providers.RecoveryReplaySame, At: now.Add(3 * time.Millisecond),
				},
				NextAttempt: providers.InferenceAttemptJournalRecord{
					OperationID: op.ID, WorkflowID: workflowID, AttemptID: op.AttemptID(2), Ordinal: 2,
					RequestHash: hash, At: now.Add(3 * time.Millisecond),
				},
			})
		}

		t.Run("unanswered network failure replays for free", func(t *testing.T) {
			op, workflowID, hash := setup(t, "unanswered", false, providers.InferenceJournalFailure{
				Origin: providers.FailureOriginNetwork, Category: providers.FailureNetwork,
			})
			if err := replay(op, workflowID, hash); err != nil {
				t.Fatalf("unanswered network replay must be admitted: %v", err)
			}
			var replays int
			storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
			replays = int(storedDocument0.Used[providers.WorkflowBudgetSamePayloadReplays])
			if replays != 0 {
				t.Fatalf("unanswered network replay consumed replay budget: used_replays=%d", replays)
			}
		})

		t.Run("answered network failure consumes replay budget", func(t *testing.T) {
			op, workflowID, hash := setup(t, "answered", true, providers.InferenceJournalFailure{
				Origin: providers.FailureOriginNetwork, Category: providers.FailureNetwork,
			})
			err := replay(op, workflowID, hash)
			requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetSamePayloadReplays)
		})

		t.Run("provider-signaled failure consumes replay budget", func(t *testing.T) {
			op, workflowID, hash := setup(t, "provider-signaled", false, providers.InferenceJournalFailure{
				Origin: providers.FailureOriginProvider, Category: providers.FailureServer, HTTPStatus: 500,
			})
			err := replay(op, workflowID, hash)
			requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetSamePayloadReplays)
		})
	})
}

func TestInferenceJournalPersistsFailureMessage(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-failure-message")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-failure-message")
		now := time.Now().UTC()

		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := testInferenceHash("failure-message")
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		attemptID := op.AttemptID(1)
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: op.ID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		failure := providers.DurableInferenceFailure(providers.NormalizedFailure{
			Origin:   providers.FailureOriginNetwork,
			Category: providers.FailureNetwork,
			Cause:    errors.New(`Post "https://api.example.com/v1/messages" (token=t0psecret123): connection reset by peer`),
		})
		if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
			OperationID: op.ID, AttemptID: attemptID, Outcome: providers.InferenceOutcomeFailed, Failure: failure, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{
			OperationID: op.ID, Outcome: providers.InferenceOutcomeFailed, Failure: failure, At: now,
		}); err != nil {
			t.Fatal(err)
		}

		want := `Post "https://api.example.com/v1/messages" (token=[redacted]): connection reset by peer`
		var attemptMessage, operationMessage string
		storedDocument0 := readSessionDocument[inferenceAttemptDocument](t, store, "inference_attempts", attemptID)
		attemptMessage = string(storedDocument0.Failure.Message)
		storedDocument1 := readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", op.ID)
		operationMessage = string(storedDocument1.Failure.Message)
		if attemptMessage != want {
			t.Fatalf("attempt failure_message = %q, want %q", attemptMessage, want)
		}
		if operationMessage != want {
			t.Fatalf("operation failure_message = %q, want %q", operationMessage, want)
		}
	})
}

func TestInferenceJournalRecoveryAttemptAdmissionRollsBackAtomically(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-recovery-atomic")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-recovery-atomic")
		now := time.Now().UTC()
		workflowID := "iwf-recovery-atomic"
		spec := providers.WorkflowBudgetSpec{
			MaxSamePayloadReplays: providers.LimitedBudget(0),
			MaxTransportSwitches:  providers.LimitedBudget(1),
		}
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := testInferenceHash("recovery-atomic")
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(op, workflowID, spec, hash, now)); err != nil {
			t.Fatal(err)
		}
		firstAttempt := op.AttemptID(1)
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: op.ID, WorkflowID: workflowID, AttemptID: firstAttempt,
			Ordinal: 1, RequestHash: hash, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
			OperationID: op.ID, AttemptID: firstAttempt, Outcome: providers.InferenceOutcomeFailed, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		err = journal.PrepareRecoveryAttempt(context.Background(), providers.InferenceRecoveryAttemptJournalRecord{
			Recovery: providers.InferenceRecoveryJournalRecord{
				OperationID: op.ID, AttemptID: firstAttempt,
				Action: providers.RecoverySwitchTransport, At: now,
			},
			NextAttempt: providers.InferenceAttemptJournalRecord{
				OperationID: op.ID, WorkflowID: workflowID, AttemptID: op.AttemptID(2),
				Ordinal: 2, RequestHash: hash, At: now,
			},
		})
		requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetSamePayloadReplays)

		var attempts, usedAttempts, replays, switches int
		var recovery string
		attempts = len(readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", op.ID).AttemptIDs)
		storedDocument0 := readSessionDocument[inferenceAttemptDocument](t, store, "inference_attempts", firstAttempt)
		recovery = string(storedDocument0.RecoveryAction)
		storedDocument1 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		usedAttempts = int(storedDocument1.Used[providers.WorkflowBudgetAttempts])
		replays = int(storedDocument1.Used[providers.WorkflowBudgetSamePayloadReplays])
		switches = int(storedDocument1.Used[providers.WorkflowBudgetTransportSwitches])
		if attempts != 1 || recovery != "" || usedAttempts != 1 || replays != 0 || switches != 0 {
			t.Fatalf("rolled-back recovery = attempts=%d action=%q counters=%d/%d/%d", attempts, recovery, usedAttempts, replays, switches)
		}
	})
}

func TestInferenceJournalCostConfidenceSettlementIsIdempotent(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-cost")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-cost")
		now := time.Now().UTC()
		workflowID := "iwf-cost"
		spec := providers.WorkflowBudgetSpec{
			MaxSubmissions:                providers.LimitedBudget(2),
			MaxUnknownBillableSubmissions: providers.LimitedBudget(1),
		}

		prepare := func(label string) (providers.InferenceOperation, string) {
			op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
			op.WorkflowID = workflowID
			hash := testInferenceHash(label)
			if err := journal.PrepareOperation(budgetedInferenceOperationRecord(op, workflowID, spec, hash, now)); err != nil {
				t.Fatal(err)
			}
			attemptID := op.AttemptID(1)
			if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
				OperationID: op.ID, WorkflowID: workflowID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now,
			}); err != nil {
				t.Fatal(err)
			}
			return op, attemptID
		}
		first, firstAttempt := prepare("cost-first")
		second, secondAttempt := prepare("cost-second")
		firstSubmission := providers.InferenceSubmissionJournalRecord{
			OperationID: first.ID, AttemptID: firstAttempt, ID: first.ID + "-s1", Ordinal: 1, AttemptOrdinal: 1,
			Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable, StartedAt: now,
		}
		if err := journal.UpsertSubmission(firstSubmission); err != nil {
			t.Fatal(err)
		}
		secondSubmission := providers.InferenceSubmissionJournalRecord{
			OperationID: second.ID, AttemptID: secondAttempt, ID: second.ID + "-s1", Ordinal: 1, AttemptOrdinal: 1,
			Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable, StartedAt: now,
		}
		err = journal.UpsertSubmission(secondSubmission)
		requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetUnknownBillable)

		firstSubmission.CostState = providers.InferenceCostKnown
		firstSubmission.ReportedUsage = &providers.TokenUsage{InputTokens: 10, OutputTokens: 4}
		if err := journal.UpsertSubmission(firstSubmission); err != nil {
			t.Fatal(err)
		}
		if err := journal.UpsertSubmission(firstSubmission); err != nil {
			t.Fatalf("repeat known settlement: %v", err)
		}
		if err := journal.UpsertSubmission(secondSubmission); err != nil {
			t.Fatalf("submission after known settlement: %v", err)
		}

		var used, known, unknown, knownTokens int
		storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		used = int(storedDocument0.Used[providers.WorkflowBudgetSubmissions])
		known = int(storedDocument0.KnownSubmissions)
		unknown = int(storedDocument0.UnknownBillable)
		knownTokens = int(storedDocument0.KnownUsageTokens)
		if used != 2 || known != 1 || unknown != 1 || knownTokens != 14 {
			t.Fatalf("cost counters = used=%d known=%d unknown=%d tokens=%d", used, known, unknown, knownTokens)
		}
	})
}

func TestInferenceJournalHardUsageBudgetRequiresSettledCost(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-hard-usage")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-hard-usage")
		workflowID := "iwf-hard-usage"
		spec := providers.WorkflowBudgetSpec{
			MaxUsageTokens:                providers.LimitedBudget(100),
			MaxUnknownBillableSubmissions: providers.LimitedBudget(1),
		}
		now := time.Now().UTC()
		first := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		firstHash := testInferenceHash("hard-usage-first")
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(first, workflowID, spec, firstHash, now)); err != nil {
			t.Fatal(err)
		}
		firstAttempt := first.AttemptID(1)
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: first.ID, WorkflowID: workflowID, AttemptID: firstAttempt,
			Ordinal: 1, RequestHash: firstHash, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		submission := providers.InferenceSubmissionJournalRecord{
			OperationID: first.ID, AttemptID: firstAttempt, ID: first.ID + "-s1",
			Ordinal: 1, AttemptOrdinal: 1, Outcome: providers.InferenceSubmissionInFlight,
			CostState: providers.InferenceCostUnknownBillable, StartedAt: now,
		}
		if err := journal.UpsertSubmission(submission); err != nil {
			t.Fatal(err)
		}

		second := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		secondHash := testInferenceHash("hard-usage-second")
		err = journal.PrepareOperation(budgetedInferenceOperationRecord(second, workflowID, spec, secondHash, now))
		var indeterminate *providers.WorkflowCostIndeterminateError
		if !errors.As(err, &indeterminate) || indeterminate.UnknownBillableSubmissions != 1 {
			t.Fatalf("unknown usage admission error = %v, want one indeterminate submission", err)
		}

		submission.CostState = providers.InferenceCostKnown
		submission.ReportedUsage = &providers.TokenUsage{InputTokens: 80, OutputTokens: 40}
		if err := journal.UpsertSubmission(submission); err != nil {
			t.Fatal(err)
		}
		err = journal.PrepareOperation(budgetedInferenceOperationRecord(second, workflowID, spec, secondHash, now))
		requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetUsageTokens)

		var operations, known, unknown, tokens int
		storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		operations = int(storedDocument0.Used[providers.WorkflowBudgetOperations])
		known = int(storedDocument0.KnownSubmissions)
		unknown = int(storedDocument0.UnknownBillable)
		tokens = int(storedDocument0.KnownUsageTokens)
		if operations != 1 || known != 1 || unknown != 0 || tokens != 120 {
			t.Fatalf("hard usage counters = operations=%d known=%d unknown=%d tokens=%d", operations, known, unknown, tokens)
		}
	})
}

func TestInferenceJournalConcurrentLastSubmissionAdmission(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-concurrent-budget")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-concurrent")
		now := time.Now().UTC()
		workflowID := "iwf-concurrent"
		spec := providers.WorkflowBudgetSpec{MaxSubmissions: providers.LimitedBudget(1)}
		type candidate struct {
			op        providers.InferenceOperation
			attemptID string
		}
		candidates := make([]candidate, 2)
		for i := range candidates {
			op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
			op.WorkflowID = workflowID
			hash := testInferenceHash(string(rune('a' + i)))
			if err := journal.PrepareOperation(budgetedInferenceOperationRecord(op, workflowID, spec, hash, now)); err != nil {
				t.Fatal(err)
			}
			attemptID := op.AttemptID(1)
			if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
				OperationID: op.ID, WorkflowID: workflowID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now,
			}); err != nil {
				t.Fatal(err)
			}
			candidates[i] = candidate{op: op, attemptID: attemptID}
		}

		var wg sync.WaitGroup
		results := make(chan error, len(candidates))
		for _, candidate := range candidates {
			candidate := candidate
			wg.Add(1)
			go func() {
				defer wg.Done()
				results <- journal.UpsertSubmission(providers.InferenceSubmissionJournalRecord{
					OperationID: candidate.op.ID, AttemptID: candidate.attemptID,
					ID: candidate.op.ID + "-s1", Ordinal: 1, AttemptOrdinal: 1,
					Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable, StartedAt: now,
				})
			}()
		}
		wg.Wait()
		close(results)
		succeeded := 0
		for err := range results {
			if err == nil {
				succeeded++
				continue
			}
			requireWorkflowBudgetDimension(t, err, providers.WorkflowBudgetSubmissions)
		}
		if succeeded != 1 {
			t.Fatalf("successful submissions = %d, want 1", succeeded)
		}
	})
}

type inferenceJournalProcessCandidate struct {
	Action      string `json:"action"`
	SessDir     string `json:"sess_dir"`
	Scope       string `json:"scope"`
	RuntimeID   string `json:"runtime_id"`
	OwnerID     string `json:"owner_id"`
	WorkflowID  string `json:"workflow_id"`
	OperationID string `json:"operation_id"`
	AttemptID   string `json:"attempt_id"`
	RequestHash string `json:"request_hash"`
	Backend     string `json:"backend"`
	ResultPath  string `json:"result_path"`
}

func TestInferenceJournalCrossProcessLastBudgetAdmission(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		for _, action := range []string{"attempt", "submission"} {
			t.Run(action, func(t *testing.T) {
				dir := t.TempDir()
				scope := "workspace-cross-process-" + action
				runtime, err := store.NewInferenceJournalRuntime(scope)
				if err != nil {
					t.Fatal(err)
				}
				t.Cleanup(func() { _ = runtime.Close() })
				ownerID := "thread-cross-process"
				journal := runtime.ForOwner(ownerID)
				workflowID := "iwf-cross-process-" + action
				spec := providers.WorkflowBudgetSpec{}
				if action == "attempt" {
					spec.MaxAttempts = providers.LimitedBudget(1)
				} else {
					spec.MaxSubmissions = providers.LimitedBudget(1)
				}
				now := time.Now().UTC()
				candidates := make([]inferenceJournalProcessCandidate, 2)
				for i := range candidates {
					op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
					hash := testInferenceHash(action + string(rune('a'+i)))
					if err := journal.PrepareOperation(budgetedInferenceOperationRecord(op, workflowID, spec, hash, now)); err != nil {
						t.Fatal(err)
					}
					attemptID := op.AttemptID(1)
					if action == "submission" {
						if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
							OperationID: op.ID, WorkflowID: workflowID, AttemptID: attemptID,
							Ordinal: 1, RequestHash: hash, At: now,
						}); err != nil {
							t.Fatal(err)
						}
					}
					candidates[i] = inferenceJournalProcessCandidate{
						Action: action, SessDir: store.documents.(*sessionTestDocuments).dir, Backend: store.documents.(*sessionTestDocuments).backend, Scope: scope, RuntimeID: runtime.RuntimeID(),
						OwnerID: ownerID, WorkflowID: workflowID, OperationID: op.ID,
						AttemptID: attemptID, RequestHash: hash,
						ResultPath: filepath.Join(dir, action+"-result-"+string(rune('0'+i))),
					}
				}

				type process struct {
					cmd    *exec.Cmd
					start  io.WriteCloser
					output bytes.Buffer
				}
				processes := make([]process, len(candidates))
				ctx, cancel := context.WithTimeout(t.Context(), 30*time.Second)
				defer cancel()
				for i, candidate := range candidates {
					payload, err := json.Marshal(candidate)
					if err != nil {
						t.Fatal(err)
					}
					processes[i].cmd = exec.CommandContext(ctx, os.Args[0], "-test.run=^TestInferenceJournalCrossProcessBudgetWorker$")
					processes[i].cmd.Env = append(os.Environ(), "WUU_INFERENCE_JOURNAL_PROCESS_CANDIDATE="+string(payload))
					processes[i].cmd.Stdout = &processes[i].output
					processes[i].cmd.Stderr = &processes[i].output
					var pipeErr error
					processes[i].start, pipeErr = processes[i].cmd.StdinPipe()
					if pipeErr != nil {
						t.Fatal(pipeErr)
					}
					if err := processes[i].cmd.Start(); err != nil {
						t.Fatal(err)
					}
				}
				for i := range processes {
					if _, err := processes[i].start.Write([]byte{1}); err != nil {
						t.Fatal(err)
					}
					if err := processes[i].start.Close(); err != nil {
						t.Fatal(err)
					}
				}
				for i := range processes {
					if err := processes[i].cmd.Wait(); err != nil {
						t.Fatalf("worker %d: %v\n%s", i, err, processes[i].output.String())
					}
				}

				succeeded := 0
				wantDimension := providers.WorkflowBudgetAttempts
				if action == "submission" {
					wantDimension = providers.WorkflowBudgetSubmissions
				}
				for _, candidate := range candidates {
					result, err := os.ReadFile(candidate.ResultPath)
					if err != nil {
						t.Fatal(err)
					}
					if string(result) == "ok" {
						succeeded++
						continue
					}
					if string(result) != "budget:"+string(wantDimension) {
						t.Fatalf("worker result = %q, want budget:%s", result, wantDimension)
					}
				}
				if succeeded != 1 {
					t.Fatalf("successful %s admissions = %d, want 1", action, succeeded)
				}
			})
		}
	})
}

func TestInferenceJournalCrossProcessBudgetWorker(t *testing.T) {
	raw := os.Getenv("WUU_INFERENCE_JOURNAL_PROCESS_CANDIDATE")
	if raw == "" {
		t.Skip("subprocess helper")
	}
	var candidate inferenceJournalProcessCandidate
	if err := json.Unmarshal([]byte(raw), &candidate); err != nil {
		t.Fatal(err)
	}
	var signal [1]byte
	if _, err := io.ReadFull(os.Stdin, signal[:]); err != nil {
		t.Fatal(err)
	}
	documents, err := persistence.Open(context.Background(), candidate.SessDir, candidate.Backend)
	if err != nil {
		t.Fatal(err)
	}
	defer documents.Close()
	runtime := &InferenceJournalRuntime{
		store: NewStore(context.Background(), documents), workspaceScope: candidate.Scope, runtimeID: candidate.RuntimeID,
	}
	journal := &documentInferenceJournal{runtime: runtime, ownerID: candidate.OwnerID}
	err = nil
	switch candidate.Action {
	case "attempt":
		err = journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{
			OperationID: candidate.OperationID, WorkflowID: candidate.WorkflowID,
			AttemptID: candidate.AttemptID, Ordinal: 1, RequestHash: candidate.RequestHash,
		})
	case "submission":
		err = journal.UpsertSubmission(providers.InferenceSubmissionJournalRecord{
			OperationID: candidate.OperationID, AttemptID: candidate.AttemptID,
			ID: candidate.OperationID + "-s1", Ordinal: 1, AttemptOrdinal: 1,
			Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable,
		})
	default:
		t.Fatalf("unknown action %q", candidate.Action)
	}
	result := "ok"
	if err != nil {
		var exceeded *providers.WorkflowBudgetExceededError
		if !errors.As(err, &exceeded) {
			t.Fatalf("unexpected admission error: %v", err)
		}
		result = "budget:" + string(exceeded.Dimension)
	}
	if err := os.WriteFile(candidate.ResultPath, []byte(result), 0o600); err != nil {
		t.Fatal(err)
	}
}

func TestInferenceJournalCrashRecoveryMatrix(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		oldRuntime, err := store.NewInferenceJournalRuntime("workspace-recovery")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Date(2026, 7, 13, 11, 0, 0, 0, time.UTC)

		type fixture struct {
			name       string
			profile    providers.InferenceWorkloadProfile
			phase      string
			wantAction providers.RecoveryActionKind
			wantResult providers.InferenceTerminalOutcome
		}
		fixtures := []fixture{
			{name: "operation-prepared", profile: providers.InferenceProfileInteractive, phase: "", wantAction: providers.RecoveryRescheduleSafe, wantResult: providers.InferenceOutcomeInterrupted},
			{name: "attempt-prepared", profile: providers.InferenceProfileContinuationCritical, phase: "prepared", wantAction: providers.RecoveryRescheduleSafe, wantResult: providers.InferenceOutcomeInterrupted},
			{name: "sent", profile: providers.InferenceProfileBackgroundAgent, phase: "sent", wantAction: providers.RecoveryBlockAmbiguous, wantResult: providers.InferenceOutcomeBlocked},
			{name: "streaming", profile: providers.InferenceProfileInteractive, phase: "streaming", wantAction: providers.RecoveryBlockAmbiguous, wantResult: providers.InferenceOutcomeBlocked},
			{name: "best-effort-sent", profile: providers.InferenceProfileBestEffort, phase: "sent", wantAction: providers.RecoveryStop, wantResult: providers.InferenceOutcomeAbandoned},
		}
		ops := make(map[string]providers.InferenceOperation, len(fixtures))
		for index, fixture := range fixtures {
			op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, fixture.profile)
			if fixture.profile == providers.InferenceProfileBestEffort {
				op.Kind = providers.InferenceOperationTitle
			}
			ops[fixture.name] = op
			journal := oldRuntime.ForOwner("thread-" + fixture.name)
			hash := testInferenceHash(fixture.name)
			if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: hash, At: now.Add(time.Duration(index) * time.Second)}); err != nil {
				t.Fatalf("%s prepare operation: %v", fixture.name, err)
			}
			if fixture.phase == "" {
				continue
			}
			attemptID := op.AttemptID(1)
			if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{OperationID: op.ID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now}); err != nil {
				t.Fatalf("%s prepare attempt: %v", fixture.name, err)
			}
			if fixture.phase == "prepared" {
				continue
			}
			submissionID := op.ID + "-s1"
			if err := journal.UpsertSubmission(providers.InferenceSubmissionJournalRecord{
				OperationID: op.ID, AttemptID: attemptID, ID: submissionID, Ordinal: 1, AttemptOrdinal: 1,
				Provider: "test", Transport: "http", StartedAt: now.Add(2 * time.Millisecond),
				Outcome: providers.InferenceSubmissionInFlight, CostState: providers.InferenceCostUnknownBillable,
			}); err != nil {
				t.Fatalf("%s send: %v", fixture.name, err)
			}
			if fixture.phase == "streaming" {
				if err := journal.MarkAttemptFirstEvent(op.ID, attemptID, submissionID, now.Add(3*time.Millisecond)); err != nil {
					t.Fatalf("%s first event: %v", fixture.name, err)
				}
			}
		}

		otherRuntime, err := store.NewInferenceJournalRuntime("other-workspace")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = otherRuntime.Close() })
		otherOp := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := otherRuntime.ForOwner("other").PrepareOperation(providers.InferenceOperationJournalRecord{Operation: otherOp, RequestHash: testInferenceHash("other"), At: now}); err != nil {
			t.Fatal(err)
		}
		crashInferenceRuntimeForTest(t, oldRuntime, now.Add(-time.Hour))

		newRuntime, err := store.NewInferenceJournalRuntime("workspace-recovery")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = newRuntime.Close() })
		recovered, err := newRuntime.ReconcileOrphans(now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(recovered) != len(fixtures) {
			t.Fatalf("recoveries = %d, want %d: %+v", len(recovered), len(fixtures), recovered)
		}
		byID := make(map[string]InferenceCrashRecovery, len(recovered))
		for _, item := range recovered {
			byID[item.OperationID] = item
		}
		for _, fixture := range fixtures {
			item := byID[ops[fixture.name].ID]
			if item.Action != fixture.wantAction || item.Outcome != fixture.wantResult || item.PriorPhase != fixture.phase {
				t.Errorf("%s recovery = %+v, want action=%q outcome=%q phase=%q", fixture.name, item, fixture.wantAction, fixture.wantResult, fixture.phase)
			}
		}

		for _, fixture := range fixtures {
			var status, outcome, action string
			storedDocument0 := readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", ops[fixture.name].ID)
			status = string(storedDocument0.Status)
			outcome = string(storedDocument0.TerminalOutcome)
			action = string(storedDocument0.RecoveryAction)
			if status != string(fixture.wantResult) || outcome != string(fixture.wantResult) || action != string(fixture.wantAction) {
				t.Errorf("%s persisted = %q/%q/%q", fixture.name, status, outcome, action)
			}
			var workflowStatus string
			storedDocument1 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", "iwf-"+ops[fixture.name].ID)
			workflowStatus = string(storedDocument1.Status)
			if workflowStatus != string(fixture.wantResult) {
				t.Errorf("%s workflow status = %q, want %q", fixture.name, workflowStatus, fixture.wantResult)
			}
			if fixture.phase == "sent" || fixture.phase == "streaming" {
				var submissionOutcome, costState string
				storedDocument2 := readSessionDocument[providers.InferenceSubmissionJournalRecord](t, store, "inference_submissions", ops[fixture.name].ID+"-s1")
				submissionOutcome = string(storedDocument2.Outcome)
				costState = string(storedDocument2.CostState)
				wantSubmission := string(providers.InferenceSubmissionInterrupted)
				if fixture.profile == providers.InferenceProfileBestEffort {
					wantSubmission = string(providers.InferenceSubmissionAbandoned)
				}
				if submissionOutcome != wantSubmission || costState != string(providers.InferenceCostUnknownBillable) {
					t.Errorf("%s submission = %q/%q, want %q/unknown_but_billable", fixture.name, submissionOutcome, costState, wantSubmission)
				}
			}
		}
		var otherStatus string
		storedDocument3 := readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", otherOp.ID)
		otherStatus = string(storedDocument3.Status)
		if otherStatus != "active" {
			t.Fatalf("other workspace operation status = %q, want active", otherStatus)
		}
		again, err := newRuntime.ReconcileOrphans(now.Add(2 * time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(again) != 0 {
			t.Fatalf("second recovery should be idempotent, got %+v", again)
		}
	})
}

func TestInferenceJournalReconcilesCrashBetweenOperationAndWorkflowCompletion(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		oldRuntime, err := store.NewInferenceJournalRuntime("workspace-workflow-crash-window")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		journal := oldRuntime.ForOwner("thread-crash-window")
		workflowID := "iwf-crash-window"
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
			op, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash("crash-window"), now,
		)); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{
			OperationID: op.ID, Outcome: providers.InferenceOutcomeSucceeded, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		crashInferenceRuntimeForTest(t, oldRuntime, now.Add(-time.Hour))

		newRuntime, err := store.NewInferenceJournalRuntime("workspace-workflow-crash-window")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = newRuntime.Close() })
		recoveries, err := newRuntime.ReconcileOrphans(now.Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(recoveries) != 0 {
			t.Fatalf("operation was already terminal, got recoveries %+v", recoveries)
		}
		var status string
		storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		status = string(storedDocument0.Status)
		if status != string(providers.InferenceOutcomeInterrupted) {
			t.Fatalf("workflow status = %q, want interrupted without outer completion evidence", status)
		}
	})
}

func TestInferenceJournalReconcileDoesNotTreatRecoveredFailureAsWorkflowFailure(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		oldRuntime, err := store.NewInferenceJournalRuntime("workspace-recovered-failure-window")
		if err != nil {
			t.Fatal(err)
		}
		now := time.Now().UTC()
		journal := oldRuntime.ForOwner("thread-recovered-failure")
		workflowID := "iwf-recovered-failure"
		prepareTerminal := func(label string, kind providers.InferenceOperationKind, parent string, outcome providers.InferenceTerminalOutcome) providers.InferenceOperation {
			op := providers.NewInferenceOperation(kind, providers.InferenceProfileInteractive)
			op.WorkflowID = workflowID
			op.ParentOperationID = parent
			if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
				op, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash(label), now,
			)); err != nil {
				t.Fatal(err)
			}
			if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{
				OperationID: op.ID, Outcome: outcome, At: now,
			}); err != nil {
				t.Fatal(err)
			}
			return op
		}
		overflow := prepareTerminal("overflow", providers.InferenceOperationAgentRound, "", providers.InferenceOutcomeFailed)
		compaction := prepareTerminal("compaction", providers.InferenceOperationCompaction, overflow.ID, providers.InferenceOutcomeSucceeded)
		prepareTerminal("resumed", providers.InferenceOperationAgentRound, compaction.ID, providers.InferenceOutcomeSucceeded)
		crashInferenceRuntimeForTest(t, oldRuntime, now.Add(-time.Hour))

		newRuntime, err := store.NewInferenceJournalRuntime("workspace-recovered-failure-window")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = newRuntime.Close() })
		if _, err := newRuntime.ReconcileOrphans(now.Add(time.Hour)); err != nil {
			t.Fatal(err)
		}
		var status string
		storedDocument0 := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", workflowID)
		status = string(storedDocument0.Status)
		if status != string(providers.InferenceOutcomeInterrupted) {
			t.Fatalf("workflow status = %q, recovered historical failure must not force failed", status)
		}
	})
}

func TestInferenceJournalRejectsMetadataMutation(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-test")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-test")
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		first := providers.InferenceOperationJournalRecord{Operation: op, RequestHash: testInferenceHash("first")}
		if err := journal.PrepareOperation(first); err != nil {
			t.Fatal(err)
		}
		first.RequestHash = testInferenceHash("changed")
		if err := journal.PrepareOperation(first); err == nil {
			t.Fatal("expected changed request hash to be rejected")
		}
		otherOwner := runtime.ForOwner("other-thread")
		first.RequestHash = testInferenceHash("first")
		if err := otherOwner.PrepareOperation(first); err == nil {
			t.Fatal("expected changed owner to be rejected")
		}
	})
}

func TestInferenceJournalEnforcesOperationParentConstraints(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-parent-constraints")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread-parent-constraints")
		now := time.Now().UTC()
		workflowID := "iwf-parent-constraints"
		parent := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
			parent, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash("parent"), now,
		)); err != nil {
			t.Fatal(err)
		}

		missing := providers.NewInferenceOperation(providers.InferenceOperationCompaction, providers.InferenceProfileInteractive)
		missing.ParentOperationID = "iop-missing"
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
			missing, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash("missing-parent"), now,
		)); err == nil {
			t.Fatal("operation with missing parent was prepared")
		}

		self := providers.NewInferenceOperation(providers.InferenceOperationCompaction, providers.InferenceProfileInteractive)
		self.ParentOperationID = self.ID
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
			self, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash("self-parent"), now,
		)); err == nil {
			t.Fatal("self-parent operation was prepared")
		}

		otherWorkflowParent := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
			otherWorkflowParent, "iwf-other-parent", providers.WorkflowBudgetSpec{}, testInferenceHash("other-parent"), now,
		)); err != nil {
			t.Fatal(err)
		}
		crossWorkflow := providers.NewInferenceOperation(providers.InferenceOperationCompaction, providers.InferenceProfileInteractive)
		crossWorkflow.ParentOperationID = otherWorkflowParent.ID
		if err := journal.PrepareOperation(budgetedInferenceOperationRecord(
			crossWorkflow, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash("cross-parent"), now,
		)); err == nil {
			t.Fatal("cross-workflow parent was accepted")
		}

		child := providers.NewInferenceOperation(providers.InferenceOperationCompaction, providers.InferenceProfileInteractive)
		child.ParentOperationID = parent.ID
		childRecord := budgetedInferenceOperationRecord(
			child, workflowID, providers.WorkflowBudgetSpec{}, testInferenceHash("valid-child"), now,
		)
		if err := journal.PrepareOperation(childRecord); err != nil {
			t.Fatal(err)
		}
		childRecord.Operation.ParentOperationID = ""
		if err := journal.PrepareOperation(childRecord); err == nil {
			t.Fatal("prepared operation parent metadata was changed")
		}
	})
}

func TestInferenceJournalMigratesLegacyOperationsIntoWorkflows(t *testing.T) {
	dir := t.TempDir()
	db, err := sql.Open("sqlite", filepath.Join(dir, "sessions.sqlite3"))
	if err != nil {
		t.Fatal(err)
	}
	legacySchema := []string{
		`CREATE TABLE inference_journal_runtimes (
			id TEXT PRIMARY KEY, workspace_scope TEXT NOT NULL, pid INTEGER NOT NULL,
			started_at INTEGER NOT NULL, heartbeat_at INTEGER NOT NULL, closed_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE inference_operations (
			id TEXT PRIMARY KEY, runtime_id TEXT NOT NULL, workspace_scope TEXT NOT NULL,
			owner_id TEXT NOT NULL, kind TEXT NOT NULL, workload_profile TEXT NOT NULL,
			payload_version INTEGER NOT NULL, request_hash TEXT NOT NULL, status TEXT NOT NULL,
			terminal_outcome TEXT NOT NULL DEFAULT '', recovery_action TEXT NOT NULL DEFAULT '',
			failure_origin TEXT NOT NULL DEFAULT '', failure_category TEXT NOT NULL DEFAULT '',
			provider_family TEXT NOT NULL DEFAULT '', provider_code TEXT NOT NULL DEFAULT '',
			http_status INTEGER NOT NULL DEFAULT 0, confidence TEXT NOT NULL DEFAULT '',
			created_at INTEGER NOT NULL, updated_at INTEGER NOT NULL, terminal_at INTEGER NOT NULL DEFAULT 0
		)`,
		`CREATE TABLE inference_attempts (
			id TEXT PRIMARY KEY, operation_id TEXT NOT NULL, ordinal INTEGER NOT NULL,
			request_hash TEXT NOT NULL, phase TEXT NOT NULL, terminal_outcome TEXT NOT NULL DEFAULT '',
			recovery_action TEXT NOT NULL DEFAULT '', retry_at INTEGER NOT NULL DEFAULT 0,
			failure_origin TEXT NOT NULL DEFAULT '', failure_category TEXT NOT NULL DEFAULT '',
			provider_family TEXT NOT NULL DEFAULT '', provider_code TEXT NOT NULL DEFAULT '',
			http_status INTEGER NOT NULL DEFAULT 0, confidence TEXT NOT NULL DEFAULT '',
			prepared_at INTEGER NOT NULL, dispatching_at INTEGER NOT NULL DEFAULT 0,
			sent_at INTEGER NOT NULL DEFAULT 0, first_event_at INTEGER NOT NULL DEFAULT 0,
			terminal_at INTEGER NOT NULL DEFAULT 0, UNIQUE(operation_id, ordinal), UNIQUE(id, operation_id)
		)`,
		`CREATE TABLE inference_submissions (
			id TEXT PRIMARY KEY, operation_id TEXT NOT NULL, attempt_id TEXT NOT NULL,
			ordinal INTEGER NOT NULL, attempt_ordinal INTEGER NOT NULL,
			provider TEXT NOT NULL DEFAULT '', protocol TEXT NOT NULL DEFAULT '',
			transport TEXT NOT NULL DEFAULT '', mode TEXT NOT NULL DEFAULT '', reason TEXT NOT NULL DEFAULT '',
			outcome TEXT NOT NULL, failure_category TEXT NOT NULL DEFAULT '', cost_state TEXT NOT NULL,
			reported_input_tokens INTEGER NOT NULL DEFAULT 0, reported_output_tokens INTEGER NOT NULL DEFAULT 0,
			reported_cache_creation INTEGER NOT NULL DEFAULT 0, reported_cache_read INTEGER NOT NULL DEFAULT 0,
			reported_cache_unknown INTEGER NOT NULL DEFAULT 0, has_reported_usage INTEGER NOT NULL DEFAULT 0,
			estimated_input_tokens INTEGER NOT NULL DEFAULT 0, estimated_output_tokens INTEGER NOT NULL DEFAULT 0,
			estimated_cache_creation INTEGER NOT NULL DEFAULT 0, estimated_cache_read INTEGER NOT NULL DEFAULT 0,
			estimated_cache_unknown INTEGER NOT NULL DEFAULT 0, has_estimated_usage INTEGER NOT NULL DEFAULT 0,
			output_bytes INTEGER NOT NULL DEFAULT 0, started_at INTEGER NOT NULL, completed_at INTEGER NOT NULL DEFAULT 0,
			UNIQUE(operation_id, ordinal)
		)`,
	}
	for _, statement := range legacySchema {
		if _, err := db.Exec(statement); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	now := time.Now().UTC().UnixMilli()
	hash := testInferenceHash("legacy")
	if _, err := db.Exec(`INSERT INTO inference_journal_runtimes VALUES ('runtime-legacy', 'workspace-legacy', 1, ?, ?, 0)`, now, now); err != nil {
		db.Close()
		t.Fatal(err)
	}
	if _, err := db.Exec(`
INSERT INTO inference_operations (
    id, runtime_id, workspace_scope, owner_id, kind, workload_profile,
    payload_version, request_hash, status, terminal_outcome, created_at, updated_at, terminal_at
) VALUES ('operation-legacy', 'runtime-legacy', 'workspace-legacy', 'thread-legacy',
          'agent_round', 'interactive', 1, ?, 'failed', 'failed', ?, ?, ?)`, hash, now, now, now); err != nil {
		db.Close()
		t.Fatal(err)
	}
	for ordinal := 1; ordinal <= 2; ordinal++ {
		if _, err := db.Exec(`
INSERT INTO inference_attempts (id, operation_id, ordinal, request_hash, phase, prepared_at)
VALUES (?, 'operation-legacy', ?, ?, 'terminal', ?)`, "attempt-legacy-"+string(rune('0'+ordinal)), ordinal, hash, now); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	legacySubmissions := []struct {
		id, cost  string
		reported  [4]int
		estimated [4]int
	}{
		{id: "submission-known", cost: "known", reported: [4]int{10, 5, 2, 3}},
		{id: "submission-estimated", cost: "estimated", estimated: [4]int{7, 3, 0, 0}},
		{id: "submission-unknown", cost: "unknown_but_billable"},
	}
	for ordinal, submission := range legacySubmissions {
		if _, err := db.Exec(`
INSERT INTO inference_submissions (
    id, operation_id, attempt_id, ordinal, attempt_ordinal, outcome, cost_state,
    reported_input_tokens, reported_output_tokens, reported_cache_creation, reported_cache_read, has_reported_usage,
    estimated_input_tokens, estimated_output_tokens, estimated_cache_creation, estimated_cache_read, has_estimated_usage,
    started_at
) VALUES (?, 'operation-legacy', 'attempt-legacy-1', ?, 1, 'failed', ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?, ?)`,
			submission.id, ordinal+1, submission.cost,
			submission.reported[0], submission.reported[1], submission.reported[2], submission.reported[3], submission.cost == "known",
			submission.estimated[0], submission.estimated[1], submission.estimated[2], submission.estimated[3], submission.cost == "estimated", now,
		); err != nil {
			db.Close()
			t.Fatal(err)
		}
	}
	if err := db.Close(); err != nil {
		t.Fatal(err)
	}

	forEachSessionStore(t, func(t *testing.T, store *Store) {
		for pass := 0; pass < 2; pass++ {
			result, err := MigrateLegacySQLite(context.Background(), dir, store.documents, NewLegacyInferenceConverter())
			if err != nil || result.AlreadyMigrated != (pass == 1) {
				t.Fatalf("migration pass %d: %+v %v", pass, result, err)
			}
			operation := readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", "operation-legacy")
			if operation.WorkflowID != "iwf-legacy-operation-legacy" || operation.AttemptLimit != 2 || len(operation.AttemptIDs) != 2 {
				t.Fatalf("legacy operation: %+v", operation)
			}
			workflow := readSessionDocument[inferenceWorkflowDocument](t, store, "inference_workflows", operation.WorkflowID)
			if workflow.Used[providers.WorkflowBudgetOperations] != 1 || workflow.Used[providers.WorkflowBudgetAttempts] != 2 || workflow.Used[providers.WorkflowBudgetSubmissions] != 3 || workflow.Used[providers.WorkflowBudgetSamePayloadReplays] != 1 || workflow.KnownSubmissions != 1 || workflow.EstimatedSubmissions != 1 || workflow.UnknownBillable != 1 || workflow.KnownUsageTokens != 20 || workflow.EstimatedUsageTokens != 10 || workflow.Status != "failed" {
				t.Fatalf("legacy workflow: %+v", workflow)
			}
		}
	})
}

func TestInferenceJournalDoesNotRecoverLiveRuntime(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		first, err := store.NewInferenceJournalRuntime("workspace-live")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = first.Close() })
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := first.ForOwner("thread").PrepareOperation(providers.InferenceOperationJournalRecord{
			Operation: op, RequestHash: testInferenceHash("live"), At: time.Now().Add(-time.Hour),
		}); err != nil {
			t.Fatal(err)
		}
		second, err := store.NewInferenceJournalRuntime("workspace-live")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = second.Close() })
		recovered, err := second.ReconcileOrphans(time.Now().Add(time.Hour))
		if err != nil {
			t.Fatal(err)
		}
		if len(recovered) != 0 {
			t.Fatalf("live runtime was treated as crashed: %+v", recovered)
		}
		var status string
		storedDocument0 := readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", op.ID)
		status = string(storedDocument0.Status)
		if status != "active" {
			t.Fatalf("live operation status = %q, want active", status)
		}
	})
}

func TestInferenceJournalRecoversTerminalRetryCheckpoint(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		oldRuntime, err := store.NewInferenceJournalRuntime("workspace-checkpoint")
		if err != nil {
			t.Fatal(err)
		}
		journal := oldRuntime.ForOwner("thread")
		now := time.Now().UTC()
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := testInferenceHash("checkpoint")
		attemptID := op.AttemptID(1)
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{OperationID: op.ID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{
			OperationID: op.ID, AttemptID: attemptID, Outcome: providers.InferenceOutcomeFailed,
			Failure: providers.InferenceJournalFailure{Category: providers.FailureNetwork}, At: now,
		}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareRecoveryAttempt(context.Background(), providers.InferenceRecoveryAttemptJournalRecord{
			Recovery: providers.InferenceRecoveryJournalRecord{
				OperationID: op.ID, AttemptID: attemptID, Action: providers.RecoveryReplaySame, At: now,
			},
			NextAttempt: providers.InferenceAttemptJournalRecord{
				OperationID: op.ID, AttemptID: op.AttemptID(2), Ordinal: 2, RequestHash: hash, At: now,
			},
		}); err != nil {
			t.Fatal(err)
		}
		crashInferenceRuntimeForTest(t, oldRuntime, now.Add(-time.Hour))
		newRuntime, err := store.NewInferenceJournalRuntime("workspace-checkpoint")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = newRuntime.Close() })
		recovered, err := newRuntime.ReconcileOrphans(now.Add(time.Minute))
		if err != nil {
			t.Fatal(err)
		}
		if len(recovered) != 1 || recovered[0].PriorPhase != "prepared" || recovered[0].PriorOutcome != "" ||
			recovered[0].PriorRecovery != "" || recovered[0].Action != providers.RecoveryRescheduleSafe {
			t.Fatalf("checkpoint recovery = %+v", recovered)
		}
		var attemptOutcome, operationStatus string
		storedDocument0 := readSessionDocument[inferenceAttemptDocument](t, store, "inference_attempts", attemptID)
		attemptOutcome = string(storedDocument0.TerminalOutcome)
		storedDocument1 := readSessionDocument[inferenceOperationDocument](t, store, "inference_operations", op.ID)
		operationStatus = string(storedDocument1.Status)
		if attemptOutcome != "failed" || operationStatus != "interrupted" {
			t.Fatalf("checkpoint persisted attempt=%q operation=%q", attemptOutcome, operationStatus)
		}
	})
}

func TestInferenceJournalSubmissionEvidenceIsMonotonic(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-monotonic")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread")
		now := time.Now().UTC()
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := testInferenceHash("monotonic")
		attemptID := op.AttemptID(1)
		submissionID := op.ID + "-s1"
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		if err := journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{OperationID: op.ID, AttemptID: attemptID, Ordinal: 1, RequestHash: hash, At: now}); err != nil {
			t.Fatal(err)
		}
		base := providers.InferenceSubmissionJournalRecord{
			OperationID: op.ID, AttemptID: attemptID, ID: submissionID, Ordinal: 1, AttemptOrdinal: 1,
			StartedAt: now, Outcome: providers.InferenceSubmissionSucceeded, CostState: providers.InferenceCostKnown,
			ReportedUsage: &providers.TokenUsage{InputTokens: 20, OutputTokens: 8}, OutputBytes: 32, CompletedAt: now.Add(time.Second),
		}
		if err := journal.UpsertSubmission(base); err != nil {
			t.Fatal(err)
		}
		stale := base
		stale.Outcome = providers.InferenceSubmissionInFlight
		stale.CostState = providers.InferenceCostUnknownBillable
		stale.ReportedUsage = nil
		stale.OutputBytes = 4
		stale.CompletedAt = time.Time{}
		if err := journal.UpsertSubmission(stale); err != nil {
			t.Fatal(err)
		}
		var outcome, cost string
		var input, output, outputBytes int
		var completedAt int64
		storedDocument0 := readSessionDocument[providers.InferenceSubmissionJournalRecord](t, store, "inference_submissions", submissionID)
		outcome = string(storedDocument0.Outcome)
		cost = string(storedDocument0.CostState)
		input = int(storedDocument0.ReportedUsage.InputTokens)
		output = int(storedDocument0.ReportedUsage.OutputTokens)
		outputBytes = int(storedDocument0.OutputBytes)
		completedAt = int64(storedDocument0.CompletedAt.UnixMilli())
		if outcome != "succeeded" || cost != "known" || input != 20 || output != 8 || outputBytes != 32 || completedAt == 0 {
			t.Fatalf("monotonic evidence regressed: %q %q usage=%d/%d bytes=%d completed=%d", outcome, cost, input, output, outputBytes, completedAt)
		}
	})
}

func TestInferenceJournalEnforcesOwnerAndDigestBoundaries(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-owner")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		owner := runtime.ForOwner("thread-a")
		op := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		hash := testInferenceHash("owner")
		if err := owner.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: hash}); err != nil {
			t.Fatal(err)
		}
		if err := runtime.ForOwner("thread-b").CompleteOperation(providers.InferenceOperationTerminalRecord{
			OperationID: op.ID, Outcome: providers.InferenceOutcomeSucceeded,
		}); err == nil {
			t.Fatal("different owner terminalized operation")
		}
		otherRuntime, err := store.NewInferenceJournalRuntime("workspace-owner")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = otherRuntime.Close() })
		if err := otherRuntime.ForOwner("thread-a").CompleteOperation(providers.InferenceOperationTerminalRecord{
			OperationID: op.ID, Outcome: providers.InferenceOutcomeSucceeded,
		}); err == nil {
			t.Fatal("different runtime terminalized operation")
		}
		raw := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := owner.PrepareOperation(providers.InferenceOperationJournalRecord{
			Operation: raw, RequestHash: "this is raw prompt text",
		}); err == nil {
			t.Fatal("non-digest request identity was persisted")
		}
	})
}

func TestInferenceJournalPrunesOnlyOldTerminalOperations(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		runtime, err := store.NewInferenceJournalRuntime("workspace-prune")
		if err != nil {
			t.Fatal(err)
		}
		t.Cleanup(func() { _ = runtime.Close() })
		journal := runtime.ForOwner("thread")
		now := time.Date(2026, 7, 13, 12, 0, 0, 0, time.UTC)
		var ids []string
		for _, age := range []time.Duration{31 * 24 * time.Hour, 29 * 24 * time.Hour} {
			op := providers.NewInferenceOperation(providers.InferenceOperationTitle, providers.InferenceProfileBestEffort)
			ids = append(ids, op.ID)
			at := now.Add(-age)
			if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: op, RequestHash: testInferenceHash("hash"), At: at}); err != nil {
				t.Fatal(err)
			}
			if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{OperationID: op.ID, Outcome: providers.InferenceOutcomeAbandoned, At: at}); err != nil {
				t.Fatal(err)
			}
			if err := journal.CompleteWorkflow(providers.InferenceWorkflowTerminalRecord{
				WorkflowID: "iwf-" + op.ID, Outcome: providers.InferenceOutcomeAbandoned, At: at,
			}); err != nil {
				t.Fatal(err)
			}
		}
		active := providers.NewInferenceOperation(providers.InferenceOperationAgentRound, providers.InferenceProfileInteractive)
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: active, RequestHash: testInferenceHash("hash"), At: now.Add(-40 * 24 * time.Hour)}); err != nil {
			t.Fatal(err)
		}

		deleted, err := runtime.Prune(now)
		if err != nil {
			t.Fatal(err)
		}
		if deleted != 2 {
			t.Fatalf("deleted = %d, want old operation and workflow", deleted)
		}
		var remaining []string
		if err := store.transaction(func(tx *persistence.Tx) error {
			operations, err := readDocumentRows[inferenceOperationDocument](tx, "inference_operations", "")
			if err != nil {
				return err
			}
			remaining = nil
			for _, operation := range operations {
				remaining = append(remaining, operation.ID)
			}
			return nil
		}); err != nil {
			t.Fatal(err)
		}
		sort.Strings(remaining)
		want := []string{ids[1], active.ID}
		sort.Strings(want)
		if len(remaining) != len(want) || remaining[0] != want[0] || remaining[1] != want[1] {
			t.Fatalf("remaining = %v, want %v", remaining, want)
		}
	})
}

func testInferenceHash(value string) string {
	digest := sha256.Sum256([]byte(value))
	return hex.EncodeToString(digest[:])
}

func crashInferenceRuntimeForTest(t *testing.T, runtime *InferenceJournalRuntime, heartbeat time.Time) {
	t.Helper()
	runtime.closeOnce.Do(func() {
		runtime.lifecycleMu.Lock()
		runtime.closing = true
		runtime.lifecycleMu.Unlock()
		runtime.activeWG.Wait()
		close(runtime.stop)
		<-runtime.heartbeatDone
		<-runtime.progressDone
	})
	if err := runtime.store.transaction(func(tx *persistence.Tx) error {
		record, err := requireInferenceDocument[InferenceRuntimeRecord](tx, "inference_journal_runtimes", runtime.runtimeID)
		if err != nil {
			return err
		}
		record.PID, record.HeartbeatAt, record.ClosedAt = 99999999, heartbeat.UnixMilli(), 0
		return tx.Put(documentNamespace("inference_journal_runtimes"), record.ID, record)
	}); err != nil {
		t.Fatal(err)
	}
}

func TestInferenceRecoveryAndPruningUseBoundedScopeReads(t *testing.T) {
	forEachSessionStore(t, func(t *testing.T, store *Store) {
		authority := &boundedInferenceDocuments{Documents: store.documents}
		selected := NewStore(context.Background(), authority)
		runtime, err := selected.NewInferenceJournalRuntime("bounded-scope")
		if err != nil {
			t.Fatal(err)
		}
		defer runtime.Close()
		journal := runtime.ForOwner("owner")
		operation := providers.NewInferenceOperation(providers.InferenceOperationTitle, providers.InferenceProfileBestEffort)
		at := time.Now().Add(-40 * 24 * time.Hour)
		if err := journal.PrepareOperation(providers.InferenceOperationJournalRecord{Operation: operation, RequestHash: testInferenceHash("bounded"), At: at}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteOperation(providers.InferenceOperationTerminalRecord{OperationID: operation.ID, Outcome: providers.InferenceOutcomeSucceeded, At: at}); err != nil {
			t.Fatal(err)
		}
		if err := journal.CompleteWorkflow(providers.InferenceWorkflowTerminalRecord{WorkflowID: "iwf-" + operation.ID, Outcome: providers.InferenceOutcomeSucceeded, At: at}); err != nil {
			t.Fatal(err)
		}
		if _, err := runtime.ReconcileOrphans(time.Now()); err != nil {
			t.Fatal(err)
		}
		if deleted, err := runtime.Prune(time.Now()); err != nil || deleted != 2 {
			t.Fatalf("prune: %d %v", deleted, err)
		}
	})
}

type boundedInferenceDocuments struct{ persistence.Documents }

func (s *boundedInferenceDocuments) Read(ctx context.Context, q persistence.Query) (persistence.View, error) {
	if q.Keys == nil && (q.Limit == 0 || q.Prefix == "") {
		return persistence.View{}, fmt.Errorf("unbounded inference scope read: %+v", q)
	}
	return s.Documents.Read(ctx, q)
}
