package appserver

import (
	"encoding/json"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSanitizeStreamEventIncludesNotice(t *testing.T) {
	got := sanitizeStreamEvent(providers.StreamEvent{
		Type: providers.EventNotice,
		Notice: &providers.StreamNotice{
			Code:    "mcp_unavailable",
			Message: "host tools unavailable",
		},
	})
	if got.Notice == nil || got.Notice.Code != "mcp_unavailable" || got.Notice.Message != "host tools unavailable" {
		t.Fatalf("unexpected notice payload: %+v", got.Notice)
	}
	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal stream event: %v", err)
	}
	if !strings.Contains(string(raw), `"notice":{"code":"mcp_unavailable","message":"host tools unavailable"}`) {
		t.Fatalf("notice missing from wire payload: %s", raw)
	}
}

func TestSanitizeStreamEventIncludesLifecycle(t *testing.T) {
	got := sanitizeStreamEvent(providers.StreamEvent{
		Type: providers.EventLifecycle,
		Lifecycle: &providers.StreamLifecycle{
			Phase:           providers.StreamPhaseReconnecting,
			OperationID:     "iop-test",
			OperationKind:   providers.InferenceOperationAgentRound,
			WorkloadProfile: providers.InferenceProfileInteractive,
			PayloadVersion:  1,
			AttemptID:       "iop-test-a2",
			Attempt:         2,
			MaxAttempts:     4,
			SubmissionID:    "iop-test-s2",
			SubmissionCount: 2,
			RetryCount:      1,
			MaxRetries:      3,
			RetryIn:         1500 * time.Millisecond,
			Elapsed:         2 * time.Second,
			Reason:          "connection reset",
			FailureCategory: "transport",
			RecoveryAction:  "replay_same",
			BudgetDimension: "same_payload_replays",
			ReplayReason:    "invocation_running",
			ResetPartial:    true,
			Workflow: providers.WorkflowBudgetSnapshot{
				WorkflowID: "iwf-test", Operations: 3, Attempts: 5, Submissions: 5,
				TransportSwitches: 1, RecoveryWaitMillis: 1500,
				KnownSubmissions: 3, EstimatedSubmissions: 1, UnknownBillableSubmissions: 1,
				KnownUsage: providers.TokenUsage{InputTokens: 120, OutputTokens: 30},
			},
		},
	})

	if got.Lifecycle == nil {
		t.Fatal("expected lifecycle payload")
	}
	if got.Lifecycle.Phase != "reconnecting" ||
		got.Lifecycle.OperationID != "iop-test" ||
		got.Lifecycle.OperationKind != "agent_round" ||
		got.Lifecycle.WorkloadProfile != "interactive" ||
		got.Lifecycle.AttemptID != "iop-test-a2" ||
		got.Lifecycle.Attempt != 2 ||
		got.Lifecycle.MaxAttempts != 4 ||
		got.Lifecycle.SubmissionID != "iop-test-s2" ||
		got.Lifecycle.SubmissionCount != 2 ||
		got.Lifecycle.RetryCount != 1 ||
		got.Lifecycle.MaxRetries != 3 ||
		got.Lifecycle.RetryInMS != 1500 ||
		got.Lifecycle.ElapsedMS != 2000 ||
		!got.Lifecycle.ResetPartial ||
		got.Lifecycle.Reason != "connection reset" ||
		got.Lifecycle.FailureCategory != "transport" ||
		got.Lifecycle.RecoveryAction != "replay_same" ||
		got.Lifecycle.BudgetDimension != "same_payload_replays" ||
		got.Lifecycle.ReplayReason != "invocation_running" ||
		got.Lifecycle.Workflow == nil ||
		got.Lifecycle.Workflow.ID != "iwf-test" ||
		got.Lifecycle.Workflow.Operations != 3 ||
		got.Lifecycle.Workflow.Attempts != 5 ||
		got.Lifecycle.Workflow.UnknownBillableSubmissions != 1 ||
		got.Lifecycle.Workflow.KnownInputTokens != 120 {
		t.Fatalf("unexpected lifecycle payload: %+v", got.Lifecycle)
	}

	raw, err := json.Marshal(got)
	if err != nil {
		t.Fatalf("marshal stream event: %v", err)
	}
	wire := string(raw)
	if !strings.Contains(wire, `"retry_in_ms":1500`) || !strings.Contains(wire, `"max_attempts":4`) || !strings.Contains(wire, `"attempt_id":"iop-test-a2"`) || !strings.Contains(wire, `"submission_id":"iop-test-s2"`) || !strings.Contains(wire, `"failure_category":"transport"`) || !strings.Contains(wire, `"unknown_billable_submissions":1`) {
		t.Fatalf("expected snake_case lifecycle wire payload, got %s", wire)
	}
	if strings.Contains(wire, "RetryIn") {
		t.Fatalf("wire payload leaked Go field names: %s", wire)
	}
}
