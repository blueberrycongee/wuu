package appserver

import (
	"context"
	"encoding/json"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentengine"
	"github.com/blueberrycongee/wuu/internal/claudeengine"
)

func TestClaudeBackgroundContinuationPersistsOneCompletedTurn(t *testing.T) {
	binary := filepath.Join(t.TempDir(), "fakeclaude")
	build := exec.Command("go", "build", "-o", binary, "../claudeengine/testdata/fakeclaude")
	if output, err := build.CombinedOutput(); err != nil {
		t.Fatalf("build fixture: %v\n%s", err, output)
	}
	rt := newTestRuntime(t, &fakeClient{})
	registry := agentengine.NewRegistry()
	if err := registry.Register(claudeengine.NewEngine(binary, rt.RootDir)); err != nil {
		t.Fatal(err)
	}
	rt.SetEnginesForTest(registry)
	out := &lockedBuffer{}
	srv := New(rt, out)
	t.Cleanup(srv.Close)
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	if err := srv.handleLine(ctx, []byte(`{"id":"start","method":"thread/start","params":{"engine":"claude"}}`)); err != nil {
		t.Fatal(err)
	}
	response := responseByID(t, parseOutput(t, out.String()), "start")
	if response["error"] != nil {
		t.Fatalf("thread/start: %+v", response)
	}
	threadID := remarshal[ThreadStartResult](t, response["result"]).Thread.ID
	request, err := json.Marshal(map[string]any{"id": "run", "method": "turn/start", "params": TurnStartParams{ThreadID: threadID, Prompt: "background_parallel"}})
	if err != nil {
		t.Fatal(err)
	}
	if err := srv.handleLine(ctx, request); err != nil {
		t.Fatal(err)
	}
	messages := waitForTurnCompletedForThread(t, out, threadID)
	completed := remarshal[TurnCompletedNotification](t, notificationByMethod(t, messages, NotificationTurnCompleted)["params"])
	want := "Launched.\n\nFirst summary.\n\nFinal summary."
	if completed.Content != want || completed.Turn.Status != TurnStatusCompleted {
		t.Fatalf("premature or incorrect completion: %+v", completed)
	}
	terminals := 0
	for _, message := range messages {
		if message["method"] == NotificationTurnCompleted {
			terminals++
		}
	}
	if terminals != 1 {
		t.Fatalf("completed notifications = %d, want one", terminals)
	}
	history, err := loadChatMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	found := false
	for _, message := range history {
		if message.Role == "assistant" && message.Content == want {
			found = true
		}
		if strings.Contains(message.Content, "CHILD private output") {
			t.Fatal("child output leaked into durable parent history")
		}
	}
	if !found {
		t.Fatalf("final summary absent from durable history: %+v", history)
	}
	metas, err := loadMetaMessages(rt.SessionDir, threadID)
	if err != nil {
		t.Fatal(err)
	}
	usageRecords := 0
	for _, meta := range metas {
		if meta.Content != "token_usage" {
			continue
		}
		usageRecords++
		if meta.InputTokens != 270 || meta.OutputTokens != 90 || meta.CacheCreationTokens != 30 || meta.CacheReadTokens != 180 {
			t.Fatalf("incomplete durable continuation usage: %+v", meta)
		}
	}
	if usageRecords != 1 {
		t.Fatalf("usage records = %d, want one combined turn record", usageRecords)
	}
}

func TestCodexEngineModelCatalogCacheUsesFreshMatchingBinary(t *testing.T) {
	now := time.Date(2026, time.September, 2, 12, 0, 0, 0, time.UTC)
	entry := &engineModelCatalogCacheEntry{
		binaryPath: "/usr/local/bin/codex",
		models: []EngineModelInfo{{
			ID:               "gpt-test",
			SupportedEfforts: []string{"low", "high"},
		}},
		expiresAt: now.Add(engineModelCatalogTTL),
	}

	models, ok := entry.load("/usr/local/bin/codex", now.Add(time.Minute))
	if !ok || len(models) != 1 || models[0].ID != "gpt-test" {
		t.Fatalf("fresh matching cache = (%+v, %v), want cached model", models, ok)
	}
	models[0].SupportedEfforts[0] = "changed"
	if entry.models[0].SupportedEfforts[0] != "low" {
		t.Fatal("cache returned mutable model effort storage")
	}

	if _, ok := entry.load("/opt/codex", now.Add(time.Minute)); ok {
		t.Fatal("cache matched a different binary path")
	}
	if _, ok := entry.load("/usr/local/bin/codex", entry.expiresAt); ok {
		t.Fatal("cache remained fresh at its expiration boundary")
	}
}
