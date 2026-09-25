package agentcontrol

import (
	"context"
	"encoding/json"
	"fmt"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/providers"
)

// pinRecordingClient records every ChatRequest it sees. Multiple instances
// stand in for distinct stream clients (e.g. default worker provider vs the
// provider named in a participant pin).
type pinRecordingClient struct {
	id      string
	mu      sync.Mutex
	got     bool
	request providers.ChatRequest
}

func (c *pinRecordingClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	c.mu.Lock()
	defer c.mu.Unlock()
	c.request = req
	c.got = true
	return providers.ChatResponse{Content: "ok from " + c.id}, nil
}

func (c *pinRecordingClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	resp, err := c.Chat(context.Background(), req)
	if err != nil {
		return nil, err
	}
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: resp.Content}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func (c *pinRecordingClient) LastRequest() (providers.ChatRequest, bool) {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.request, c.got
}

func (c *pinRecordingClient) WaitRequest(timeout time.Duration) (providers.ChatRequest, bool) {
	deadline := time.Now().Add(timeout)
	for time.Now().Before(deadline) {
		if req, ok := c.LastRequest(); ok {
			return req, true
		}
		time.Sleep(5 * time.Millisecond)
	}
	return c.LastRequest()
}

// pinAgentControl builds a minimal AgentControl with the requested default
// worker client. Tests that exercise queued-spawn restore install a model-pin
// resolver via SetModelPinClientResolver before triggering maybeStartQueued.
func pinAgentControl(t *testing.T, dir string, defaultClient providers.StreamClient) *AgentControl {
	t.Helper()
	harnessDir := filepath.Join(dir, "harness")
	threadDir := filepath.Join(dir, "threads")
	historyDir := filepath.Join(dir, "history")
	c, err := New(Config{
		Client:       defaultClient,
		DefaultModel: "default-worker-model",
		ParentRepo:   dir,
		WorktreeRoot: filepath.Join(dir, ".wuu", "worktrees"),
		SessionID:    "sess-queued-pin",
		HistoryDir:   historyDir,
		ThreadDir:    threadDir,
		HarnessDir:   harnessDir,
		WorkerFactory: func(string, WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return fakeToolkit{}, nil
		},
	})
	if err != nil {
		t.Fatalf("agentcontrol.New: %v", err)
	}
	t.Cleanup(func() { stopAndCloseAgentControlForTest(t, c) })
	return c
}

func stopAndCloseAgentControlForTest(t *testing.T, control *AgentControl) {
	t.Helper()
	if control == nil {
		return
	}
	control.BeginShutdown()
	control.StopAll()
	control.YieldWorkerTerminalFinalizations()
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	for _, snap := range control.Manager().List() {
		if _, err := control.Manager().Wait(ctx, snap.ID); err != nil {
			t.Errorf("wait for worker %s during cleanup: %v", snap.ID, err)
		}
	}
	deadline := time.Now().Add(5 * time.Second)
	for control.HasOwnedWorkerExecutions() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if control.HasOwnedWorkerExecutions() {
		t.Errorf("worker execution leases remain owned during cleanup: %d", control.OwnedWorkerExecutionCount())
	}
	control.Close()
}

// pinThreadMeta builds a queued-spawn thread metadata blob for the given
// worker id, matching what registerChildThreadWithStatus would normally
// persist before a queue write.
func pinThreadMeta(workerID, taskName string) agentthread.Metadata {
	now := time.Now().UTC()
	return agentthread.Metadata{
		ID:        workerID,
		TaskName:  taskName,
		Role:      DefaultSubagentType,
		ParentID:  "root",
		Path:      agentthread.RootPath + "/" + workerID,
		CreatedAt: now,
		UpdatedAt: now,
		Status:    agentthread.StatusPending,
		Source:    agentthread.Source{Kind: agentthread.SourceThreadSpawn, ParentPath: agentthread.RootPath},
	}
}

