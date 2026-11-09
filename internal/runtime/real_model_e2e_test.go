//go:build wuu_real_model_e2e

package runtime_test

import (
	"context"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/runtime"
)

func TestRealModelLongSingleUserTurnCompactsAndCompletes(t *testing.T) {
	if os.Getenv("WUU_REAL_MODEL_E2E") != "1" {
		t.Skip("set WUU_REAL_MODEL_E2E=1 to call the configured model")
	}

	root, err := filepath.Abs(filepath.Join("..", ".."))
	if err != nil {
		t.Fatal(err)
	}
	home := os.Getenv("HOME")
	cfg, configPath, err := config.LoadFrom(root, home)
	if err != nil {
		t.Fatalf("load config: %v", err)
	}

	rt, err := runtime.NewSession(runtime.Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: configPath,
		Config:     cfg,
		NoTools:    true,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer rt.Cleanup()

	// Force the production runner through the compact path without spending a
	// real context-window-sized request. The model call is still real.
	rt.StreamRunner.ContextWindowOverride = 1000
	rt.StreamRunner.DisableAutoCompact = false

	history := []providers.ChatMessage{
		{Role: "system", Content: "For this integration test, reply with exactly WUU_COMPACT_OK and no other text."},
		{Role: "user", Content: "Continue this single-turn tool run. The final answer must be exactly WUU_COMPACT_OK."},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_1", Name: "run_shell", Arguments: `{"command":"rg ContextOverflow"}`},
		}},
		{Role: "tool", Name: "run_shell", ToolCallID: "call_1", Content: strings.Repeat("large output ", 1200)},
		{Role: "assistant", Content: "I found the first clue."},
		{Role: "assistant", ToolCalls: []providers.ToolCall{
			{ID: "call_2", Name: "run_shell", Arguments: `{"command":"sed -n 1,220p internal/agent/loop.go"}`},
		}},
		{Role: "tool", Name: "run_shell", ToolCallID: "call_2", Content: strings.Repeat("large output ", 1200)},
		{Role: "assistant", Content: "I am ready to provide the final answer."},
	}

	var compactSeen bool
	ctx, cancel := context.WithTimeout(context.Background(), 90*time.Second)
	defer cancel()

	res, err := rt.StreamRunner.RunWithCallback(ctx, history, func(ev providers.StreamEvent) {
		if ev.Type == providers.EventCompact {
			compactSeen = true
		}
	})
	if err != nil {
		t.Fatalf("RunWithCallback: %v", err)
	}
	if !compactSeen || !res.HistoryRewritten {
		t.Fatalf("expected compacted run, compactSeen=%v historyRewritten=%v", compactSeen, res.HistoryRewritten)
	}
	if !strings.Contains(res.Content, "WUU_COMPACT_OK") {
		t.Fatalf("expected WUU_COMPACT_OK, got %q", res.Content)
	}
}
