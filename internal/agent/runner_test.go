package agent

import (
	"context"
	"errors"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type workflowRecordingJournal struct {
	completed []providers.InferenceWorkflowTerminalRecord
}

func (*workflowRecordingJournal) PrepareOperation(providers.InferenceOperationJournalRecord) error {
	return nil
}
func (*workflowRecordingJournal) PrepareAttempt(providers.InferenceAttemptJournalRecord) error {
	return nil
}
func (*workflowRecordingJournal) UpsertSubmission(providers.InferenceSubmissionJournalRecord) error {
	return nil
}
func (*workflowRecordingJournal) MarkAttemptFirstEvent(string, string, string, time.Time) error {
	return nil
}
func (*workflowRecordingJournal) CompleteAttempt(providers.InferenceAttemptTerminalRecord) error {
	return nil
}
func (*workflowRecordingJournal) PrepareRecoveryAttempt(context.Context, providers.InferenceRecoveryAttemptJournalRecord) error {
	return nil
}
func (*workflowRecordingJournal) CompleteOperation(providers.InferenceOperationTerminalRecord) error {
	return nil
}
func (j *workflowRecordingJournal) CompleteWorkflow(record providers.InferenceWorkflowTerminalRecord) error {
	j.completed = append(j.completed, record)
	return nil
}

type fakeStreamBackedClient struct {
	streamCalls int
	events      []providers.StreamEvent
}

func (f *fakeStreamBackedClient) Chat(_ context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "should not be used"}, nil
}

func (f *fakeStreamBackedClient) StreamChat(_ context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	f.streamCalls++
	ch := make(chan providers.StreamEvent, len(f.events))
	for _, event := range f.events {
		ch <- event
	}
	close(ch)
	return ch, nil
}

func TestRunner_UsesStreamingStepPath(t *testing.T) {
	client := &fakeStreamBackedClient{events: []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "streamed"},
		{Type: providers.EventDone},
	}}
	runner := Runner{Client: client, Model: "gpt-test"}

	answer, err := runner.Run(context.Background(), "hello")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "streamed" {
		t.Fatalf("unexpected answer: %s", answer)
	}
	if client.streamCalls != 1 {
		t.Fatalf("expected StreamChat to be called once, got %d", client.streamCalls)
	}
}

func TestRunnerPreservesExplicitInferenceWorkflow(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{{Content: "done"}}}
	journal := &workflowRecordingJournal{}
	runner := Runner{Client: client, Model: "gpt-test", InferenceJournal: journal}
	workflow := providers.NewInferenceWorkflow(providers.InferenceProfileInteractive)
	ctx := providers.WithInferenceWorkflow(context.Background(), workflow)

	if _, err := runner.Run(ctx, "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("requests = %d", len(client.requests))
	}
	if got := client.requests[0].Operation.WorkflowID; got != workflow.ID {
		t.Fatalf("workflow = %q, want %q", got, workflow.ID)
	}
	if len(journal.completed) != 0 {
		t.Fatalf("runner completed caller-owned workflow: %+v", journal.completed)
	}
}

func TestRunnerCompletesWorkflowItCreates(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{{Content: "done"}}}
	journal := &workflowRecordingJournal{}
	runner := Runner{Client: client, Model: "gpt-test", InferenceJournal: journal}

	if _, err := runner.Run(context.Background(), "hello"); err != nil {
		t.Fatalf("Run: %v", err)
	}
	if len(journal.completed) != 1 || journal.completed[0].Outcome != providers.InferenceOutcomeSucceeded {
		t.Fatalf("workflow completions = %+v", journal.completed)
	}
	if journal.completed[0].WorkflowID != client.requests[0].Operation.WorkflowID {
		t.Fatalf("completed workflow = %q, request workflow = %q", journal.completed[0].WorkflowID, client.requests[0].Operation.WorkflowID)
	}
}

func TestRunner_RunWithToolCall(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{
		{
			ToolCalls: []providers.ToolCall{{ID: "call_1", Name: "run_shell", Arguments: `{"command":"echo hi"}`}},
		},
		{Content: "final answer"},
	}}
	tool := &fakeTools{}
	runner := Runner{Client: client, Tools: tool, Model: "gpt-test", SystemPrompt: "sys", MaxSteps: 4}

	answer, err := runner.Run(context.Background(), "task")
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if answer != "final answer" {
		t.Fatalf("unexpected answer: %s", answer)
	}
	if len(tool.calls) != 1 {
		t.Fatalf("expected one tool call, got %d", len(tool.calls))
	}

	lastReq := client.requests[len(client.requests)-1]
	foundToolMessage := false
	for _, msg := range lastReq.Messages {
		if msg.Role == "tool" && msg.ToolCallID == "call_1" {
			foundToolMessage = true
			break
		}
	}
	if !foundToolMessage {
		t.Fatal("expected tool message in follow-up request")
	}
}

func TestRunner_OutputTruncationCompletesRun(t *testing.T) {
	client := &fakeClient{responses: []providers.ChatResponse{
		{Content: "part one ", Truncated: true, StopReason: "length"},
	}}
	runner := Runner{Client: client, Model: "gpt-test"}

	res, err := runner.RunWithUsage(context.Background(), "write a story", nil)
	if err != nil {
		t.Fatalf("Run returned error: %v", err)
	}
	if res.Content != "part one " {
		t.Fatalf("expected first partial answer, got %q", res.Content)
	}
	if res.FinishReason != providers.FinishReasonLength || res.StopReason != "length" || !res.Truncated {
		t.Fatalf("expected length finish metadata, got reason=%q stop=%q truncated=%v", res.FinishReason, res.StopReason, res.Truncated)
	}
	if len(client.requests) != 1 {
		t.Fatalf("expected 1 chat call, got %d", len(client.requests))
	}
}

func TestRunner_ContextOverflowFreshPromptPropagates(t *testing.T) {
	overflow := &providers.HTTPError{StatusCode: 400, Body: "context_length_exceeded", ContextOverflow: true}
	client := &fakeClient{
		responses: []providers.ChatResponse{
			{},
		},
		errors: []error{overflow},
	}
	runner := Runner{Client: client, Model: "gpt-test", SystemPrompt: "sys"}

	_, err := runner.Run(context.Background(), "do work")
	if err == nil || !providers.IsContextOverflow(err) {
		t.Fatalf("expected context overflow for fresh prompt, got %v", err)
	}
	if len(client.requests) != 1 {
		t.Fatalf("fresh prompt should not retry a no-op compact, got %d requests", len(client.requests))
	}
}

type fakeClient struct {
	responses []providers.ChatResponse
	errors    []error // optional, indexed parallel to responses
	requests  []providers.ChatRequest
	idx       int
}

func (f *fakeClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	f.requests = append(f.requests, req)
	if f.idx >= len(f.responses) {
		return providers.ChatResponse{}, errors.New("unexpected chat call")
	}
	resp := f.responses[f.idx]
	var err error
	if f.idx < len(f.errors) {
		err = f.errors[f.idx]
	}
	f.idx++
	if err != nil {
		return providers.ChatResponse{}, err
	}
	return resp, nil
}

type fakeTools struct {
	calls []providers.ToolCall
}

func (f *fakeTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "run_shell", InputSchema: map[string]any{"type": "object"}}}
}

func (f *fakeTools) Execute(_ context.Context, call providers.ToolCall) (string, error) {
	f.calls = append(f.calls, call)
	return `{"ok":true}`, nil
}
