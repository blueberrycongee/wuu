package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
)

type sideThreadRuntimeTools struct{}

func (sideThreadRuntimeTools) Definitions() []providers.ToolDefinition {
	return []providers.ToolDefinition{{Name: "write_file"}}
}

func (sideThreadRuntimeTools) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", nil
}

func TestNewSideThreadRunnerRemovesMutableMainThreadCallbacks(t *testing.T) {
	called := false
	base := &agent.StreamRunner{
		Client:               providers.AdaptStreamClient(&staticClient{}),
		Tools:                sideThreadRuntimeTools{},
		Model:                "test-model",
		SystemPrompt:         "main prompt",
		MaxSteps:             20,
		BeforeStep:           func() []providers.ChatMessage { called = true; return nil },
		BeforeRequestContext: func() []agent.ContextSegment { called = true; return nil },
		BeforeRequest:        func(context.Context, *providers.ChatRequest) error { called = true; return nil },
		AfterTurn:            func(context.Context, *agent.StreamRunner, []providers.ChatMessage, agent.LoopResult) { called = true },
		OnEvent:              func(providers.StreamEvent) { called = true },
	}
	runner, err := (&Session{StreamRunner: base}).NewSideThreadRunner("side-actual-id", "", ThreadModelSelection{})
	if err != nil {
		t.Fatalf("NewSideThreadRunner: %v", err)
	}
	if runner.Tools != nil || runner.BeforeStep != nil || runner.BeforeRequestContext != nil || runner.BeforeRequest != nil || runner.AfterTurn != nil || runner.OnEvent != nil {
		t.Fatalf("side runner retained mutable callbacks: %+v", runner)
	}
	if runner.MaxSteps != 20 {
		t.Fatalf("MaxSteps=%d want inherited 20", runner.MaxSteps)
	}
	if runner.PromptCacheKey != "side-thread:side-actual-id" {
		t.Fatalf("PromptCacheKey=%q", runner.PromptCacheKey)
	}
	if called {
		t.Fatal("constructing side runner invoked a main-thread callback")
	}
}

func TestNewSideThreadRunnerUsesReadOnlyMainAgentTools(t *testing.T) {
	root := t.TempDir()
	if err := os.WriteFile(filepath.Join(root, "evidence.txt"), []byte("side evidence\n"), 0o600); err != nil {
		t.Fatal(err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	base := &agent.StreamRunner{
		Client:       providers.AdaptStreamClient(&staticClient{}),
		ProviderName: "test-provider",
		Model:        "test-model",
		MaxSteps:     20,
	}
	runner, err := (&Session{RootDir: root, StreamRunner: base, Toolkit: kit}).NewSideThreadRunner("side-tools", root, ThreadModelSelection{})
	if err != nil {
		t.Fatalf("NewSideThreadRunner: %v", err)
	}
	if runner.Tools == nil {
		t.Fatal("side runner has no tools")
	}
	if !strings.Contains(runner.SystemPrompt, "Use your read-only tools") {
		t.Fatalf("side prompt does not teach read-only tool use:\n%s", runner.SystemPrompt)
	}
	readResult, err := runner.Tools.Execute(context.Background(), providers.ToolCall{
		ID:        "read-1",
		Name:      "read_file",
		Arguments: `{"path":"evidence.txt"}`,
	})
	if err != nil {
		t.Fatalf("read_file: %v", err)
	}
	if !strings.Contains(readResult, "side evidence") {
		t.Fatalf("read_file result = %q", readResult)
	}
	_, err = runner.Tools.Execute(context.Background(), providers.ToolCall{
		ID:        "write-1",
		Name:      "write_file",
		Arguments: `{"path":"blocked.txt","content":"must not exist\n"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "error_kind=boundary_denied") {
		t.Fatalf("write_file error = %v, want read-only boundary denial", err)
	}
	if _, statErr := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(statErr) {
		t.Fatalf("blocked write created a file, stat error = %v", statErr)
	}
}

// A read-only side chat must open even when the conversation's pinned model
// cannot be resolved (no loadable config, a removed provider). The runner
// degrades to the workspace model instead of failing the whole side chat.
func TestNewSideThreadRunnerFallsBackWhenModelUnresolvable(t *testing.T) {
	base := &agent.StreamRunner{
		Client: providers.AdaptStreamClient(&staticClient{}),
		Model:  "workspace-model",
	}
	s := &Session{ProviderName: "workspace-provider", Model: "workspace-model", StreamRunner: base}

	runner, err := s.NewSideThreadRunner("side-fallback", "", ThreadModelSelection{
		Provider: "ghost-provider",
		Model:    "ghost-model",
	})
	if err != nil {
		t.Fatalf("NewSideThreadRunner: %v", err)
	}
	if runner.Model != "workspace-model" {
		t.Fatalf("fallback runner model = %q, want workspace-model", runner.Model)
	}
}

// A side chat pinned to a different model must not inherit the workspace
// model's media admission policy, or unsupported images can reach the wire.
// Explicit test modalities keep this invariant independent of catalog updates.
func TestNewSideThreadRunnerReDerivesMediaInputForPinnedModel(t *testing.T) {
	root := t.TempDir()
	configPath := filepath.Join(root, "config.json")
	cfg := config.Config{
		DefaultProvider: "test",
		Providers: map[string]config.ProviderConfig{
			"test": {
				Type:    "openai-compatible",
				BaseURL: "https://example.invalid/v1",
				APIKey:  "test-key",
				Model:   "text-only-model",
				Models: map[string]config.ProviderModelConfig{
					"text-only-model": {
						Modalities: &config.ProviderModelModalitiesConfig{
							Input: []string{"text"}, Output: []string{"text"},
						},
					},
				},
			},
		},
	}
	raw, err := json.Marshal(cfg)
	if err != nil {
		t.Fatalf("marshal config: %v", err)
	}
	if err := os.WriteFile(configPath, raw, 0o600); err != nil {
		t.Fatal(err)
	}

	// The workspace runner's policy admits images (base model supports them
	// or is unknown). The pinned model is explicitly configured as text-only,
	// so the side runner must re-derive a rejecting policy.
	base := &agent.StreamRunner{
		Client: providers.AdaptStreamClient(&staticClient{}),
		Model:  "workspace-model",
		MediaInput: providers.MediaInputPolicy{
			Image: true, File: true, ImageKnown: true, FileKnown: true,
		},
	}
	s := &Session{
		StreamRunner:   base,
		ConfigLoadMode: ConfigLoadFile,
		ConfigPath:     configPath,
		RootDir:        root,
	}

	runner, err := s.NewSideThreadRunner("side-media", "", ThreadModelSelection{
		Provider: "test",
		Model:    "text-only-model",
	})
	if err != nil {
		t.Fatalf("NewSideThreadRunner: %v", err)
	}
	if runner.Model != "text-only-model" {
		t.Fatalf("pinned runner model = %q, want text-only-model", runner.Model)
	}
	want := providers.MediaInputPolicy{ImageKnown: true, FileKnown: true, VideoKnown: true}
	if runner.MediaInput != want {
		t.Fatalf("pinned runner media input = %+v, want %+v (text-only model must reject images)", runner.MediaInput, want)
	}
}

type staticClient struct{}

func (*staticClient) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{Content: "ok"}, nil
}
