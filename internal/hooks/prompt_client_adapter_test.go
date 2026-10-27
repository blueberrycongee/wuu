package hooks

import (
	"context"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type adapterJournal struct {
	mu         sync.Mutex
	operations []providers.InferenceOperation
}

func (j *adapterJournal) PrepareOperation(record providers.InferenceOperationJournalRecord) error {
	j.mu.Lock()
	j.operations = append(j.operations, record.Operation)
	j.mu.Unlock()
	return nil
}
func (*adapterJournal) PrepareAttempt(providers.InferenceAttemptJournalRecord) error { return nil }
func (*adapterJournal) UpsertSubmission(context.Context, providers.InferenceSubmissionJournalRecord) error {
	return nil
}
func (*adapterJournal) MarkAttemptFirstEvent(string, string, string, time.Time) error { return nil }
func (*adapterJournal) CompleteAttempt(providers.InferenceAttemptTerminalRecord) error {
	return nil
}
func (*adapterJournal) PrepareRecoveryAttempt(context.Context, providers.InferenceRecoveryAttemptJournalRecord) error {
	return nil
}
func (*adapterJournal) CompleteOperation(providers.InferenceOperationTerminalRecord) error {
	return nil
}
func (*adapterJournal) CompleteWorkflow(providers.InferenceWorkflowTerminalRecord) error { return nil }

func (j *adapterJournal) snapshot() []providers.InferenceOperation {
	j.mu.Lock()
	defer j.mu.Unlock()
	return append([]providers.InferenceOperation(nil), j.operations...)
}

type adapterClient struct{}

func (adapterClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: `{"ok":true}`}, nil
}

func TestProviderModelClientUsesDefaultJournalOnlyForDetachedContext(t *testing.T) {
	defaultJournal := &adapterJournal{}
	requestJournal := &adapterJournal{}
	client := NewProviderModelClient(adapterClient{}, "test-model", defaultJournal)

	if _, err := client.ChatJSON(context.Background(), "", "system", "user"); err != nil {
		t.Fatal(err)
	}
	if operations := defaultJournal.snapshot(); len(operations) != 1 ||
		operations[0].Kind != providers.InferenceOperationHookPrompt ||
		operations[0].WorkloadProfile != providers.InferenceProfileContinuationCritical {
		t.Fatalf("default journal operations = %+v", operations)
	}

	ctx := providers.WithInferenceJournal(context.Background(), requestJournal)
	if _, err := client.ChatJSON(ctx, "", "system", "user"); err != nil {
		t.Fatal(err)
	}
	if operations := requestJournal.snapshot(); len(operations) != 1 {
		t.Fatalf("request journal operations = %+v, want one", operations)
	}
	if operations := defaultJournal.snapshot(); len(operations) != 1 {
		t.Fatalf("default journal received inherited-context operation: %+v", operations)
	}
}