func writeQueuedSpawnPayload(t *testing.T, harnessDir, workerID, modelPin, modelOverride string) {
	t.Helper()
	now := time.Now().UTC()
	payload := queuedSpawnPayload{
		WorkerID:      workerID,
		ParticipantID: "participant-andy",
		WorkerType:    DefaultSubagentType,
		ThreadMeta:    pinThreadMeta(workerID, "andy-task"),
		Prompt:        "do the thing",
		Isolation:     string(IsolationInplace),
		ModelOverride: modelOverride,
		ModelPin:      modelPin,
	}
	raw, err := json.Marshal(payload)
	if err != nil {
		t.Fatalf("marshal queued payload: %v", err)
	}
	store := harness.NewStore(harnessDir)
	if err := store.UpsertQueueItem(harness.QueueItem{
		ID:        workerID,
		TaskID:    workerID,
		Kind:      "agent_spawn",
		Payload:   raw,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("UpsertQueueItem: %v", err)
	}
}

// TestQueuedSpawnRestore_CrossProviderPinRebuildsClient pins a queued spawn
// to a provider that differs from the current worker default. After restore,
// the worker must run on the rebuilt client (not the worker default client)
// and must not silently issue a request against the wrong provider.
func TestQueuedSpawnRestore_CrossProviderPinRebuildsClient(t *testing.T) {
	dir := t.TempDir()
	defaultClient := &pinRecordingClient{id: "default"}
	overrideClient := &pinRecordingClient{id: "override"}

	c := pinAgentControl(t, dir, defaultClient)
	c.SetWorkerProviderName("default-provider")

	resolveCalls := 0
	c.SetModelPinClientResolver(func(rawPin string) (string, providers.StreamClient, error) {
		resolveCalls++
		// Mirror appserver's parseParticipantModelPin behavior.
		idx := strings.Index(rawPin, ":")
		if idx < 0 {
			return strings.TrimSpace(rawPin), nil, nil
		}
		provider := strings.TrimSpace(rawPin[:idx])
		model := strings.TrimSpace(rawPin[idx+1:])
		if provider == "default-provider" {
			return model, nil, nil
		}
		if provider != "alt-provider" {
			return "", nil, fmt.Errorf("unexpected provider %q", provider)
		}
		return model, overrideClient, nil
	})

	writeQueuedSpawnPayload(t, c.HarnessStore().Dir(), "worker_andy_cross", "alt-provider:pinned-model", "pinned-model")

	// Re-run restore to pick up the queue item that was written after New.
	if err := c.restoreQueuedSpawns(); err != nil {
		t.Fatalf("restoreQueuedSpawns: %v", err)
	}
	// Drain only after the resolver has been installed.
	c.StartQueuedWork()
	c.maybeStartQueued(context.Background())

	if resolveCalls != 1 {
		t.Fatalf("expected exactly one resolver call, got %d", resolveCalls)
	}

	// The override client must have been hit; the default client must not.
	if req, ok := overrideClient.WaitRequest(time.Second); !ok {
		t.Fatalf("override client never received a chat request")
	} else if req.Model != "pinned-model" {
		t.Fatalf("override client got model %q, want %q", req.Model, "pinned-model")
	}
	if _, ok := defaultClient.LastRequest(); ok {
		t.Fatalf("default client should not have received a request when cross-provider pin resolves")
	}
}

// TestQueuedSpawnRestore_BareModelPinKeepsModelOverride ensures that a
// participant pin without a colon (same provider) still overrides the model
// after restart, without requiring a fresh client.
func TestQueuedSpawnRestore_BareModelPinKeepsModelOverride(t *testing.T) {
	dir := t.TempDir()
	defaultClient := &pinRecordingClient{id: "default"}

	c := pinAgentControl(t, dir, defaultClient)
	c.SetWorkerProviderName("default-provider")

	resolveCalls := 0
	c.SetModelPinClientResolver(func(rawPin string) (string, providers.StreamClient, error) {
		resolveCalls++
		idx := strings.Index(rawPin, ":")
		if idx < 0 {
			return strings.TrimSpace(rawPin), nil, nil
		}
		return strings.TrimSpace(rawPin[idx+1:]), nil, nil
	})

	writeQueuedSpawnPayload(t, c.HarnessStore().Dir(), "worker_andy_bare", "bare-pinned-model", "bare-pinned-model")

	if err := c.restoreQueuedSpawns(); err != nil {
		t.Fatalf("restoreQueuedSpawns: %v", err)
	}
	c.StartQueuedWork()
	c.maybeStartQueued(context.Background())

	if resolveCalls != 1 {
		t.Fatalf("expected exactly one resolver call, got %d", resolveCalls)
	}
	if req, ok := defaultClient.WaitRequest(time.Second); !ok {
		t.Fatalf("default client never received a chat request")
	} else if req.Model != "bare-pinned-model" {
		t.Fatalf("default client got model %q, want %q", req.Model, "bare-pinned-model")
	}
}

// TestQueuedSpawnRestore_ResolverFailureFailsExplicitly asserts that a
// cross-provider pin whose resolver fails (e.g. unconfigured provider) makes
// the queued spawn fail with a clear error, and the default client never
// receives a request for that run.
func TestQueuedSpawnRestore_ResolverFailureFailsExplicitly(t *testing.T) {
	dir := t.TempDir()
	defaultClient := &pinRecordingClient{id: "default"}

	c := pinAgentControl(t, dir, defaultClient)
	c.SetWorkerProviderName("default-provider")

	c.SetModelPinClientResolver(func(rawPin string) (string, providers.StreamClient, error) {
		return "", nil, fmt.Errorf("participant pin %q: provider missing in config", rawPin)
	})

	writeQueuedSpawnPayload(t, c.HarnessStore().Dir(), "worker_andy_missing", "missing-provider:some-model", "some-model")

	if err := c.restoreQueuedSpawns(); err != nil {
		t.Fatalf("restoreQueuedSpawns: %v", err)
	}
	c.StartQueuedWork()
	c.maybeStartQueued(context.Background())

	if req, ok := defaultClient.LastRequest(); ok {
		t.Fatalf("default client must not receive a request when resolver fails; got model=%q", req.Model)
	}

	// The worker thread must be marked failed in the in-memory registry so
	// callers querying the registry see the failure.
	thr, ok := c.Threads().Resolve("worker_andy_missing")
	if !ok {
		t.Fatalf("expected thread worker_andy_missing to be registered")
	}
	if thr.Status != agentthread.StatusFailed {
		t.Fatalf("worker_andy_missing thread status = %q, want failed", thr.Status)
	}

	// Harness task must be marked failed with the resolver error in the message.
	tasks, err := c.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	var task *harness.Task
	for i := range tasks {
		if tasks[i].ID == "worker_andy_missing" {
			task = &tasks[i]
		}
	}
	if task == nil {
		t.Fatalf("expected harness task for worker_andy_missing, got %+v", tasks)
	}
	if task.Status != harness.TaskStatusFailed {
		t.Fatalf("harness task status = %q, want failed", task.Status)
	}
	if !strings.Contains(task.Error, "missing-provider") {
		t.Fatalf("harness task error should mention missing-provider; got %q", task.Error)
	}
}

// TestQueuedSpawnRestore_NoResolverStillValidatesCrossProvider guards the
// spec rule that the runtime must never silently fall back to the default
// client when a cross-provider pin is persisted but no resolver is wired up:
// the queued spawn must fail with an explicit error.
func TestQueuedSpawnRestore_NoResolverStillValidatesCrossProvider(t *testing.T) {
	dir := t.TempDir()
	defaultClient := &pinRecordingClient{id: "default"}

	c := pinAgentControl(t, dir, defaultClient)
	c.SetWorkerProviderName("default-provider")

	// No SetModelPinClientResolver call. Cross-provider pin must not
	// silently use the worker default.
	writeQueuedSpawnPayload(t, c.HarnessStore().Dir(), "worker_andy_orphaned", "alt-provider:pinned-model", "pinned-model")

	if err := c.restoreQueuedSpawns(); err != nil {
		t.Fatalf("restoreQueuedSpawns: %v", err)
	}
	c.StartQueuedWork()
	c.maybeStartQueued(context.Background())

	if _, ok := defaultClient.LastRequest(); ok {
		t.Fatalf("default client must not receive a request for orphaned cross-provider pin")
	}

	tasks, err := c.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	var task *harness.Task
	for i := range tasks {
		if tasks[i].ID == "worker_andy_orphaned" {
			task = &tasks[i]
		}
	}
	if task == nil {
		t.Fatalf("expected harness task for worker_andy_orphaned, got %+v", tasks)
	}
	if task.Status != harness.TaskStatusFailed {
		t.Fatalf("harness task status = %q, want failed", task.Status)
	}
	if !strings.Contains(task.Error, "alt-provider") || !strings.Contains(task.Error, "pinned-model") {
		t.Fatalf("error should mention the orphan pin provider and model; got %q", task.Error)
	}
}

// TestQueuedSpawnRestore_NoResolverBareModelPinStillWorks ensures legacy
// payloads persisted without a ModelPin still restore correctly — same as
// the pre-fix behavior.
func TestQueuedSpawnRestore_NoResolverBareModelPinStillWorks(t *testing.T) {
	dir := t.TempDir()
	defaultClient := &pinRecordingClient{id: "default"}

	c := pinAgentControl(t, dir, defaultClient)
	c.SetWorkerProviderName("default-provider")

	// No resolver; only ModelOverride is persisted.
	writeQueuedSpawnPayload(t, c.HarnessStore().Dir(), "worker_andy_legacy", "", "legacy-pinned-model")

	if err := c.restoreQueuedSpawns(); err != nil {
		t.Fatalf("restoreQueuedSpawns: %v", err)
	}
	c.StartQueuedWork()
	c.maybeStartQueued(context.Background())

	if req, ok := defaultClient.WaitRequest(time.Second); !ok {
		t.Fatalf("default client never received a chat request")
	} else if req.Model != "legacy-pinned-model" {
		t.Fatalf("default client got model %q, want %q", req.Model, "legacy-pinned-model")
	}
}

// TestQueuedSpawnRestore_ResolverReturnsNilClientForCrossProvider guards
// against a misbehaving resolver that returns (model, nil, nil) when a
// different provider is needed: the queued spawn must fail rather than
// silently fall back to the default client.
func TestQueuedSpawnRestore_ResolverReturnsNilClientForCrossProvider(t *testing.T) {
	dir := t.TempDir()
	defaultClient := &pinRecordingClient{id: "default"}

	c := pinAgentControl(t, dir, defaultClient)
	c.SetWorkerProviderName("default-provider")

	c.SetModelPinClientResolver(func(rawPin string) (string, providers.StreamClient, error) {
		return "pinned-model", nil, nil // bug: claims cross-provider but returns no client
	})

	writeQueuedSpawnPayload(t, c.HarnessStore().Dir(), "worker_andy_buggy", "alt-provider:pinned-model", "pinned-model")

	if err := c.restoreQueuedSpawns(); err != nil {
		t.Fatalf("restoreQueuedSpawns: %v", err)
	}
	c.StartQueuedWork()
	c.maybeStartQueued(context.Background())

	if _, ok := defaultClient.LastRequest(); ok {
		t.Fatalf("default client must not receive a request when resolver returns nil client for cross-provider pin")
	}

	tasks, err := c.HarnessStore().ListTasks()
	if err != nil {
		t.Fatalf("ListTasks: %v", err)
	}
	for _, task := range tasks {
		if task.ID == "worker_andy_buggy" && task.Status != harness.TaskStatusFailed {
			t.Fatalf("expected worker_andy_buggy failed, got %q (err=%q)", task.Status, task.Error)
		}
	}
}
