package agentcontrol

import (
	"context"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

type aliasRecordingClient struct {
	mu       sync.Mutex
	requests []providers.ChatRequest
}

func (c *aliasRecordingClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	c.record(req)
	return providers.ChatResponse{Content: "done"}, nil
}

func (c *aliasRecordingClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.record(req)
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "done"}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func (c *aliasRecordingClient) record(req providers.ChatRequest) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.requests = append(c.requests, req)
}

func (c *aliasRecordingClient) lastRequest(t *testing.T) providers.ChatRequest {
	t.Helper()
	c.mu.Lock()
	defer c.mu.Unlock()
	if len(c.requests) == 0 {
		t.Fatal("alias client received no requests")
	}
	return c.requests[len(c.requests)-1]
}

func testAgentControl(t *testing.T) *AgentControl {
	t.Helper()
	dir := t.TempDir()
	c, err := New(Config{
		Client:        &fakeClient{resp: providers.ChatResponse{Content: "done"}},
		DefaultModel:  "default-model",
		ProviderName:  "default-provider",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		HistoryDir:    filepath.Join(dir, "history"),
		ThreadDir:     filepath.Join(dir, "threads"),
		HarnessDir:    filepath.Join(dir, "harness"),
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	return c
}

func TestSpawnValidModelAliasUsesResolvedRuntime(t *testing.T) {
	c := testAgentControl(t)
	defer c.Close()

	aliasClient := &aliasRecordingClient{}
	c.SetModelAliasResolver(func(alias string) AliasResolutionResult {
		if alias != "cheap" {
			return AliasResolutionResult{Unknown: true}
		}
		return AliasResolutionResult{
			Found: true,
			Runtime: subagent.WorkerRuntime{
				Provider:                "alias-provider",
				Model:                   "alias-model",
				APIModel:                "api-model",
				Effort:                  "low",
				Variant:                 "thinking",
				ProviderOptions:         map[string]any{"opt": 1},
				Temperature:             0.5,
				ContextWindow:           1000,
				MaxInputTokens:          900,
				OutputReserveTokens:     100,
				CompactThresholdTokens:  800,
				CompactThresholdPct:     0.8,
				CompactKeepRecentTokens: 200,
				DisableAutoCompact:      true,
				Client:                  aliasClient,
			},
		}
	})

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:       "worker",
		TaskName:   "valid_alias",
		Prompt:     "hello",
		Isolation:  "inplace",
		ModelAlias: "cheap",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if res.Status != "running" && res.Status != "completed" {
		t.Fatalf("expected running or completed, got %s", res.Status)
	}
	if res.ModelAlias != "cheap" {
		t.Fatalf("expected model_alias cheap, got %q", res.ModelAlias)
	}
	if res.ModelAliasFallback {
		t.Fatal("expected no fallback for valid alias")
	}
	if res.ResolvedProvider != "alias-provider" || res.ResolvedModel != "alias-model" || res.ResolvedAPIModel != "api-model" {
		t.Fatalf("unexpected resolved provenance: provider=%q model=%q api=%q", res.ResolvedProvider, res.ResolvedModel, res.ResolvedAPIModel)
	}

	snap := c.manager.Get(res.AgentID).Snapshot()
	if snap.ResolvedProvider != "alias-provider" || snap.ResolvedModel != "alias-model" || snap.ResolvedAPIModel != "api-model" {
		t.Fatalf("snapshot provenance mismatch: provider=%q model=%q api=%q", snap.ResolvedProvider, snap.ResolvedModel, snap.ResolvedAPIModel)
	}

	// Wait for the run to complete and verify history persistence.
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	if _, err := c.manager.Wait(ctx, res.AgentID); err != nil {
		t.Fatalf("Wait: %v", err)
	}
	request := aliasClient.lastRequest(t)
	if request.Model != "api-model" || request.Effort != "low" || request.ProviderOptions["opt"] != 1 {
		t.Fatalf("alias request did not use the resolved API runtime: %+v", request)
	}

	run, err := subagent.LoadPersistedRun(filepath.Join(c.historyDir, res.AgentID+".json"))
	if err != nil {
		t.Fatalf("LoadPersistedRun: %v", err)
	}
	if run.ModelAlias != "cheap" {
		t.Fatalf("persisted model_alias = %q, want cheap", run.ModelAlias)
	}
	if run.Runtime == nil {
		t.Fatal("expected persisted runtime")
	}
	if run.Runtime.Provider != "alias-provider" || run.Runtime.Model != "alias-model" || run.Runtime.APIModel != "api-model" {
		t.Fatalf("persisted runtime mismatch: provider=%q model=%q api=%q", run.Runtime.Provider, run.Runtime.Model, run.Runtime.APIModel)
	}
}

func TestSpawnUnknownModelAliasFallsBackAndSnapshotsDefaults(t *testing.T) {
	c := testAgentControl(t)
	defer c.Close()

	c.SetModelAliasResolver(func(alias string) AliasResolutionResult {
		return AliasResolutionResult{Unknown: true, ValidAliases: []string{"cheap", "frontend"}}
	})

	res, err := c.Spawn(context.Background(), SpawnRequest{
		Type:       "worker",
		TaskName:   "unknown_alias",
		Prompt:     "hello",
		Isolation:  "inplace",
		ModelAlias: "missing",
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if !res.ModelAliasFallback {
		t.Fatal("expected fallback flag for unknown alias")
	}
	if len(res.ValidAliases) != 2 || res.ValidAliases[0] != "cheap" || res.ValidAliases[1] != "frontend" {
		t.Fatalf("unexpected valid aliases: %v", res.ValidAliases)
	}
	if res.ResolvedProvider != "default-provider" || res.ResolvedModel != "default-model" {
		t.Fatalf("unexpected fallback provenance: provider=%q model=%q", res.ResolvedProvider, res.ResolvedModel)
	}

	snap := c.manager.Get(res.AgentID).Snapshot()
	if !snap.ModelAliasFallback {
		t.Fatalf("snapshot should preserve fallback flag")
	}
	if snap.ResolvedProvider != "default-provider" || snap.ResolvedModel != "default-model" {
		t.Fatalf("snapshot fallback provenance mismatch: provider=%q model=%q", snap.ResolvedProvider, snap.ResolvedModel)
	}
}

func TestForkWithModelAlias(t *testing.T) {
	c := testAgentControl(t)
	defer c.Close()

	aliasClient := &fakeClient{resp: providers.ChatResponse{Content: "fork done"}}
	c.SetModelAliasResolver(func(alias string) AliasResolutionResult {
		if alias != "review" {
			return AliasResolutionResult{Unknown: true}
		}
		return AliasResolutionResult{
			Found: true,
			Runtime: subagent.WorkerRuntime{
				Provider: "review-provider",
				Model:    "review-model",
				Client:   aliasClient,
			},
		}
	})

	res, err := c.Fork(context.Background(), ForkRequest{
		TaskName:   "fork_alias",
		Prompt:     "continue",
		Isolation:  "inplace",
		ModelAlias: "review",
	}, []providers.ChatMessage{
		{Role: "system", Content: "sys"},
		{Role: "user", Content: "parent"},
	})
	if err != nil {
		t.Fatalf("Fork: %v", err)
	}
	if res.ResolvedProvider != "review-provider" || res.ResolvedModel != "review-model" {
		t.Fatalf("fork provenance mismatch: provider=%q model=%q", res.ResolvedProvider, res.ResolvedModel)
	}
}

func TestModelAliasResolverErrorFailsSpawn(t *testing.T) {
	c := testAgentControl(t)
	defer c.Close()

	c.SetModelAliasResolver(func(alias string) AliasResolutionResult {
		return AliasResolutionResult{Found: true, Err: context.DeadlineExceeded}
	})

	_, err := c.Spawn(context.Background(), SpawnRequest{
		Type:       "worker",
		TaskName:   "broken_alias",
		Prompt:     "hello",
		Isolation:  "inplace",
		ModelAlias: "bad",
	})
	if err == nil || !strings.Contains(err.Error(), "resolve model alias") {
		t.Fatalf("expected alias resolution error, got %v", err)
	}
}

func TestModelAliasResolverRejectsIncompleteRuntime(t *testing.T) {
	c := testAgentControl(t)
	defer c.Close()
	c.SetModelAliasResolver(func(string) AliasResolutionResult {
		return AliasResolutionResult{Found: true, Runtime: subagent.WorkerRuntime{Provider: "provider", Model: "model"}}
	})
	if _, _, _, err := c.resolveModelAlias("incomplete"); err == nil || !strings.Contains(err.Error(), "nil client") {
		t.Fatalf("incomplete runtime error = %v", err)
	}
}

func TestQueuedModelAliasResolvesAgainAtLaunch(t *testing.T) {
	dir := t.TempDir()
	blocker := &blockingStreamClient{started: make(chan struct{}), release: make(chan struct{})}
	c, err := New(Config{
		Client:        blocker,
		ProviderName:  "default-provider",
		DefaultModel:  "default-model",
		ParentRepo:    dir,
		WorktreeRoot:  filepath.Join(dir, "wt"),
		HistoryDir:    filepath.Join(dir, "history"),
		ThreadDir:     filepath.Join(dir, "threads"),
		HarnessDir:    filepath.Join(dir, "harness"),
		MaxParallel:   1,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
	})
	if err != nil {
		t.Fatalf("New: %v", err)
	}
	defer c.Close()
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	oldClient := &aliasRecordingClient{}
	newClient := &aliasRecordingClient{}
	var runtimeMu sync.Mutex
	current := subagent.WorkerRuntime{Provider: "old-provider", Model: "old-model", APIModel: "old-api-model", Client: oldClient}
	c.SetModelAliasResolver(func(string) AliasResolutionResult {
		runtimeMu.Lock()
		defer runtimeMu.Unlock()
		return AliasResolutionResult{Found: true, Runtime: current.Clone()}
	})
	c.StartQueuedWork()

	occupier, err := c.Spawn(context.Background(), SpawnRequest{Type: "worker", TaskName: "occupier", Prompt: "block", Isolation: "inplace"})
	if err != nil {
		t.Fatalf("spawn occupier: %v", err)
	}
	select {
	case <-blocker.started:
	case <-time.After(5 * time.Second):
		t.Fatal("occupier did not start")
	}
	queued, err := c.Spawn(context.Background(), SpawnRequest{Type: "worker", TaskName: "queued_alias", Prompt: "queued", Isolation: "inplace", ModelAlias: "frontend"})
	if err != nil {
		t.Fatalf("queue alias: %v", err)
	}
	if queued.Status != "queued" || queued.ResolvedProvider != "old-provider" {
		t.Fatalf("queued admission provenance = %+v", queued)
	}
	runtimeMu.Lock()
	current = subagent.WorkerRuntime{Provider: "new-provider", Model: "new-model", APIModel: "new-api-model", Client: newClient}
	runtimeMu.Unlock()
	close(blocker.release)
	waitForSubAgentStatus(t, c.manager, occupier.AgentID, subagent.StatusCompleted, 5*time.Second)
	c.maybeStartQueued(context.Background())
	deadline := time.Now().Add(5 * time.Second)
	var queuedAgent *subagent.SubAgent
	for time.Now().Before(deadline) {
		queuedAgent = c.manager.Get(queued.AgentID)
		if queuedAgent != nil && queuedAgent.Snapshot().Status == subagent.StatusCompleted {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	if queuedAgent == nil || queuedAgent.Snapshot().Status != subagent.StatusCompleted {
		tasks, _ := c.HarnessStore().ListTasks()
		c.queueMu.Lock()
		queuedCount := len(c.queued)
		c.queueMu.Unlock()
		t.Fatalf("queued alias did not launch: agent=%v queued_count=%d tasks=%+v", queuedAgent, queuedCount, tasks)
	}
	snap := queuedAgent.Snapshot()
	if snap.ResolvedProvider != "new-provider" || snap.ResolvedModel != "new-model" || snap.ResolvedAPIModel != "new-api-model" {
		t.Fatalf("queued launch runtime = %+v", snap)
	}
	if got := newClient.lastRequest(t).Model; got != "new-api-model" {
		t.Fatalf("queued request model = %q, want new-api-model", got)
	}
	oldClient.mu.Lock()
	oldCalls := len(oldClient.requests)
	oldClient.mu.Unlock()
	if oldCalls != 0 {
		t.Fatalf("queued worker used admission-time alias client %d times", oldCalls)
	}
}

func TestModelAliasRuntimeSurvivesRestartResume(t *testing.T) {
	dir := t.TempDir()
	initRepo(t, dir)
	historyDir := filepath.Join(dir, "state", "workers")
	threadDir := filepath.Join(dir, "state", "threads")
	harnessDir := filepath.Join(dir, "state", "harness")
	newControl := func(client providers.StreamClient) *AgentControl {
		c, err := New(Config{
			Client:        client,
			ProviderName:  "changed-default-provider",
			DefaultModel:  "changed-default-model",
			ParentRepo:    dir,
			WorktreeRoot:  filepath.Join(dir, ".wuu", "worktrees"),
			HistoryDir:    historyDir,
			ThreadDir:     threadDir,
			HarnessDir:    harnessDir,
			SessionID:     "sess-alias-restart",
			WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) { return fakeToolkit{}, nil },
		})
		if err != nil {
			t.Fatalf("New: %v", err)
		}
		return c
	}

	firstAliasClient := &aliasRecordingClient{}
	first := newControl(&fakeClient{resp: providers.ChatResponse{Content: "default"}})
	first.SetModelAliasResolver(func(string) AliasResolutionResult {
		return AliasResolutionResult{Found: true, Runtime: subagent.WorkerRuntime{
			Provider: "frozen-provider", Model: "frozen-model", APIModel: "frozen-api-model",
			Effort: "high", Variant: "thinking", ProviderOptions: map[string]any{"reasoningEffort": "high"}, Client: firstAliasClient,
		}}
	})
	spawned, err := first.Spawn(context.Background(), SpawnRequest{
		Type: DefaultSubagentType, TaskName: "alias_restart", Prompt: "first", Isolation: "inplace", ModelAlias: "frontend", Synchronous: true,
	})
	if err != nil {
		t.Fatalf("first spawn: %v", err)
	}
	first.Close()

	resumedClient := &aliasRecordingClient{}
	second := newControl(&fakeClient{resp: providers.ChatResponse{Content: "new default"}})
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, second) })
	second.SetModelAliasResolver(func(string) AliasResolutionResult {
		t.Fatal("resume must not re-resolve the alias against current settings")
		return AliasResolutionResult{}
	})
	second.SetProviderClientResolver(func(provider string) (providers.StreamClient, error) {
		if provider != "frozen-provider" {
			t.Fatalf("resume provider = %q, want frozen-provider", provider)
		}
		return resumedClient, nil
	})
	if _, err := second.FollowupTask(context.Background(), spawned.AgentID, "second"); err != nil {
		t.Fatalf("FollowupTask: %v", err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	final, err := second.Manager().Wait(ctx, spawned.AgentID)
	if err != nil {
		t.Fatalf("Wait resumed: %v", err)
	}
	if final.ResolvedProvider != "frozen-provider" || final.ResolvedModel != "frozen-model" || final.ResolvedAPIModel != "frozen-api-model" || final.ResolvedVariant != "thinking" {
		t.Fatalf("resumed provenance drifted: %+v", final)
	}
	request := resumedClient.lastRequest(t)
	if request.Model != "frozen-api-model" || request.Effort != "high" || request.ProviderOptions["reasoningEffort"] != "high" {
		t.Fatalf("resumed request runtime drifted: %+v", request)
	}
}
