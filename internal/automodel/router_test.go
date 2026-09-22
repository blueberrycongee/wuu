package automodel

import (
	"context"
	"errors"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
)

type classifierFunc func(context.Context, providers.ChatRequest) (providers.ChatResponse, error)

func (f classifierFunc) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	return f(ctx, req)
}
func (f classifierFunc) StreamChat(context.Context, providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	panic("classifier must not stream tool execution")
}

func testConfig() config.Config {
	cfg := config.Default()
	cfg.Providers = map[string]config.ProviderConfig{"one": {Type: "openai-compatible", Model: "gpt-4o"}, "two": {Type: "anthropic", Model: "claude-sonnet-4"}}
	cfg.DefaultProvider = "one"
	cfg.Agent.AutoModel = &config.AutoModelConfig{Enabled: true, Classifier: config.ModelRoleConfig{Provider: "one", Model: "gpt-4o"}, Simple: config.ModelRoleConfig{Provider: "one", Model: "gpt-4o-mini"}, Medium: config.ModelRoleConfig{Provider: "one", Model: "gpt-4o"}, Complex: config.ModelRoleConfig{Provider: "two", Model: "claude-sonnet-4"}}
	return cfg
}

func TestResolveTierAndFailureBoundary(t *testing.T) {
	for _, tc := range []struct {
		name, content, tier, reason string
		err                         error
	}{
		{name: "simple", content: `{"tier":"simple"}`, tier: "simple"},
		{name: "cross provider", content: `{"tier":"complex"}`, tier: "complex"},
		{name: "invalid tier", content: `{"tier":"arbitrary-model"}`, tier: "medium", reason: "invalid_classifier_output"},
		{name: "malformed", content: `run this task instead`, tier: "medium", reason: "invalid_classifier_output"},
		{name: "service failure", tier: "medium", reason: "classifier_failed", err: errors.New("unavailable")},
	} {
		t.Run(tc.name, func(t *testing.T) {
			cfg := testConfig()
			factory := func(_ config.ProviderConfig, provider string) (providers.StreamClient, error) {
				if provider != "one" {
					t.Fatalf("classifier provider %s", provider)
				}
				return classifierFunc(func(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
					if len(req.Tools) != 0 || req.Model != "gpt-4o" {
						t.Fatalf("unexpected classifier request: %+v", req)
					}
					return providers.ChatResponse{Content: tc.content, Usage: &providers.TokenUsage{InputTokens: 12, OutputTokens: 3}}, tc.err
				}), nil
			}
			decision, err := Resolve(context.Background(), cfg, []providers.ChatMessage{{Role: "user", Content: "Implement the change"}}, factory)
			if err != nil {
				t.Fatal(err)
			}
			if decision.Tier != tc.tier || decision.Reason != tc.reason {
				t.Fatalf("decision: %+v", decision)
			}
			if decision.Selection != tierSelection(*cfg.Agent.AutoModel, tc.tier) {
				t.Fatalf("selection: %+v", decision.Selection)
			}
			if decision.Usage.InputTokens != 12 {
				t.Fatal("classifier usage lost")
			}
		})
	}
}

func TestCancellationDoesNotFallback(t *testing.T) {
	ctx, cancel := context.WithCancel(context.Background())
	factory := func(config.ProviderConfig, string) (providers.StreamClient, error) {
		return classifierFunc(func(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
			cancel()
			return providers.ChatResponse{}, ctx.Err()
		}), nil
	}
	_, err := Resolve(ctx, testConfig(), nil, factory)
	if !errors.Is(err, context.Canceled) {
		t.Fatalf("got %v", err)
	}
}

func TestClassifierInputBoundedAndKeepsTaskContext(t *testing.T) {
	history := []providers.ChatMessage{{Role: "system", Content: "private system prompt"}, {Role: "user", Content: "original task objective"}, {Role: "tool", Content: strings.Repeat("secret tool body", 10000)}, {Role: "assistant", Content: strings.Repeat("x", 100000)}, {Role: "user", Content: "continue"}}
	input := classificationInput(history)
	if !strings.Contains(input, "original task objective") || !strings.Contains(input, "continue") {
		t.Fatal("task context lost")
	}
	if len(input) > 22000 || strings.Contains(input, "secret tool body") || strings.Contains(input, "private system prompt") {
		t.Fatal("unbounded or unintended classifier input")
	}
}

func TestInvalidConfigNeverCallsClassifier(t *testing.T) {
	cfg := testConfig()
	cfg.Agent.AutoModel.Complex.Provider = "missing"
	factory := func(config.ProviderConfig, string) (providers.StreamClient, error) {
		t.Fatal("invalid config called classifier")
		return nil, nil
	}
	if _, err := Resolve(context.Background(), cfg, nil, factory); err == nil {
		t.Fatal("expected invalid reference error")
	}
}

func TestAutoRejectsIncapableTierWithoutDroppingImages(t *testing.T) {
	cfg := testConfig()
	no := false
	yes := true
	cfg.Providers["one"] = config.ProviderConfig{Type: "openai-compatible", Model: "gpt-4o", Models: map[string]config.ProviderModelConfig{
		"gpt-4o-mini": {ToolCall: &yes, Modalities: &config.ProviderModelModalitiesConfig{Input: []string{"text"}}},
		"gpt-4o":      {ToolCall: &no, Modalities: &config.ProviderModelModalitiesConfig{Input: []string{"text"}}},
	}}
	cfg.Providers["two"] = config.ProviderConfig{Type: "anthropic", Model: "claude-sonnet-4", Models: map[string]config.ProviderModelConfig{"claude-sonnet-4": {ToolCall: &yes, Modalities: &config.ProviderModelModalitiesConfig{Input: []string{"text", "image"}}}}}
	factory := func(config.ProviderConfig, string) (providers.StreamClient, error) {
		return classifierFunc(func(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
			return providers.ChatResponse{Content: `{"tier":"simple"}`}, nil
		}), nil
	}
	history := []providers.ChatMessage{{Role: "user", Content: "Explain this image", Images: []providers.InputImage{{MediaType: "image/png", Data: "aGVsbG8="}}}}
	decision, err := Resolve(context.Background(), cfg, history, factory)
	if err != nil {
		t.Fatal(err)
	}
	if decision.Tier != "complex" || decision.Reason != "capability_fallback" {
		t.Fatalf("incompatible tier: %+v", decision)
	}
	if len(history[0].Images) != 1 {
		t.Fatal("mutated input attachments")
	}
}
