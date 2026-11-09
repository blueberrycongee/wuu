package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/compact"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type failingNoteStore struct {
	cancellationNoteStore
	loadErr, saveErr error
}

func (s *failingNoteStore) LoadCompactionNote(ctx context.Context, key string) (CompactionNote, bool, error) {
	if s.loadErr != nil {
		return CompactionNote{}, false, s.loadErr
	}
	return s.cancellationNoteStore.LoadCompactionNote(ctx, key)
}

func (s *failingNoteStore) StoreCompactionNote(ctx context.Context, key string, note CompactionNote) error {
	if s.saveErr != nil {
		return s.saveErr
	}
	return s.cancellationNoteStore.StoreCompactionNote(ctx, key, note)
}

func fallbackHistory() []providers.ChatMessage {
	return []providers.ChatMessage{{Role: "system", Content: "instructions"}, {Role: "user", Content: "finish migration"}, {Role: "assistant", Content: strings.Repeat("migration verified; ", 3000)}}
}

func TestNoteFailureFallsBackToTraditionalCompact(t *testing.T) {
	failure := errors.New("note unavailable")
	for _, stage := range []string{"load", "generation", "reanchor", "unchanged"} {
		t.Run(stage, func(t *testing.T) {
			history := fallbackHistory()
			store := &failingNoteStore{}
			if stage == "load" {
				store.loadErr = failure
			}
			if stage == "reanchor" || stage == "unchanged" {
				store.note = CompactionNote{Markdown: "Migration applied", CoveredMessages: len(history), CoveredHash: CompactionHistoryHash(history)}
			}
			if stage == "reanchor" {
				store.saveErr = failure
			}
			registry := NewCompactionRegistry()
			if stage == "unchanged" {
				registry.Register(cancellationNoteProvider{})
			} else {
				registry.Register(replacementNoteProvider{})
			}
			calls, failures := 0, 0
			want := []providers.ChatMessage{{Role: "system", Content: compact.BuildSummaryContent("Migration applied; verify the result.")}}
			fn := resolveEffectiveCompaction(LoopConfig{
				Model: "test", CompactionRegistry: registry, CompactionNoteStore: store,
				ForkCompactionNote: func(context.Context, []providers.ChatMessage, CompactionNotePlan) (CompactionNoteForkResult, error) {
					return CompactionNoteForkResult{}, failure
				},
				OnCompactionNote: func(status string, err error) {
					if status == "failed" && err != nil {
						failures++
					}
				},
				Compact: func(_ context.Context, input []providers.ChatMessage) ([]providers.ChatMessage, error) {
					calls++
					if !reflect.DeepEqual(input, history) {
						t.Fatal("traditional compact received a failed replacement rather than original history")
					}
					return want, nil
				},
			})
			got, err := fn(context.Background(), history)
			if err != nil || !reflect.DeepEqual(got, want) || calls != 1 || failures != 1 {
				t.Fatalf("fallback failed: calls=%d failures=%d result=%+v err=%v", calls, failures, got, err)
			}
		})
	}
}

func TestTraditionalFallbackFailureAndCancellationPreserveHistory(t *testing.T) {
	noteErr, legacyErr := errors.New("note failed"), errors.New("summary failed")
	history := fallbackHistory()
	original := providers.CloneChatMessages(history)
	for _, mode := range []string{"error", "unchanged", "empty", "invalid-tools", "canceled", "deadline"} {
		t.Run(mode, func(t *testing.T) {
			cause := noteErr
			if mode == "canceled" {
				cause = context.Canceled
			}
			if mode == "deadline" {
				cause = context.DeadlineExceeded
			}
			calls := 0
			got, err := fallbackAfterNoteFailure(context.Background(), history, cause, func(_ context.Context, input []providers.ChatMessage) ([]providers.ChatMessage, error) {
				calls++
				switch mode {
				case "unchanged":
					return input, nil
				case "empty":
					return nil, nil
				case "invalid-tools":
					return []providers.ChatMessage{{Role: "tool", ToolCallID: "orphan", Content: "result"}}, nil
				}
				input[0].Content = "failed attempt mutated its input"
				return nil, legacyErr
			})
			if err == nil || got != nil || !reflect.DeepEqual(history, original) || !errors.Is(err, cause) {
				t.Fatalf("failure lost history or cause: %+v %v", got, err)
			}
			if mode == "error" && !errors.Is(err, legacyErr) {
				t.Fatal("fallback error was hidden")
			}
			if (mode == "canceled" || mode == "deadline") && calls != 0 {
				t.Fatal("cancellation started another inference")
			}
		})
	}
}

func TestNewContextContinuesWithoutSummaryInference(t *testing.T) {
	history := fallbackHistory()
	step := &contextSwitchStep{}
	archived, committed, calls := false, false, 0
	builder := func(_ context.Context, input []providers.ChatMessage, head, fixed, target int) ([]providers.ChatMessage, error) {
		calls++
		if !archived {
			t.Fatal("reset ran before durable archival")
		}
		return buildFreshContext(input, head, fixed, target)
	}
	res, err := RunToolLoop(context.Background(), history, LoopConfig{
		Model: "test", MaxSteps: 3, Tools: contextSwitchTools{},
		FreshContext: builder, FreshContextTokens: 4000,
		Compact: func(context.Context, []providers.ChatMessage) ([]providers.ChatMessage, error) {
			t.Fatal("context window invoked summary inference")
			return nil, nil
		},
		ArchiveHistory: func(context.Context, []providers.ChatMessage) (HistoryArchive, error) {
			archived = true
			return HistoryArchive{Seqs: []int{101, 102}, HeadSeq: 102}, nil
		},
		AcceptFreshContext: func(_ context.Context, messages []providers.ChatMessage, head int) ([]providers.ChatMessage, int, error) {
			committed = true
			return messages, head, nil
		},
	}, step)
	if err != nil || !res.HistoryRewritten || !committed || calls != 1 || len(step.requests) != 2 {
		t.Fatalf("session did not continue after fallback: calls=%d committed=%v rewritten=%v requests=%d err=%v", calls, committed, res.HistoryRewritten, len(step.requests), err)
	}
}
