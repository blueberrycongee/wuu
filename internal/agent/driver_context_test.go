package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// A replacement must not erase original messages, race accepted input, or claim
// rollback after a durable commit. Failures before each persistence boundary
// must leave enough state for a caller to retry without duplicating the archive.
func TestDriverContextReplacementPersistenceBoundaries(t *testing.T) {
	for _, failure := range []string{"none", "archive", "commit", "cancel-after-commit"} {
		t.Run(failure, func(t *testing.T) {
			ctx, cancel := context.WithCancel(context.Background())
			defer cancel()
			var archived []providers.ChatMessage
			var committed []providers.ChatMessage
			commitCalls := 0
			failed := errors.New("injected persistence failure")
			runner := &StreamRunner{
				ArchiveHistory: func(_ context.Context, messages []providers.ChatMessage) (HistoryArchive, error) {
					if failure == "archive" {
						return HistoryArchive{}, failed
					}
					seqs := make([]int, len(messages))
					for i := range messages {
						seqs[i] = 11 + len(archived) + i
					}
					archived = append(archived, providers.CloneChatMessages(messages)...)
					return HistoryArchive{Seqs: seqs, HeadSeq: 10 + len(archived)}, nil
				},
				CommitFreshContext: func(_ context.Context, messages []providers.ChatMessage, head int, key string, _ CompactionNote) ([]providers.ChatMessage, int, error) {
					commitCalls++
					if failure == "commit" {
						return nil, head, failed
					}
					if key == "" || len(archived) != 1 || head != 11 {
						t.Fatalf("commit did not follow its archive: key=%q archived=%+v head=%d", key, archived, head)
					}
					committed = providers.CloneChatMessages(messages)
					committed[0].Seq = 12
					if failure == "cancel-after-commit" {
						cancel()
					}
					return committed, 12, nil
				},
			}
			gateway := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "original instruction", Seq: 10}}}, nil)
			defer gateway.close()
			initial, err := gateway.ReadContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			gateway.appendMessages(providers.ChatMessage{Role: "assistant", Content: "observed evidence"})
			if _, err := gateway.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: initial.Revision, Messages: []providers.ChatMessage{{Role: "user", Content: "stale"}}}); err == nil || len(archived) != 0 || commitCalls != 0 {
				t.Fatalf("stale replacement reached persistence: archived=%d commits=%d err=%v", len(archived), commitCalls, err)
			}
			current, err := gateway.ReadContext(ctx)
			if err != nil {
				t.Fatal(err)
			}
			replacement := []providers.ChatMessage{{Role: "user", Content: "focused context"}}
			changed, err := gateway.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: replacement})
			if failure == "archive" || failure == "commit" {
				if !errors.Is(err, failed) {
					t.Fatalf("lost persistence failure: %v", err)
				}
				var unchanged loopdriver.TurnContext
				unchanged, err = gateway.ReadContext(ctx)
				if err != nil || len(unchanged.Messages) != 2 || unchanged.Messages[0].Content != "original instruction" {
					t.Fatalf("failed preparation changed active context: %+v err=%v", unchanged, err)
				}
				partial, _ := gateway.result("")
				if partial.HistoryRewritten || (failure == "archive" && len(partial.DurableNewMessages) != 1) || (failure == "commit" && len(partial.DurableNewMessages) != 0) {
					t.Fatalf("failed replacement lost archive ownership: %+v", partial)
				}
				failure = "none"
				changed, err = gateway.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: unchanged.Revision, Messages: replacement})
			}
			if err != nil || len(changed.Messages) != 1 || changed.Messages[0].Seq != 12 {
				t.Fatalf("committed context lost: %+v err=%v", changed, err)
			}
			if failure != "cancel-after-commit" {
				receipt, err := gateway.CompleteTurn(ctx, loopdriver.TurnResult{Content: "answer", Messages: []providers.ChatMessage{{Role: "assistant", Content: "answer"}}})
				if err != nil {
					t.Fatal(err)
				}
				result, _ := gateway.result(receipt.ID)
				if !result.HistoryRewritten || len(result.NewMessages) != 2 || result.NewMessages[0].Content != "focused context" || len(result.DurableNewMessages) != 1 || result.DurableNewMessages[0].Content != "answer" {
					t.Fatalf("active context and durable originals mixed: %+v", result)
				}
			} else {
				gateway.close()
				result, _ := gateway.result("")
				if !result.HistoryRewritten || len(result.NewMessages) != 1 || result.NewMessages[0].Seq != 12 || result.HistoryArchiveHeadSeq != 12 {
					t.Fatalf("cancellation claimed to undo a committed context: %+v", result)
				}
			}
			if len(archived) != 1 || archived[0].Content != "observed evidence" || len(committed) != 1 {
				t.Fatalf("replacement duplicated or erased originals: archived=%+v committed=%+v", archived, committed)
			}
		})
	}
}

func TestDriverContextReplacementRequiresPersistence(t *testing.T) {
	ctx := context.Background()
	gateway := newStreamTurnGateway(ctx, &StreamRunner{}, loopdriver.PersistedInput{}, nil)
	defer gateway.close()
	current, err := gateway.ReadContext(ctx)
	if err != nil || current.CanReplace {
		t.Fatalf("missing persistence advertised replacement: %+v err=%v", current, err)
	}
	if _, err := gateway.ReplaceContext(ctx, loopdriver.ContextReplacement{Revision: current.Revision, Messages: []providers.ChatMessage{{Role: "user", Content: "replacement"}}}); err == nil {
		t.Fatal("replacement silently fell back to volatile state")
	}
}
