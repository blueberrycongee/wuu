package providers

import (
	"context"
	"errors"
	"testing"
)

// The transport boundary must not invent a second operation, send before a
// durable submission grant, or turn a lost receipt response into success.
func TestRemoteAttemptPhysicalAccounting(t *testing.T) {
	operation := NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive)
	execution := NewInferenceExecution(operation)
	attempt := execution.BeginAttempt()
	fail := false
	calls := 0
	remote := NewRemoteInferenceAttempt(operation, attempt.ID, attempt.Ordinal, func(method string, input, output any) error {
		calls++
		if fail {
			return errors.New("receipt response lost")
		}
		switch method {
		case "create":
			submission, err := attempt.RecordSubmission(input.(InferenceSubmissionMeta))
			if err != nil {
				return err
			}
			*output.(*InferenceSubmission) = submission
		case "snapshot":
			*output.(*[]InferenceSubmission) = attempt.SubmissionSnapshot()
		case "settle":
			update := input.(RemoteSubmissionUpdate)
			execution.mu.Lock()
			var submission InferenceSubmission
			for _, s := range execution.submissions {
				if s.ID == update.ID {
					submission = s
				}
			}
			execution.mu.Unlock()
			if update.Outcome == InferenceSubmissionSucceeded {
				submission.CompleteSuccess(update.Usage)
			}
		default:
			return errors.New("unsupported receipt operation")
		}
		return nil
	})
	request, err := EnsureInferenceAttemptContext(context.Background(), ChatRequest{Attempt: remote, Operation: operation}, InferenceOperationAuxiliary, InferenceProfileInteractive)
	if err != nil || request.Execution != nil || request.Attempt.ID != attempt.ID {
		t.Fatalf("remote attempt identity replaced: %+v %v", request, err)
	}
	submission, err := remote.RecordSubmission(InferenceSubmissionMeta{Provider: "fixture", Protocol: "http", Transport: "sse"})
	if err != nil {
		t.Fatal(err)
	}
	submission.CompleteSuccess(&TokenUsage{InputTokens: 2, OutputTokens: 3})
	snapshots := remote.SubmissionSnapshot()
	if len(snapshots) != 1 || snapshots[0].Outcome != InferenceSubmissionSucceeded || snapshots[0].ReportedUsage.OutputTokens != 3 {
		t.Fatalf("physical receipt lost: %+v", snapshots)
	}
	fail = true
	_, err = remote.RecordSubmission(InferenceSubmissionMeta{})
	if err == nil || remote.JournalError() == nil {
		t.Fatal("lost durable admission must fail closed")
	}
	previous := calls
	submission.CompleteSuccess(nil)
	if calls != previous {
		t.Fatal("unknown accounting outcome was blindly retried")
	}
}

func TestRemoteSubmissionPreservesTerminalPhysicalUsage(t *testing.T) {
	execution := NewInferenceExecution(NewInferenceOperation(InferenceOperationAgentRound, InferenceProfileInteractive))
	attempt := execution.BeginAttempt()
	submission, err := attempt.RecordSubmission(InferenceSubmissionMeta{})
	if err != nil {
		t.Fatal(err)
	}
	if err = submission.ApplyRemoteSubmissionUpdate(RemoteSubmissionUpdate{Outcome: InferenceSubmissionSucceeded, Usage: &TokenUsage{InputTokens: 7, OutputTokens: 4}}, true); err != nil {
		t.Fatal(err)
	}
	for _, settle := range []bool{false, true} {
		if err = submission.ApplyRemoteSubmissionUpdate(RemoteSubmissionUpdate{Outcome: InferenceSubmissionSucceeded, Usage: &TokenUsage{InputTokens: 1, OutputTokens: 1}, FromStream: true}, settle); err != nil {
			t.Fatal(err)
		}
	}
	got := attempt.SubmissionSnapshot()
	if len(got) != 1 || got[0].ReportedUsage.InputTokens != 7 || got[0].ReportedUsage.OutputTokens != 4 {
		t.Fatalf("late stream replaced physical usage: %+v", got)
	}
}
