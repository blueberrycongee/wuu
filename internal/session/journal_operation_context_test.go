package session

import (
	"context"
	"encoding/json"
	"errors"
	"github.com/blueberrycongee/wuu/internal/providers"
	"sync"
	"testing"
	"time"
)

// One shared runtime must bind each operation independently, including lazy
// initialization and every required write, without retaining the first caller.
func TestJournalOperationContextBinding(t *testing.T) {
	type identityKey struct{}
	for _, lazy := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "lazy"}[lazy], func(t *testing.T) {
			var mu sync.Mutex
			seen := map[string]int{}
			client := NewClient(context.Background(), func(ctx context.Context, method string, _ json.RawMessage) (json.RawMessage, error) {
				identity, _ := ctx.Value(identityKey{}).(string)
				if identity == "" {
					t.Errorf("%s lost operation identity", method)
				}
				mu.Lock()
				seen[identity]++
				mu.Unlock()
				return json.RawMessage(`{"values":[]}`), nil
			})
			runtime := &InferenceJournalRuntime{remote: &remoteJournalRuntime{store: client, handle: "handle"}}
			if lazy {
				real := runtime
				runtime = NewLazyInferenceJournalRuntime(func(ctx context.Context) (*InferenceJournalRuntime, error) {
					if ctx.Value(identityKey{}) == nil {
						t.Error("initialization lost identity")
					}
					return real, nil
				})
			}
			shared := runtime.ForOwner("owner")
			var wg sync.WaitGroup
			for _, id := range []string{"first", "second"} {
				wg.Add(1)
				go func(id string) {
					defer wg.Done()
					operationCtx, cancel := context.WithCancel(context.WithValue(context.Background(), identityKey{}, id))
					ctx := providers.WithInferenceJournal(operationCtx, shared)
					cancel() // Required terminal writes retain identity after model cancellation.
					journal := providers.InferenceJournalFromContext(ctx)
					calls := []func() error{
						func() error { return journal.PrepareOperation(providers.InferenceOperationJournalRecord{}) },
						func() error { return journal.PrepareAttempt(providers.InferenceAttemptJournalRecord{}) },
						func() error {
							return journal.UpsertSubmission(context.Background(), providers.InferenceSubmissionJournalRecord{})
						},
						func() error { return journal.MarkAttemptFirstEvent("op", "attempt", "submission", time.Now()) },
						func() error { return journal.CompleteAttempt(providers.InferenceAttemptTerminalRecord{}) },
						func() error {
							return journal.PrepareRecoveryAttempt(context.Background(), providers.InferenceRecoveryAttemptJournalRecord{})
						},
						func() error { return journal.CompleteOperation(providers.InferenceOperationTerminalRecord{}) },
						func() error {
							return journal.CompleteWorkflow(context.Background(), providers.InferenceWorkflowTerminalRecord{})
						},
					}
					cancelled, stop := context.WithCancel(context.Background())
					stop()
					if err := journal.UpsertSubmission(cancelled, providers.InferenceSubmissionJournalRecord{}); !errors.Is(err, context.Canceled) {
						t.Errorf("explicit cancellation lost: %v", err)
					}
					if err := journal.CompleteWorkflow(cancelled, providers.InferenceWorkflowTerminalRecord{}); !errors.Is(err, context.Canceled) {
						t.Errorf("workflow explicit cancellation lost: %v", err)
					}
					deadline, stopDeadline := context.WithDeadline(context.Background(), time.Now().Add(-time.Second))
					defer stopDeadline()
					if err := journal.CompleteWorkflow(deadline, providers.InferenceWorkflowTerminalRecord{}); !errors.Is(err, context.DeadlineExceeded) {
						t.Errorf("workflow cleanup deadline lost: %v", err)
					}
					for _, call := range calls {
						if err := call(); err != nil {
							t.Error(err)
						}
					}
				}(id)
			}
			wg.Wait()
			for _, id := range []string{"first", "second"} {
				if seen[id] != 8 {
					t.Errorf("%s writes=%d", id, seen[id])
				}
			}
		})
	}
}

func TestJournalSettlementUsesLiveReceiptAdmission(t *testing.T) {
	type identityKey struct{}
	for _, lazy := range []bool{false, true} {
		t.Run(map[bool]string{false: "remote", true: "lazy"}[lazy], func(t *testing.T) {
			writes := 0
			client := NewClient(context.Background(), func(ctx context.Context, _ string, _ json.RawMessage) (json.RawMessage, error) {
				if ctx.Value(identityKey{}) != "admitted-operation" {
					t.Fatal("settlement replaced the journal's admitted identity")
				}
				_, end, err := providers.BeginModelReceipt(ctx)
				if err != nil {
					return nil, err
				}
				defer end(nil)
				writes++
				return json.RawMessage(`{"values":[]}`), nil
			})
			runtime := &InferenceJournalRuntime{remote: &remoteJournalRuntime{store: client, handle: "handle"}}
			if lazy {
				real := runtime
				runtime = NewLazyInferenceJournalRuntime(func(context.Context) (*InferenceJournalRuntime, error) { return real, nil })
			}
			operation, logical := providers.NewModelReceiptScope(context.WithValue(context.Background(), identityKey{}, "admitted-operation"))
			journal := providers.InferenceJournalFromContext(providers.WithInferenceJournal(operation, runtime.ForOwner("owner")))
			logical.Seal()
			completion, settlement := providers.NewModelReceiptScope(context.WithValue(context.Background(), identityKey{}, "unrelated-caller"))
			defer settlement.Seal()
			if err := journal.UpsertSubmission(completion, providers.InferenceSubmissionJournalRecord{}); err != nil {
				t.Fatalf("settlement inherited the ended logical ticket: %v", err)
			}
			if err := journal.CompleteWorkflow(completion, providers.InferenceWorkflowTerminalRecord{}); err != nil {
				t.Fatalf("workflow inherited the ended logical ticket: %v", err)
			}
			settlement.Seal()
			if err := journal.CompleteWorkflow(completion, providers.InferenceWorkflowTerminalRecord{}); !errors.Is(err, providers.ErrModelReceiptScopeClosed) {
				t.Fatalf("sealed settlement admitted a workflow: %v", err)
			}
			if err := journal.UpsertSubmission(completion, providers.InferenceSubmissionJournalRecord{}); !errors.Is(err, providers.ErrModelReceiptScopeClosed) {
				t.Fatalf("sealed settlement admitted a write: %v", err)
			}
			if writes != 2 {
				t.Fatalf("writes = %d, want submission and workflow settlement", writes)
			}
		})
	}
}
