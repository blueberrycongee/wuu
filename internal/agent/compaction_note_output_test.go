package agent

import (
	"context"
	"errors"
	"reflect"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type noteOutput struct {
	body   string
	finish providers.FinishReason
}

type noteOutputClient struct {
	streamOnlyNoteClient
	outputs  []noteOutput
	requests []providers.ChatRequest
}

func (c *noteOutputClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.requests = append(c.requests, req)
	output := c.outputs[min(len(c.requests)-1, len(c.outputs)-1)]
	if req.Attempt.Valid() {
		req.Attempt.RecordSubmission(providers.InferenceSubmissionMeta{Provider: "test", Protocol: "mock", Transport: "memory", Mode: "stream"})
	}
	events := make(chan providers.StreamEvent, 2)
	events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: output.body}
	events <- providers.StreamEvent{Type: providers.EventDone, FinishReason: output.finish, Usage: &providers.TokenUsage{InputTokens: 100, OutputTokens: 20, CacheReadTokens: 50}}
	close(events)
	return events, nil
}

func TestNoteOutputRecoveryInstallsCompleteCheckpoint(t *testing.T) {
	// Recreate the incident's 9984-byte bound with both ASCII-heavy notes and
	// multilingual prose. Token limits are deliberately ignored by the client.
	for _, first := range []noteOutput{
		{strings.Repeat("migration details ", 700), providers.FinishReasonStop},
		{strings.Repeat("迁移已完成。", 600), providers.FinishReasonStop},
		{"Only the first half of the task", providers.FinishReasonLength},
	} {
		t.Run(string(first.finish)+"/"+first.body[:6], func(t *testing.T) {
			client := &noteOutputClient{outputs: []noteOutput{first, {"Migration applied. Verify tests; do not reapply. [History Seq 41]", providers.FinishReasonStop}}}
			runner := &StreamRunner{LoopDriver: DefaultDriver{}, Client: client, ProviderName: "test", Model: "test"}
			history := []providers.ChatMessage{{Role: "user", Content: "finish migration", Seq: 1}, {Role: "assistant", Content: "Migration applied", Seq: 41}}
			store := &cancellationNoteStore{}
			ctx := context.WithValue(context.Background(), compactionNoteBudgetKey{}, 39936)
			note, usage, err := generateCompactionNote(ctx, cancellationNoteProvider{}, store, runner.CompactionNoteFork(), "test", history, true)
			if err != nil {
				t.Fatal(err)
			}
			if len(client.requests) != 2 || note.Markdown != client.outputs[1].body || store.storeCalls != 1 || !validCompactionNote(note, history) {
				t.Fatalf("did not install the complete regenerated checkpoint: calls=%d note=%+v stores=%d", len(client.requests), note, store.storeCalls)
			}
			if usage == nil || usage.InputTokens != 200 || usage.OutputTokens != 40 || usage.CacheReadTokens != 100 {
				t.Fatalf("retry costs lost: %+v", usage)
			}
			for _, req := range client.requests {
				if !reflect.DeepEqual(req.Messages[:len(req.Messages)-1], client.requests[0].Messages[:len(client.requests[0].Messages)-1]) {
					t.Fatal("retry changed the source snapshot or included rejected output as history")
				}
			}
		})
	}
}

func TestNoteOutputExhaustionPreservesCheckpointAndContextRecovery(t *testing.T) {
	history := []providers.ChatMessage{{Role: "user", Content: "finish migration", Seq: 1}}
	for i := 0; i < 10; i++ {
		history = append(history, providers.ChatMessage{Role: "assistant", Content: strings.Repeat("verified migration details ", 100), Seq: i + 2})
	}
	previous := CompactionNote{Markdown: "Old progress", CoveredMessages: len(history), CoveredHash: CompactionHistoryHash(history)}
	history = append(history, providers.ChatMessage{Role: "assistant", Content: "latest verified result", Seq: 41})
	store := &cancellationNoteStore{note: previous}
	client := &noteOutputClient{outputs: []noteOutput{{strings.Repeat("未完", 2000), providers.FinishReasonStop}}}
	runner := &StreamRunner{LoopDriver: DefaultDriver{}, Client: client, ProviderName: "test", Model: "test"}
	ctx := context.WithValue(context.Background(), compactionNoteBudgetKey{}, 39936)
	note, usage, err := generateCompactionNote(ctx, cancellationNoteProvider{}, store, runner.CompactionNoteFork(), "test", history, true)
	if err == nil || note.Markdown != "" || store.note != previous || store.storeCalls != 0 || len(client.requests) != 3 {
		t.Fatalf("unbounded retry or invalid checkpoint installed: note=%+v calls=%d err=%v", note, len(client.requests), err)
	}
	if usage == nil || usage.InputTokens != 300 {
		t.Fatalf("exhausted retry usage lost: %+v", usage)
	}
	replacement, err := buildFreshContext(history, 41, 0, 4000)
	if err != nil || providers.ValidateToolCallHistory(replacement) != nil {
		t.Fatalf("note failure blocked context recovery: %v", err)
	}
	foundLatest := false
	for _, message := range replacement {
		foundLatest = foundLatest || message.Cause == "fresh_context"
	}
	if !foundLatest {
		t.Fatal("recovery lost uncovered progress")
	}
}

func TestNoteOutputExactUTF8LimitAndCancellation(t *testing.T) {
	client := &noteOutputClient{outputs: []noteOutput{{strings.Repeat("中", 16), providers.FinishReasonStop}}}
	runner := &StreamRunner{LoopDriver: DefaultDriver{}, Client: client, ProviderName: "test", Model: "test"}
	result, err := runner.CompactionNoteFork()(context.Background(), nil, CompactionNotePlan{Prompt: "write brief", MaxBytes: 48})
	if err != nil || len(result.Markdown) != 48 || len(client.requests) != 1 {
		t.Fatalf("exact UTF-8 bound rejected: %+v %v", result, err)
	}
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	calls := 0
	fork := boundedCompactionNoteFork(func(context.Context, []providers.ChatMessage, CompactionNotePlan) (CompactionNoteForkResult, error) {
		calls++
		cancel()
		return CompactionNoteForkResult{Markdown: strings.Repeat("中", 100), Usage: &providers.TokenUsage{OutputTokens: 10}}, nil
	})
	result, err = fork(ctx, nil, CompactionNotePlan{Prompt: "write brief", MaxBytes: 48})
	if !errors.Is(err, context.Canceled) || calls != 1 || result.Markdown != "" || result.Usage.OutputTokens != 10 {
		t.Fatalf("cancellation retried, installed output, or lost cost: calls=%d result=%+v err=%v", calls, result, err)
	}
}

func TestNoteOutputDoesNotRetryUnrelatedFailures(t *testing.T) {
	failure := errors.New("provider unavailable")
	calls := 0
	fork := boundedCompactionNoteFork(func(context.Context, []providers.ChatMessage, CompactionNotePlan) (CompactionNoteForkResult, error) {
		calls++
		return CompactionNoteForkResult{}, failure
	})
	if _, err := fork(context.Background(), nil, CompactionNotePlan{MaxBytes: 48}); !errors.Is(err, failure) || calls != 1 {
		t.Fatalf("output repair multiplied unrelated retries: calls=%d err=%v", calls, err)
	}
}
