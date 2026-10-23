package exec

import (
	"bytes"
	"context"
	"os"
	"path/filepath"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

// blockingProvider blocks on stream events until released or context done.
type bugReproProvider struct {
	started chan struct{}
	release chan struct{}
	once    sync.Once
}

func newBugReproProvider() *bugReproProvider {
	return &bugReproProvider{
		started: make(chan struct{}),
		release: make(chan struct{}),
	}
}

func (b *bugReproProvider) Chat(ctx context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	<-b.started
	select {
	case <-b.release:
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
	return providers.ChatResponse{Content: "done"}, nil
}

func (b *bugReproProvider) StreamChat(ctx context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	b.once.Do(func() { close(b.started) })
	ch := make(chan providers.StreamEvent, 4)
	go func() {
		defer close(ch)
		select {
		case <-b.release:
		case <-ctx.Done():
			ch <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
			return
		}
		ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "hello"}
		ch <- providers.StreamEvent{Type: providers.EventDone}
	}()
	return ch, nil
}

// TestCancellationEmitsTurnInterrupted verifies that when the parent
// context is canceled mid-turn, the JSONL event stream ends with
// turn_interrupted (not turn_failed).
func TestCancellationEmitsTurnInterrupted(t *testing.T) {
	root := t.TempDir()
	// This test exercises a real local app-server controller, which persists
	// sessions. Keep its user-level state out of the developer's actual ~/.wuu;
	// otherwise every run leaks a "do work" session into the Desktop's
	// projectless conversation list.
	t.Setenv("WUU_HOME", filepath.Join(root, ".wuu"))
	configPath := filepath.Join(root, ".wuu.json")
	if err := writeMinimalConfig(configPath); err != nil {
		t.Fatalf("write config: %v", err)
	}
	provider := newBugReproProvider()

	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	cfg, loadedPath, err := loadExecConfig(root, os.Getenv("HOME"), Options{ConfigPath: configPath})
	if err != nil {
		t.Fatalf("load config: %v", err)
	}
	rt, err := runtime.NewSession(runtime.Options{
		RootDir: root, HomeDir: os.Getenv("HOME"), ConfigPath: loadedPath,
		ConfigLoadMode: runtime.ConfigLoadFile, Config: cfg, NoTools: true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	rt.StreamRunner = &agent.StreamRunner{
		LoopDriver: agent.DefaultDriver{},

		Client:       providers.AdaptStreamClient(provider),
		Model:        "fake-model",
		SystemPrompt: "fake prompt",
	}
	controller := newLocalControllerForRuntime(ctx, rt)

	var stdout bytes.Buffer
	runCtx, runCancel := context.WithCancel(context.Background())
	defer runCancel()

	runDone := make(chan error, 1)
	go func() {
		runDone <- Run(runCtx, Options{
			Prompt:     "do work",
			JSON:       true,
			Stdout:     &stdout,
			Controller: controller,
		})
	}()

	// Wait for the provider to be hit (so the turn has started).
	select {
	case <-provider.started:
	case <-time.After(10 * time.Second):
		t.Fatalf("provider never started")
	}

	// Cancel the parent context.
	runCancel()

	select {
	case err := <-runDone:
		if ExitCode(err) != ExitInterrupted {
			t.Logf("Run returned err=%v (exit=%d)", err, ExitCode(err))
		}
	case <-time.After(10 * time.Second):
		t.Fatalf("Run did not return after cancel")
	}

	// Drain events.
	events := parseJSONLines(t, stdout.String())
	// Find the last turn-related event.
	var lastTurnEvent string
	for _, ev := range events {
		switch ev["type"] {
		case "turn_failed", "turn_interrupted":
			lastTurnEvent = ev["type"].(string)
		case "result":
			lastTurnEvent = "result:" + ev["status"].(string)
		}
	}

	if lastTurnEvent == "turn_failed" {
		t.Fatalf("final event is turn_failed, expected turn_interrupted")
	}
	if lastTurnEvent != "result:interrupted" && lastTurnEvent != "turn_interrupted" {
		t.Fatalf("unexpected final turn event: %q", lastTurnEvent)
	}

}

func writeMinimalConfig(path string) error {
	content := `{
		"default_provider": "test",
		"providers": {
			"test": {
				"type": "openai-compatible",
				"base_url": "https://example.test/v1",
				"api_key": "sk-test",
				"model": "gpt-test"
			}
		},
		"agent": {"permission_mode": "standard"}
	}`
	return os.WriteFile(path, []byte(content), 0o644)
}
