package agent

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	"github.com/blueberrycongee/wuu/internal/pluginprotocol"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Public recovery/lifetime contracts: a slow summary must not hold the turn
// gate; application retains its concurrent tail; a newer reset/model fences
// stale proposals; a committed result survives losing the caller's reply;
// shutdown cancels and drains before resources are released.
type barrierCompactionProvider struct {
	started chan struct{}
	release chan struct{}
	drained chan struct{}
}

func (*barrierCompactionProvider) CompactionKey() string   { return "barrier" }
func (*barrierCompactionProvider) CompactionPriority() int { return 1 }
func (p *barrierCompactionProvider) Compact(ctx context.Context, _ string, _ []providers.ChatMessage) ([]providers.ChatMessage, error) {
	close(p.started)
	defer close(p.drained)
	select {
	case <-p.release:
		return []providers.ChatMessage{{Role: "user", Content: "Archived summary."}}, nil
	case <-ctx.Done():
		return nil, ctx.Err()
	}
}

func TestDriverCompactionConcurrentProposal(t *testing.T) {
	for _, scenario := range []string{"append", "reset", "model-change", "lost-reply", "shutdown", "cancel", "new-generation"} {
		t.Run(scenario, func(t *testing.T) {
			ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
			defer cancel()
			documents, err := persistence.Open(ctx, t.TempDir(), "jsonl")
			if err != nil {
				t.Fatal(err)
			}
			defer documents.Close()
			provider := &barrierCompactionProvider{started: make(chan struct{}), release: make(chan struct{}), drained: make(chan struct{})}
			registry := NewCompactionRegistry()
			registry.Register(provider)
			var admitted bool
			var seq = 2
			runner := &StreamRunner{
				Client: &fakeStreamBackedClient{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "New answer."}, {Type: providers.EventDone}}}, Model: "first-model", CompactionRegistry: registry,
				DriverCompactions: &DriverCompactionStore{Documents: documents, SessionID: "session"},
				BeforeStep: func() []providers.ChatMessage {
					if admitted {
						return nil
					}
					admitted = true
					return []providers.ChatMessage{{Role: "user", Content: "Concurrent steering.", Seq: 3}}
				},
				ArchiveHistory: func(_ context.Context, messages []providers.ChatMessage) (HistoryArchive, error) {
					seqs := make([]int, len(messages))
					for i := range messages {
						seq++
						seqs[i] = seq
					}
					return HistoryArchive{HeadSeq: seq, Seqs: seqs}, nil
				},
				CommitFreshContext: func(_ context.Context, messages []providers.ChatMessage, head int, _ string, _ CompactionNote) ([]providers.ChatMessage, int, error) {
					return messages, head, nil
				},
			}
			input := loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "Old request.", Seq: 1}, {Role: "assistant", Content: "Old answer.", Seq: 2}}}
			gateway := newStreamTurnGateway(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "first-execution"}), runner, input, nil)
			gateway.descriptor = loopdriver.Descriptor{ID: "test-driver", Version: "1"}
			defer gateway.close()
			current, err := gateway.ReadContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			proposal, err := gateway.StartCompaction(ctx, pluginprotocol.DriverCompactionParams{ID: "summary", Revision: current.Revision, Budget: pluginprotocol.DriverCompactBudget{Reason: "threshold"}})
			if err != nil || proposal.Status != "running" {
				t.Fatalf("start: %+v %v", proposal, err)
			}
			select {
			case <-provider.started:
			case <-ctx.Done():
				t.Fatal("summary never began")
			}
			if scenario == "shutdown" {
				gateway.close()
				select {
				case <-provider.drained:
				default:
					t.Fatal("close returned before summary drained")
				}
				return
			}
			if scenario == "cancel" {
				result, err := gateway.CancelCompaction(ctx, proposal.ID)
				if err != nil || result.Status != "canceled" {
					t.Fatalf("cancel: %+v %v", result, err)
				}
				select {
				case <-provider.drained:
				default:
					t.Fatal("cancel returned before summary drained")
				}
				result, err = gateway.ApplyCompaction(ctx, proposal.ID)
				if err != nil || result.Status != "canceled" {
					t.Fatalf("canceled summary applied: %+v %v", result, err)
				}
				return
			}
			if scenario == "new-generation" {
				registry.Unregister(provider.CompactionKey())
				result, err := gateway.ReadCompaction(ctx, proposal.ID, true)
				if err != nil || result.Status != "failed" {
					t.Fatalf("retired provider result: %+v %v", result, err)
				}
				select {
				case <-provider.drained:
				default:
					t.Fatal("retired provider was not canceled")
				}
				return
			}
			if _, err := gateway.AdmitInput(ctx); err != nil {
				t.Fatalf("summary blocked input: %v", err)
			}
			request, err := gateway.ReadTurn(ctx)
			if err != nil {
				t.Fatal(err)
			}
			if _, err := gateway.CallModel(ctx, request); err != nil {
				t.Fatalf("summary blocked model: %v", err)
			}
			if scenario == "reset" {
				current, _ = gateway.ReadContext(ctx)
				if _, err := gateway.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: []providers.ChatMessage{{Role: "user", Content: "New context."}}}); err != nil {
					t.Fatal(err)
				}
			}
			if scenario == "model-change" {
				gateway.modelBinding = &ModelStepBinding{Revision: 1, snapshot: &StreamRunner{Model: "second-model"}}
			}
			close(provider.release)
			result, err := gateway.ReadCompaction(ctx, proposal.ID, true)
			if err != nil || result.Status != "completed" {
				t.Fatalf("read: %+v %v", result, err)
			}
			if scenario == "lost-reply" {
				// Resume with the same host-owned store, not the old in-memory result.
				current, _ = gateway.ReadContext(ctx)
				gateway.close()
				gateway = newStreamTurnGateway(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "session", ExecutionID: "second-execution"}), runner, loopdriver.PersistedInput{Messages: current.Messages}, nil)
				gateway.descriptor = loopdriver.Descriptor{ID: "test-driver", Version: "1"}
				defer gateway.close()
			}
			result, err = gateway.ApplyCompaction(ctx, proposal.ID)
			if err != nil {
				t.Fatal(err)
			}
			if scenario == "reset" || scenario == "model-change" {
				if result.Status != "stale" {
					t.Fatalf("late proposal was not fenced: %+v", result)
				}
				return
			}
			if result.Status != "applied" {
				t.Fatalf("apply: %+v", result)
			}
			current, _ = gateway.ReadContext(ctx)
			if len(current.Messages) != 3 || current.Messages[0].Content != "Archived summary." || current.Messages[1].Content != "Concurrent steering." || current.Messages[2].Content != "New answer." {
				t.Fatalf("lost concurrent tail: %+v", current.Messages)
			}
		})
	}
}
