package providers

import (
	"context"
	"errors"
	"slices"
	"testing"
)

func TestPhysicalReceiptDefersRequiredTerminalCheckpoint(t *testing.T) {
	for _, fail := range []bool{false, true} {
		t.Run(map[bool]string{false: "settled", true: "unknown-write"}[fail], func(t *testing.T) {
			journal := &recordingInferenceJournal{}
			ctx := WithInferenceJournal(context.Background(), journal)
			req, err := EnsureInferenceAttemptContext(ctx, ChatRequest{Model: "fixture"}, InferenceOperationAuxiliary, InferenceProfileInteractive)
			if err != nil {
				t.Fatal(err)
			}
			release, err := req.Attempt.RetainPhysicalReceipt()
			if err != nil {
				t.Fatal(err)
			}
			submission, err := req.Attempt.RecordSubmission(InferenceSubmissionMeta{})
			if err != nil {
				t.Fatal(err)
			}
			failure := NormalizeFailure(context.Canceled)
			if err = req.Attempt.Complete(InferenceOutcomeCanceled, failure); err != nil {
				t.Fatal(err)
			}
			if err = req.Execution.Complete(InferenceOutcomeCanceled, failure); err != nil {
				t.Fatal(err)
			}
			if slices.Contains(journal.events, "operation:canceled") || slices.Contains(journal.events, "attempt:canceled") {
				t.Fatal("logical cancellation invented durable terminal before physical completion")
			}
			if _, err = req.Execution.BeginAttemptChecked(); err == nil {
				t.Fatal("pending terminal admitted another attempt")
			}
			submission.CompleteFailure(failure)
			if fail {
				journal.failSubmission = errors.New("unknown physical checkpoint")
			}
			err = release()
			if fail {
				if err == nil || req.Attempt.JournalError() == nil {
					t.Fatal("required receipt failure lost")
				}
				if slices.Contains(journal.events, "operation:canceled") {
					t.Fatal("unknown physical checkpoint invented durable terminal")
				}
			} else if err != nil || !slices.Contains(journal.events, "operation:canceled") || !slices.Contains(journal.events, "attempt:canceled") {
				t.Fatalf("settled receipt did not flush terminal: %v %v", journal.events, err)
			}
		})
	}
}
