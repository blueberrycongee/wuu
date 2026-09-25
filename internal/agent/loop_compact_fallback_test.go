package agent

import (
	"context"
	"errors"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

type funcCompactionProvider struct {
	key      string
	priority int
	compact  func(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error)
}

func (f *funcCompactionProvider) CompactionKey() string   { return f.key }
func (f *funcCompactionProvider) CompactionPriority() int { return f.priority }
func (f *funcCompactionProvider) Compact(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	return f.compact(ctx, model, messages)
}

func TestResolveEffectiveCompactionFallsBackWhenUnavailable(t *testing.T) {
	registry := NewCompactionRegistry()
	registry.Register(&funcCompactionProvider{
		key: "note", priority: 100,
		compact: func(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return nil, ErrCompactionUnavailable
		},
	})

	fallbackResult := []providers.ChatMessage{{Role: "system", Content: "default compacted"}}
	var fallbackInput []providers.ChatMessage
	fallback := func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
		fallbackInput = providers.CloneChatMessages(messages)
		return fallbackResult, nil
	}

	effective := resolveEffectiveCompaction(LoopConfig{
		Model:              "test-model",
		Compact:            fallback,
		CompactionRegistry: registry,
	})

	original := []providers.ChatMessage{{Role: "user", Content: "hello"}}
	got, err := effective(context.Background(), original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Content != "default compacted" {
		t.Fatalf("expected the default compactor result, got %+v", got)
	}
	if len(fallbackInput) != 1 || fallbackInput[0].Content != "hello" {
		t.Fatalf("fallback did not receive the original transcript: %+v", fallbackInput)
	}
}

func TestResolveEffectiveCompactionUnavailableWithoutFallbackIsNoop(t *testing.T) {
	registry := NewCompactionRegistry()
	registry.Register(&funcCompactionProvider{
		key: "note", priority: 100,
		compact: func(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return nil, ErrCompactionUnavailable
		},
	})

	effective := resolveEffectiveCompaction(LoopConfig{Model: "test-model", CompactionRegistry: registry})
	original := []providers.ChatMessage{{Role: "user", Content: "hello"}}
	got, err := effective(context.Background(), original)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Content != "hello" {
		t.Fatalf("expected the original transcript unchanged, got %+v", got)
	}
}

func TestResolveEffectiveCompactionUsesProviderResult(t *testing.T) {
	registry := NewCompactionRegistry()
	providerResult := []providers.ChatMessage{{Role: "system", Content: "plugin compacted"}}
	registry.Register(&funcCompactionProvider{
		key: "note", priority: 100,
		compact: func(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return providerResult, nil
		},
	})

	effective := resolveEffectiveCompaction(LoopConfig{
		Model: "test-model",
		Compact: func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			t.Fatal("default compactor must not run when the provider succeeds")
			return nil, nil
		},
		CompactionRegistry: registry,
	})

	got, err := effective(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hello"}})
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}
	if len(got) != 1 || got[0].Content != "plugin compacted" {
		t.Fatalf("expected the provider result, got %+v", got)
	}
}

func TestResolveEffectiveCompactionPropagatesProviderErrors(t *testing.T) {
	registry := NewCompactionRegistry()
	boom := errors.New("boom")
	registry.Register(&funcCompactionProvider{
		key: "note", priority: 100,
		compact: func(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			return nil, boom
		},
	})

	effective := resolveEffectiveCompaction(LoopConfig{
		Model: "test-model",
		Compact: func(ctx context.Context, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
			t.Fatal("default compactor must not run on provider errors other than ErrCompactionUnavailable")
			return nil, nil
		},
		CompactionRegistry: registry,
	})

	_, err := effective(context.Background(), []providers.ChatMessage{{Role: "user", Content: "hello"}})
	if !errors.Is(err, boom) {
		t.Fatalf("expected provider error to propagate, got %v", err)
	}
}
