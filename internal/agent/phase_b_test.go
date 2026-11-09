package agent

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/providers"
)

func TestSystemPromptAssemblerPriorityOrdering(t *testing.T) {
	a := NewSystemPromptAssembler()
	a.Add(NewStaticPromptSection("low", "low priority", 1))
	a.Add(NewStaticPromptSection("high", "high priority", 100))
	a.Add(NewStaticPromptSection("mid", "mid priority", 50))

	_, sections := a.Assemble("")
	if len(sections) != 3 {
		t.Fatalf("expected 3 sections, got %d", len(sections))
	}
	if sections[0].Key != "high" {
		t.Errorf("expected high first, got %s", sections[0].Key)
	}
	if sections[1].Key != "mid" {
		t.Errorf("expected mid second, got %s", sections[1].Key)
	}
	if sections[2].Key != "low" {
		t.Errorf("expected low third, got %s", sections[2].Key)
	}
}

func TestSystemPromptAssemblerDuplicateKey(t *testing.T) {
	a := NewSystemPromptAssembler()
	a.Add(NewStaticPromptSection("key", "first", 1))
	a.Add(NewStaticPromptSection("key", "second", 2))

	if a.ProviderCount() != 1 {
		t.Errorf("expected 1 provider after duplicate key, got %d", a.ProviderCount())
	}

	_, sections := a.Assemble("")
	if len(sections) != 1 {
		t.Fatalf("expected 1 section, got %d", len(sections))
	}
	if sections[0].Bytes != len("second") {
		t.Errorf("expected second section (last registered), got %q", sections[0].Key)
	}
}

func TestSystemPromptAssemblerEmptySectionSkipped(t *testing.T) {
	a := NewSystemPromptAssembler()
	a.Add(NewStaticPromptSection("empty", "", 1))
	a.Add(NewStaticPromptSection("valid", "valid content", 1))

	_, sections := a.Assemble("")
	if len(sections) != 1 {
		t.Fatalf("expected 1 section (empty skipped), got %d", len(sections))
	}
	if sections[0].Key != "valid" {
		t.Errorf("expected valid, got %s", sections[0].Key)
	}
}

func TestRequestTransformChainOrdering(t *testing.T) {
	c := NewRequestTransformChain()
	var order []string

	c.Add(NewRequestTransform("mid", func(ctx context.Context, req *providers.ChatRequest) error {
		order = append(order, "mid")
		return nil
	}, 50))
	c.Add(NewRequestTransform("high", func(ctx context.Context, req *providers.ChatRequest) error {
		order = append(order, "high")
		return nil
	}, 100))
	c.Add(NewRequestTransform("low", func(ctx context.Context, req *providers.ChatRequest) error {
		order = append(order, "low")
		return nil
	}, 1))

	req := &providers.ChatRequest{Model: "test"}
	err := c.Apply(context.Background(), req, nil)
	if err != nil {
		t.Fatalf("unexpected error: %v", err)
	}

	if len(order) != 3 {
		t.Fatalf("expected 3 transforms, got %d", len(order))
	}
	if order[0] != "high" || order[1] != "mid" || order[2] != "low" {
		t.Errorf("expected [high mid low], got %v", order)
	}
}

func TestRequestTransformChainErrorStops(t *testing.T) {
	c := NewRequestTransformChain()
	var secondCalled bool

	c.Add(NewRequestTransform("first", func(ctx context.Context, req *providers.ChatRequest) error {
		return context.Canceled
	}, 10))
	c.Add(NewRequestTransform("second", func(ctx context.Context, req *providers.ChatRequest) error {
		secondCalled = true
		return nil
	}, 5))

	req := &providers.ChatRequest{Model: "test"}
	err := c.Apply(context.Background(), req, nil)
	if err == nil {
		t.Fatal("expected error from first transform")
	}
	if secondCalled {
		t.Error("second transform should not be called after error")
	}
}

func TestCompactionRegistryResolve(t *testing.T) {
	r := NewCompactionRegistry()

	// Empty registry returns nil.
	if r.Resolve(nil) != nil {
		t.Error("expected nil from empty registry")
	}

	// Register providers.
	r.Register(&mockCompactionProvider{key: "default", priority: 1})
	r.Register(&mockCompactionProvider{key: "custom", priority: 10})

	resolved := r.Resolve(nil)
	if resolved == nil {
		t.Fatal("expected resolved provider")
	}
	if resolved.CompactionKey() != "custom" {
		t.Errorf("expected custom (higher priority), got %s", resolved.CompactionKey())
	}

	if r.Count() != 2 {
		t.Errorf("expected 2 providers, got %d", r.Count())
	}
}

// mock types for testing

type mockCompactionProvider struct {
	key      string
	priority int
}

func (m *mockCompactionProvider) CompactionKey() string   { return m.key }
func (m *mockCompactionProvider) CompactionPriority() int { return m.priority }
func (m *mockCompactionProvider) Compact(ctx context.Context, model string, messages []providers.ChatMessage) ([]providers.ChatMessage, error) {
	return messages, nil
}
