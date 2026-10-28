package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// Selection is an admission boundary, not a mutable request interceptor. A
// rejected revision must preserve the current binding, including failed-call
// retries, and a canceled execution must not resolve another selection.
func TestDriverModelSelectionBoundary(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	defer cancel()
	a, b := &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "ok"}, {Type: providers.EventDone}}}, &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "ok"}, {Type: providers.EventDone}}}
	pending := uint64(1)
	reject := false
	resolutions := 0
	runner := &StreamRunner{Client: a, ProviderName: "a", Model: "a", SystemPrompt: "first"}
	runner.ResolveModelStep = func(context.Context, uint64) (*ModelStepBinding, error) {
		resolutions++
		if reject {
			return nil, errors.New("selection rejected")
		}
		client, provider, prompt := providers.StreamClient(a), "a", "first"
		if pending == 2 {
			client, provider, prompt = b, "b", "second"
		}
		return &ModelStepBinding{Revision: pending, Client: client, Provider: provider, Model: provider, SystemPrompt: prompt}, nil
	}
	g := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "system", Content: "first"}, {Role: "user", Content: "hello"}}}, nil)
	defer g.close()
	first, err := g.ReadTurn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	pending = 2
	unchanged, err := g.ReadTurn(ctx)
	if err != nil || unchanged.Model != "a" || unchanged.ModelSelectionRevision != 1 {
		t.Fatalf("selection changed during assembly: %+v %v", unchanged, err)
	}
	if _, err := g.CallModel(ctx, first); err != nil {
		t.Fatal(err)
	}
	reject = true
	if _, err := g.AdmitInput(ctx); err == nil {
		t.Fatal("accepted rejected selection")
	}
	unchanged, _ = g.ReadTurn(ctx)
	if unchanged.Model != "a" {
		t.Fatal("failed prepare replaced live model")
	}
	reject = false
	if _, err := g.AdmitInput(ctx); err != nil {
		t.Fatal(err)
	}
	second, err := g.ReadTurn(ctx)
	if err != nil || second.Provider != "b" || second.ModelSelectionRevision != 2 || second.Messages[0].Content != "second" {
		t.Fatalf("selection not applied before assembly: %+v %v", second, err)
	}
	if _, err := g.CallModel(ctx, first); err == nil {
		t.Fatal("stale assembled request accepted")
	}
	if _, err := g.CallModel(ctx, second); err != nil {
		t.Fatal(err)
	}
	if len(a.requests) != 1 || len(b.requests) != 1 {
		t.Fatalf("wrong transport: a=%d b=%d", len(a.requests), len(b.requests))
	}
	before := resolutions
	cancel()
	if _, err := g.AdmitInput(context.Background()); err == nil || resolutions != before {
		t.Fatal("canceled execution resolved selection")
	}
}

func TestDefaultDriverRefreshesModelBeforeAssembly(t *testing.T) {
	first := &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventDone, FinishReason: providers.FinishReasonContinue}}}
	second := &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "done"}, {Type: providers.EventDone, FinishReason: providers.FinishReasonStop}}}
	calls := 0
	runner := &StreamRunner{LoopDriver: DefaultDriver{}, Client: first, ProviderName: "first", Model: "first", SystemPrompt: "first prompt", MaxSteps: 2}
	runner.ResolveModelStep = func(context.Context, uint64) (*ModelStepBinding, error) {
		calls++
		if calls == 1 {
			return &ModelStepBinding{Revision: 1, Client: first, Provider: "first", Model: "first", SystemPrompt: "first prompt"}, nil
		}
		return &ModelStepBinding{Revision: 2, Client: second, Provider: "second", Model: "second", SystemPrompt: "second prompt"}, nil
	}
	_, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "system", Content: "first prompt"}, {Role: "user", Content: "continue"}}, nil)
	if err != nil {
		t.Fatal(err)
	}
	if len(first.requests) != 1 || len(second.requests) != 1 {
		t.Fatalf("did not switch model across steps: first=%d second=%d", len(first.requests), len(second.requests))
	}
	if req := second.requests[0]; req.Provider != "second" || req.Model != "second" || req.Messages[0].Content != "second prompt" || req.ModelSelectionRevision != 2 {
		t.Fatalf("inconsistent second request: %+v", req)
	}
}

func TestDriverFailedModelRetryKeepsSelection(t *testing.T) {
	ctx := context.Background()
	old := &mockStreamClient{attempts: []mockStreamAttempt{{err: errors.New("permanent synthetic failure")}, {events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "recovered"}, {Type: providers.EventDone}}}}}
	next := &mockStreamClient{events: []providers.StreamEvent{{Type: providers.EventContentDelta, Content: "next"}, {Type: providers.EventDone}}}
	revision := uint64(1)
	runner := &StreamRunner{Client: old, ProviderName: "old", Model: "old"}
	runner.ResolveModelStep = func(context.Context, uint64) (*ModelStepBinding, error) {
		if revision == 1 {
			return &ModelStepBinding{Revision: 1, Client: old, Provider: "old", Model: "old"}, nil
		}
		return &ModelStepBinding{Revision: 2, Client: next, Provider: "next", Model: "next"}, nil
	}
	g := newStreamTurnGateway(ctx, runner, loopdriver.PersistedInput{Messages: []providers.ChatMessage{{Role: "user", Content: "retry"}}}, nil)
	defer g.close()
	request, err := g.ReadTurn(ctx)
	if err != nil {
		t.Fatal(err)
	}
	if _, err := g.CallModel(ctx, request); err == nil {
		t.Fatal("expected synthetic failure")
	}
	revision = 2
	if _, err := g.AdmitInput(ctx); err != nil {
		t.Fatal(err)
	}
	retry, err := g.ReadTurn(ctx)
	if err != nil || retry.ModelSelectionRevision != 1 || retry.Model != "old" {
		t.Fatalf("retry adopted pending selection: %+v %v", retry, err)
	}
	if _, err := g.CallModel(ctx, retry); err != nil {
		t.Fatal(err)
	}
	if len(next.requests) != 0 {
		t.Fatal("pending provider was used for retry")
	}
	if _, err := g.AdmitInput(ctx); err != nil {
		t.Fatal(err)
	}
	selected, _ := g.ReadTurn(ctx)
	if selected.Model != "next" {
		t.Fatal("successful retry did not open next boundary")
	}
}
