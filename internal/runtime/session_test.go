package runtime

import (
	"bytes"
	"context"
	"crypto/sha256"
	"encoding/json"
	"errors"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/activity"
	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/capability"
	"github.com/blueberrycongee/wuu/internal/config"
	wuucontext "github.com/blueberrycongee/wuu/internal/context"
	"github.com/blueberrycongee/wuu/internal/extensions"
	"github.com/blueberrycongee/wuu/internal/harness"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/modelbudget"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/process"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func TestSessionMaxParallelDefaultsAndOverrides(t *testing.T) {
	session := &Session{maxParallel: 3}
	if session.MaxParallel() != 3 {
		t.Fatalf("MaxParallel = %d, want 3", session.MaxParallel())
	}

	var zero Session
	if zero.MaxParallel() != config.DefaultAgentMaxParallel {
		t.Fatalf("zero-value MaxParallel = %d, want %d", zero.MaxParallel(), config.DefaultAgentMaxParallel)
	}
}

func TestThreadProcessManagerSharesRuntimeHostGeneration(t *testing.T) {
	workspaceRoot := t.TempDir()
	workspaceManager, err := process.NewManager(workspaceRoot, filepath.Join(t.TempDir(), "workspace-runtime"))
	if err != nil {
		t.Fatalf("NewManager: %v", err)
	}
	s := &Session{RootDir: workspaceRoot, ProcessManager: workspaceManager}

	threadManager, err := s.processManagerForThread(t.TempDir(), t.TempDir())
	if err != nil {
		t.Fatalf("processManagerForThread: %v", err)
	}
	if threadManager == workspaceManager {
		t.Fatal("a different thread root should receive a thread-local manager")
	}
	if threadManager.HostGenerationID() != workspaceManager.HostGenerationID() {
		t.Fatalf("thread host generation = %q, want workspace generation %q", threadManager.HostGenerationID(), workspaceManager.HostGenerationID())
	}
}

func TestSessionCleanupClosesMCPManager(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	manager := mcp.NewManager()
	manager.Configure(map[string]mcp.ServerConfig{
		"docs": {Name: "docs", Command: "unused"},
	})
	kit.SetMCPManager(manager)
	session := &Session{Toolkit: kit}

	if _, err := session.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if err := manager.Connect(context.Background(), "docs"); !errors.Is(err, mcp.ErrManagerClosed) {
		t.Fatalf("Connect after session cleanup error = %v, want ErrManagerClosed", err)
	}
}

func TestSessionLifecycleHooksDispatchOnce(t *testing.T) {
	logPath := filepath.Join(t.TempDir(), "events.log")
	command := func(event string) string { return fmt.Sprintf("printf '%s\\n' >> %q", event, logPath) }
	dispatcher := hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.SessionStart: {{Command: command("start")}},
		hooks.SessionEnd:   {{Command: command("end")}},
	}))
	session := &Session{RootDir: t.TempDir(), StateDir: t.TempDir(), HookDispatcher: dispatcher}
	if err := session.SetSessionID("session-hooks"); err != nil {
		t.Fatalf("SetSessionID: %v", err)
	}
	if _, err := session.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	raw, err := os.ReadFile(logPath)
	if err != nil {
		t.Fatalf("ReadFile: %v", err)
	}
	if got, want := strings.Fields(string(raw)), []string{"start", "end"}; strings.Join(got, ",") != strings.Join(want, ",") {
		t.Fatalf("lifecycle events = %v, want %v", got, want)
	}
}

func TestSessionCleanupPreservesWorktreesWhileTerminalFinalizationIsPending(t *testing.T) {
	control, worktreePath := newRuntimeCleanupWorktree(t, "pending-terminal-cleanup")
	// The control's harness path is stable under the repository state root used
	// by newRuntimeCleanupWorktree. Derive it from the worker's session ID via
	// its durable store so this assertion covers the real cleanup boundary.
	storeDir := control.HarnessStore().Dir()
	if err := os.MkdirAll(filepath.Join(storeDir, "terminal-finalizations"), 0o755); err != nil {
		t.Fatalf("mkdir terminal finalizations: %v", err)
	}
	if err := os.WriteFile(filepath.Join(storeDir, "terminal-finalizations", "malformed-but-owned.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write pending terminal finalization: %v", err)
	}

	rt := &Session{AgentControl: control}
	if _, err := rt.Cleanup(); err == nil || !strings.Contains(err.Error(), "preserving session worktrees") {
		t.Fatalf("Cleanup error = %v, want pending-terminal diagnostic", err)
	}
	if _, err := os.Stat(worktreePath); err != nil {
		t.Fatalf("pending terminal cleanup removed replay worktree: %v", err)
	}
}

func TestSessionCleanupRemovesWorktreesWithoutPendingTerminalFinalization(t *testing.T) {
	control, worktreePath := newRuntimeCleanupWorktree(t, "settled-terminal-cleanup")
	rt := &Session{AgentControl: control}
	if _, err := rt.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if _, err := os.Stat(worktreePath); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("settled cleanup retained worktree: %v", err)
	}
}

func newRuntimeCleanupWorktree(t *testing.T, sessionID string) (*agentcontrol.AgentControl, string) {
	t.Helper()
	root := t.TempDir()
	runGit := func(args ...string) {
		t.Helper()
		cmd := exec.Command("git", args...)
		cmd.Dir = root
		if out, err := cmd.CombinedOutput(); err != nil {
			t.Fatalf("git %v: %v\n%s", args, err, out)
		}
	}
	runGit("init")
	runGit("config", "user.email", "runtime-test@example.com")
	runGit("config", "user.name", "Runtime Test")
	writeSessionTestFile(t, filepath.Join(root, "README.md"), "runtime cleanup test\n")
	runGit("add", "README.md")
	runGit("commit", "-m", "initial")

	stateRoot := filepath.Join(t.TempDir(), "state")
	artifactDir := filepath.Join(stateRoot, "sessions", sessionID)
	control, err := agentcontrol.New(agentcontrol.Config{
		Client:       &sessionRecordingClient{},
		DefaultModel: "runtime-cleanup-test",
		ParentRepo:   root,
		WorktreeRoot: filepath.Join(stateRoot, "worktrees"),
		SessionID:    sessionID,
		HistoryDir:   filepath.Join(artifactDir, "workers"),
		ThreadDir:    filepath.Join(artifactDir, "threads"),
		HarnessDir:   filepath.Join(artifactDir, "harness"),
		WorkerFactory: func(string, agentcontrol.WorkerType, agentthread.Metadata) (agent.ToolExecutor, error) {
			return sideThreadRuntimeTools{}, nil
		},
	})
	if err != nil {
		t.Fatalf("agentcontrol.New: %v", err)
	}
	t.Cleanup(func() {
		control.BeginShutdown()
		control.StopAll()
		control.YieldWorkerTerminalFinalizations()
		control.Close()
		_ = control.CleanupSession()
	})
	spawned, err := control.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "cleanup_worktree",
		Prompt:      "finish immediately",
		Isolation:   string(agentcontrol.IsolationWorktree),
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}
	if spawned.WorktreePath == "" {
		t.Fatal("worktree spawn returned no path")
	}
	deadline := time.Now().Add(2 * time.Second)
	for control.HasOwnedWorkerExecutions() && time.Now().Before(deadline) {
		time.Sleep(10 * time.Millisecond)
	}
	if control.HasOwnedWorkerExecutions() {
		t.Fatal("completed worker retained its execution lease")
	}
	return control, spawned.WorktreePath
}

type sessionRecordingClient struct {
	mu            sync.Mutex
	last          providers.ChatRequest
	streamBatches [][]providers.StreamEvent
}

type sessionQueueBlockingClient struct {
	started chan struct{}
	release chan struct{}
}

func (c *sessionQueueBlockingClient) Chat(ctx context.Context, _ providers.ChatRequest) (providers.ChatResponse, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	select {
	case <-c.release:
		return providers.ChatResponse{Content: "done"}, nil
	case <-ctx.Done():
		return providers.ChatResponse{}, ctx.Err()
	}
}

func (c *sessionQueueBlockingClient) StreamChat(ctx context.Context, _ providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	select {
	case c.started <- struct{}{}:
	default:
	}
	events := make(chan providers.StreamEvent, 2)
	go func() {
		defer close(events)
		select {
		case <-c.release:
			events <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "done"}
			events <- providers.StreamEvent{Type: providers.EventDone}
		case <-ctx.Done():
			events <- providers.StreamEvent{Type: providers.EventError, Error: ctx.Err()}
		}
	}()
	return events, nil
}

func TestNewSessionStartsLegacyAgentControlQueue(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	client := &sessionQueueBlockingClient{started: make(chan struct{}, 8), release: make(chan struct{})}
	t.Cleanup(func() {
		_, _ = rt.Cleanup()
	})
	if rt.AgentControl == nil {
		t.Fatal("NewSession did not build the legacy AgentControl")
	}
	rt.AgentControl.UpdateWorkerDefaults(client, "gpt-test", subagent.ManagerOptions{})
	if err := rt.SetSessionID("legacy-queue-session"); err != nil {
		t.Fatalf("SetSessionID: %v", err)
	}

	var firstWorkerID string
	for i := 0; i < 5; i++ {
		result, spawnErr := rt.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
			Type:     agentcontrol.DefaultSubagentType,
			TaskName: "legacy_queue_" + string(rune('a'+i)),
			Prompt:   "hold root runtime capacity",
		})
		if spawnErr != nil {
			t.Fatalf("spawn root worker %d: %v", i, spawnErr)
		}
		if i == 0 {
			firstWorkerID = result.AgentID
		}
		select {
		case <-client.started:
		case <-time.After(2 * time.Second):
			t.Fatalf("root worker %d did not start", i)
		}
	}
	queued, err := rt.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:     agentcontrol.DefaultSubagentType,
		TaskName: "legacy_queue_waiter",
		Prompt:   "run when root capacity frees",
	})
	if err != nil {
		t.Fatalf("queue root worker: %v", err)
	}
	if queued.Status != string(subagent.StatusQueued) {
		t.Fatalf("root queued worker status = %q, want queued", queued.Status)
	}
	if !rt.AgentControl.Stop(firstWorkerID) {
		t.Fatalf("stop root worker %s", firstWorkerID)
	}
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("legacy root AgentControl did not drain its queue")
	}
	if _, err := rt.Cleanup(); err != nil {
		t.Fatalf("Cleanup: %v", err)
	}
	if rt.AgentControl != nil {
		t.Fatal("Cleanup retained the legacy AgentControl")
	}
}

func TestSetSessionIDRestoresLegacyQueueAndTerminalRecovery(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	client := &sessionQueueBlockingClient{started: make(chan struct{}, 2), release: make(chan struct{})}
	rt.AgentControl.UpdateWorkerDefaults(client, "gpt-test", subagent.ManagerOptions{})
	t.Cleanup(func() {
		close(client.release)
		_, _ = rt.Cleanup()
	})

	const (
		sessionID        = "legacy-restored-session"
		queuedWorkerID   = "legacy-restored-queued-worker"
		terminalWorkerID = "legacy-restored-terminal-worker"
	)
	artifactDir := statepath.SessionArtifactDir(rt.StateDir, sessionID)
	harnessDir := filepath.Join(artifactDir, "harness")
	now := time.Now().UTC()
	meta := agentthread.Metadata{
		ID:        queuedWorkerID,
		SessionID: sessionID,
		ParentID:  sessionID,
		Path:      agentthread.RootPath + "/restored_queue",
		TaskName:  "restored_queue",
		Role:      agentcontrol.DefaultSubagentType,
		Status:    agentthread.StatusPending,
		Source: agentthread.Source{
			Kind:           agentthread.SourceThreadSpawn,
			ParentThreadID: sessionID,
			ParentPath:     agentthread.RootPath,
			Depth:          2,
			EdgeStatus:     agentthread.EdgeOpen,
		},
		CreatedAt: now,
		UpdatedAt: now,
	}
	payload, err := json.Marshal(map[string]any{
		"worker_id":   queuedWorkerID,
		"worker_type": agentcontrol.DefaultSubagentType,
		"thread_meta": meta,
		"prompt":      "resume after the real session path is bound",
		"isolation":   "inplace",
	})
	if err != nil {
		t.Fatalf("marshal queue payload: %v", err)
	}
	if err := harness.NewStore(harnessDir).UpsertQueueItem(harness.QueueItem{
		ID:        queuedWorkerID,
		TaskID:    queuedWorkerID,
		Kind:      "agent_spawn",
		Payload:   payload,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist queue payload: %v", err)
	}
	terminalPath := writeRuntimeTerminalFinalization(t, harnessDir, terminalWorkerID)
	finalized := make(chan subagent.Notification, 1)
	unsubscribe := rt.AgentControl.SubscribeWorkerTerminalFinalizer(func(n subagent.Notification) error {
		if n.AgentID == terminalWorkerID {
			finalized <- n
		}
		return nil
	})
	defer unsubscribe()

	select {
	case <-client.started:
		t.Fatal("legacy queue started before the real session was bound")
	case <-time.After(100 * time.Millisecond):
	}
	if err := rt.SetSessionID(sessionID); err != nil {
		t.Fatalf("SetSessionID: %v", err)
	}
	select {
	case <-client.started:
	case <-time.After(2 * time.Second):
		t.Fatal("bound legacy runtime did not start its restored queue")
	}
	select {
	case notification := <-finalized:
		if notification.AgentID != terminalWorkerID || notification.Status != subagent.StatusCompleted {
			t.Fatalf("recovered terminal notification = %+v", notification)
		}
	case <-time.After(2 * time.Second):
		t.Fatal("bound legacy runtime did not recover its terminal intent")
	}
	deadline := time.Now().Add(2 * time.Second)
	for {
		_, statErr := os.Stat(terminalPath)
		if errors.Is(statErr, os.ErrNotExist) {
			break
		}
		if time.Now().After(deadline) {
			t.Fatalf("terminal intent was not acknowledged: %v", statErr)
		}
		time.Sleep(10 * time.Millisecond)
	}
}

func TestSetSessionIDReturnsQueueRestoreFailure(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")
	rt, err := NewSession(Options{
		RootDir: root,
		HomeDir: home,
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "gpt-test"},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	t.Cleanup(func() { _, _ = rt.Cleanup() })
	harnessDir := filepath.Join(statepath.SessionArtifactDir(rt.StateDir, "corrupt-legacy-queue"), "harness")
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		t.Fatalf("mkdir harness: %v", err)
	}
	if err := os.WriteFile(filepath.Join(harnessDir, "queue.json"), []byte("{"), 0o600); err != nil {
		t.Fatalf("write corrupt queue: %v", err)
	}
	if err := rt.SetSessionID("corrupt-legacy-queue"); err == nil || !strings.Contains(err.Error(), "restore queued spawns") {
		t.Fatalf("SetSessionID error = %v, want queue restore failure", err)
	}
}

func writeRuntimeTerminalFinalization(t *testing.T, harnessDir, workerID string) string {
	t.Helper()
	now := time.Now().UTC()
	record := map[string]any{
		"schema_version": "wuu/worker-terminal-finalization/v1",
		"agent_id":       workerID,
		"status":         subagent.StatusCompleted,
		"snapshot": map[string]any{
			"id":           workerID,
			"task_name":    "recovered_terminal",
			"agent_path":   agentthread.RootPath + "/recovered_terminal",
			"parent_id":    "legacy-restored-session",
			"status":       subagent.StatusCompleted,
			"started_at":   now.Add(-time.Second),
			"completed_at": now,
			"result":       "recovered result",
		},
		"created_at": now,
	}
	data, err := json.Marshal(record)
	if err != nil {
		t.Fatalf("marshal terminal finalization: %v", err)
	}
	digest := sha256.Sum256([]byte(workerID))
	path := filepath.Join(harnessDir, "terminal-finalizations", fmt.Sprintf("%x.json", digest[:]))
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir terminal finalizations: %v", err)
	}
	if err := os.WriteFile(path, data, 0o600); err != nil {
		t.Fatalf("write terminal finalization: %v", err)
	}
	return path
}

func (c *sessionRecordingClient) Chat(_ context.Context, req providers.ChatRequest) (providers.ChatResponse, error) {
	c.mu.Lock()
	c.last = req
	c.mu.Unlock()
	return providers.ChatResponse{Content: "done"}, nil
}

func (c *sessionRecordingClient) StreamChat(_ context.Context, req providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	c.mu.Lock()
	c.last = req
	if len(c.streamBatches) > 0 {
		events := append([]providers.StreamEvent(nil), c.streamBatches[0]...)
		c.streamBatches = c.streamBatches[1:]
		c.mu.Unlock()
		ch := make(chan providers.StreamEvent, len(events))
		for _, event := range events {
			ch <- event
		}
		close(ch)
		return ch, nil
	}
	c.mu.Unlock()
	ch := make(chan providers.StreamEvent, 2)
	ch <- providers.StreamEvent{Type: providers.EventContentDelta, Content: "done"}
	ch <- providers.StreamEvent{Type: providers.EventDone}
	close(ch)
	return ch, nil
}

func TestRuntimeContextInjectorIncludesOnlyDynamicTypedBlocks(t *testing.T) {
	inject := RuntimeContextInjector(nil, "", func() []wuucontext.Block {
		return []wuucontext.Block{{
			Kind:    wuucontext.BlockTaskState,
			Title:   "Current visible task plan",
			Source:  "runtime.task_state",
			Content: "plan:\n- [in_progress] edit",
		}}
	})

	msgs := inject()
	messages := flattenContextSegmentsForTest(msgs)
	if len(messages) != 1 {
		t.Fatalf("expected split context messages, got %+v", msgs)
	}
	combined := strings.Builder{}
	names := make(map[string]bool, len(messages))
	for _, msg := range messages {
		if msg.Role != "user" || !msg.Hidden || !wuucontext.IsSystemReminder(msg.Name, msg.Content) {
			t.Fatalf("expected hidden context reminder message, got %+v", msg)
		}
		if names[msg.Name] {
			t.Fatalf("context message names should be unique: %+v", msgs)
		}
		names[msg.Name] = true
		combined.WriteString(msg.Content)
		combined.WriteString("\n")
	}
	content := combined.String()
	for _, want := range []string{"<system-reminder>", "[TASK_STATE]", "rule: Latest update for this key wins.", "[in_progress] edit"} {
		if !strings.Contains(content, want) {
			t.Fatalf("injected context missing %q:\n%s", want, content)
		}
	}
	if strings.Contains(content, "[ENVIRONMENT]") {
		t.Fatalf("stable environment should stay out of request-only context:\n%s", content)
	}
}

func (c *sessionRecordingClient) LastRequest() providers.ChatRequest {
	c.mu.Lock()
	defer c.mu.Unlock()
	return c.last
}

func flattenContextSegmentsForTest(segments []agent.ContextSegment) []providers.ChatMessage {
	var out []providers.ChatMessage
	for _, segment := range segments {
		out = append(out, segment.Messages...)
	}
	return out
}

func sessionToolDefByName(defs []providers.ToolDefinition, name string) (providers.ToolDefinition, bool) {
	for _, def := range defs {
		if def.Name == name {
			return def, true
		}
	}
	return providers.ToolDefinition{}, false
}

func writeSessionTestFile(t *testing.T, path, content string) {
	t.Helper()
	if err := os.MkdirAll(filepath.Dir(path), 0o755); err != nil {
		t.Fatalf("mkdir %s: %v", filepath.Dir(path), err)
	}
	if err := os.WriteFile(path, []byte(content), 0o644); err != nil {
		t.Fatalf("write %s: %v", path, err)
	}
}

func TestNewSessionUsesUserStateNotWorkspaceDotWuu(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	wuuHome := filepath.Join(home, "state")
	t.Setenv("WUU_HOME", wuuHome)
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if !strings.HasPrefix(rt.StateDir, filepath.Join(wuuHome, "workspaces")+string(os.PathSeparator)) {
		t.Fatalf("StateDir = %q, want under %q", rt.StateDir, filepath.Join(wuuHome, "workspaces"))
	}
	if rt.SessionDir != statepath.SessionsDir(wuuHome) {
		t.Fatalf("SessionDir = %q, want %q", rt.SessionDir, statepath.SessionsDir(wuuHome))
	}
	if _, err := os.Stat(filepath.Join(root, ".wuu")); !os.IsNotExist(err) {
		t.Fatalf("workspace .wuu should not be created, stat err=%v", err)
	}
	if _, err := os.Stat(filepath.Join(rt.StateDir, "runtime", "processes")); err != nil {
		t.Fatalf("process registry should be under user state: %v", err)
	}
	if rt.ActivityRegistry == nil {
		t.Fatal("session must own an Activity registry")
	}
	threadRuntime, err := rt.NewThreadRuntime("thread-activity-test")
	if err != nil {
		t.Fatalf("NewThreadRuntime: %v", err)
	}
	if threadRuntime.ActivityRegistry != rt.ActivityRegistry {
		t.Fatal("thread runtime must share the session Activity registry")
	}
}

func TestNewSessionKeepsGitContextOutOfBaseSystemPrompt(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	cmd := exec.Command("git", "init")
	cmd.Dir = root
	if out, err := cmd.CombinedOutput(); err != nil {
		t.Fatalf("git init: %v\n%s", err, out)
	}
	writeSessionTestFile(t, filepath.Join(root, "changed.txt"), "dirty\n")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	for _, disallowed := range []string{"# Git Context", "Recent commits:", "Status:\n", "Branch:"} {
		if strings.Contains(rt.BaseSystemPrompt, disallowed) {
			t.Fatalf("base system prompt should not include volatile git context %q:\n%s", disallowed, rt.BaseSystemPrompt)
		}
	}

	if !strings.Contains(rt.BaseSystemPrompt, "# Environment") ||
		!strings.Contains(rt.BaseSystemPrompt, "- Current working directory: "+root) ||
		!strings.Contains(rt.BaseSystemPrompt, "- Current date:") {
		t.Fatalf("base system prompt should include stable environment context:\n%s", rt.BaseSystemPrompt)
	}
	foundEnvironmentSection := false
	for _, section := range rt.BaseSystemPromptSections {
		if section.Key == "environment" && section.Static {
			foundEnvironmentSection = true
			break
		}
	}
	if !foundEnvironmentSection {
		t.Fatalf("base system prompt should report a static environment section: %+v", rt.BaseSystemPromptSections)
	}
	if strings.Contains(rt.BaseSystemPrompt, "Git status:") || strings.Contains(rt.BaseSystemPrompt, "Git branch:") {
		t.Fatalf("base system prompt should not include volatile git state:\n%s", rt.BaseSystemPrompt)
	}

	segments := RuntimeContextInjector(nil, "")()
	msgs := flattenContextSegmentsForTest(segments)
	if len(msgs) != 0 {
		t.Fatalf("default runtime context should not inject stable environment messages, got %+v", segments)
	}
	content := rt.BaseSystemPrompt
	if strings.Contains(content, "[REPO_MAP]") || strings.Contains(content, "source: runtime.repo_map") {
		t.Fatalf("base system prompt should not inject repo map by default:\n%s", content)
	}
	if strings.Contains(content, "[RECENT_DIFF]") {
		t.Fatalf("base system prompt should not inject recent diff by default:\n%s", content)
	}
}

func TestApplyGeneralConfigRefreshesPromptAndGitAttribution(t *testing.T) {
	// Strip inherited wuu git-wrapper shim dirs from PATH. Agent shells (and
	// any process spawned from one, like `go test` run inside wuu) carry the
	// attribution wrapper first on PATH. The structured git tool resolves
	// "git" through this process's PATH, and the shim appends the co-author
	// trailer unconditionally, which breaks the attribution-disabled
	// assertion below.
	pathEntries := strings.Split(os.Getenv("PATH"), string(os.PathListSeparator))
	kept := pathEntries[:0]
	for _, entry := range pathEntries {
		if strings.Contains(entry, "git-wrapper") {
			continue
		}
		kept = append(kept, entry)
	}
	t.Setenv("PATH", strings.Join(kept, string(os.PathListSeparator)))

	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	baseConfig := config.Config{
		DefaultProvider: "test",
		Providers: map[string]config.ProviderConfig{
			"test": {
				Type:      "openai-compatible",
				BaseURL:   "https://example.test/v1",
				APIKeyEnv: "TEST_WUU_KEY",
				Model:     "gpt-test",
			},
		},
	}
	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config:     baseConfig,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.Toolkit == nil {
		t.Fatal("expected toolkit")
	}
	gitAttributionEnabled := false
	baseConfig.Agent.GitAttributionEnabled = &gitAttributionEnabled
	rt.ApplyGeneralConfig(baseConfig, home)
	for _, args := range [][]string{
		{"init", "-q", root},
		{"-C", root, "config", "user.name", "Runtime Test"},
		{"-C", root, "config", "user.email", "runtime@example.com"},
	} {
		if output, commandErr := exec.Command("git", args...).CombinedOutput(); commandErr != nil {
			t.Fatalf("git %v: %v\n%s", args, commandErr, output)
		}
	}
	commitThroughToolkit := func(message, content string) string {
		t.Helper()
		gitTool := rt.Toolkit.LookupTool("git")
		if gitTool == nil {
			t.Fatal("git tool is not registered")
		}
		if writeErr := os.WriteFile(filepath.Join(root, "attribution.txt"), []byte(content), 0o644); writeErr != nil {
			t.Fatal(writeErr)
		}
		for _, invocation := range []map[string]any{
			{"subcommand": "add", "args": []string{"attribution.txt"}},
			{"subcommand": "commit", "args": []string{"-m", message}},
		} {
			arguments, _ := json.Marshal(invocation)
			if _, executeErr := gitTool.Execute(context.Background(), string(arguments)); executeErr != nil {
				t.Fatalf("commit through toolkit: %v", executeErr)
			}
		}
		output, commandErr := exec.Command("git", "-C", root, "log", "-1", "--format=%B").Output()
		if commandErr != nil {
			t.Fatalf("read commit: %v", commandErr)
		}
		return string(output)
	}

	disabledMessage := commitThroughToolkit("Disabled attribution", "disabled\n")
	if strings.Contains(disabledMessage, "wuu-agent[bot]") {
		t.Fatalf("disabled attribution added a WUU trailer:\n%s", disabledMessage)
	}
	gitAttributionEnabled = true
	baseConfig.Agent.GitAttributionEnabled = &gitAttributionEnabled
	rt.ApplyGeneralConfig(baseConfig, home)
	enabledMessage := commitThroughToolkit("Enabled attribution", "enabled\n")
	if !strings.Contains(enabledMessage, "wuu-agent[bot]") {
		t.Fatalf("enabled attribution did not add a WUU trailer:\n%s", enabledMessage)
	}
}

func TestNewSessionDiscoversPluginSkills(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	pluginRoot := filepath.Join(root, ".wuu", "plugins", "compose-kit")
	writeSessionTestFile(t, filepath.Join(pluginRoot, "plugin.json"), `{
  "id": "compose-kit",
  "description": "Compose plugin assets",
  "skills": ["skills"]
}`)
	writeSessionTestFile(t, filepath.Join(pluginRoot, "skills", "brainstorm.md"), `---
name: brainstorm
description: Explore product options.
---
Brainstorm options.
`)
	cfg := config.Config{
		DefaultProvider: "test",
		Providers: map[string]config.ProviderConfig{
			"test": {
				Type:      "openai-compatible",
				BaseURL:   "https://example.test/v1",
				APIKeyEnv: "TEST_WUU_KEY",
				Model:     "gpt-test",
			},
		},
	}
	grantSessionTestPlugin(t, &cfg, root, filepath.Join(home, "state"), "compose-kit")
	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config:     cfg,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !runtimeHasPlugin(rt.Plugins, "compose-kit") {
		t.Fatalf("plugins not discovered: %+v", rt.Plugins)
	}
	skill, ok := skills.Find(rt.Skills, "brainstorm")
	if !ok || skill.Source != "plugin:compose-kit" {
		t.Fatalf("plugin skill not discovered with source: %+v", skill)
	}
}

func runtimeHasPlugin(items []pluginpkg.Plugin, id string) bool {
	for _, item := range items {
		if item.ID == id {
			return true
		}
	}
	return false
}

func grantSessionTestPlugin(t *testing.T, cfg *config.Config, root, wuuHome, id string) {
	t.Helper()
	for _, item := range pluginpkg.Discover(root, wuuHome) {
		if item.ID != id {
			continue
		}
		settings := cfg.Extensions
		if settings == nil {
			settings = &extensions.Settings{}
		}
		if err := settings.RecordGrant(extensions.Grant{
			SubjectID:   item.SubjectID,
			Fingerprint: item.Fingerprint,
			Scope:       extensions.GrantScopeProject,
			Permissions: append([]string(nil), item.EffectivePermissions...),
			ApprovedAt:  time.Now().UTC(),
		}); err != nil {
			t.Fatalf("grant plugin %q: %v", id, err)
		}
		cfg.Extensions = settings
		return
	}
	t.Fatalf("plugin %q not discovered", id)
}

func TestNewSessionConsumesCodexPluginManifestAssets(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	pluginRoot := filepath.Join(root, ".wuu", "plugins", "codex-kit")
	writeSessionTestFile(t, filepath.Join(pluginRoot, ".codex-plugin", "plugin.json"), `{
  "name": "codex-kit",
  "skills": "./skills",
  "mcpServers": {"docs": {"command": "codex-docs"}}
}`)
	writeSessionTestFile(t, filepath.Join(pluginRoot, "skills", "review", "SKILL.md"), `---
name: codex-review
description: Review through a Codex-format plugin.
---
Review the change.
`)

	cfg := config.Config{
		DefaultProvider: "test",
		Providers: map[string]config.ProviderConfig{
			"test": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKeyEnv: "TEST_WUU_KEY", Model: "gpt-test"},
		},
	}
	grantSessionTestPlugin(t, &cfg, root, filepath.Join(home, "state"), "codex-kit")
	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config:     cfg,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if skill, ok := skills.Find(rt.Skills, "review"); !ok || skill.Source != "plugin:codex-kit" {
		t.Fatalf("Codex plugin skill not discovered: %+v", rt.Skills)
	}
	servers := mcpServersFromConfigAndPlugins(cfg, rt.Plugins)
	if servers["plugin.codex-kit.docs"].Command != "codex-docs" {
		t.Fatalf("Codex plugin MCP server not normalized: %+v", servers)
	}
}

func TestDiscoverSkillsUsesOpencodeAndAgentsPaths(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}
	workspace := filepath.Join(root, "packages", "app")
	if err := os.MkdirAll(workspace, 0755); err != nil {
		t.Fatalf("mkdir workspace: %v", err)
	}

	writeSessionTestFile(t, filepath.Join(root, ".opencode", "skill", "root-skill", "SKILL.md"), `---
name: root-skill
description: Root opencode skill.
---
Root body.
`)
	writeSessionTestFile(t, filepath.Join(workspace, ".opencode", "skills", "app-skill", "SKILL.md"), `---
name: app-skill
description: App opencode skill.
---
App body.
`)
	writeSessionTestFile(t, filepath.Join(workspace, ".agents", "skills", "agent-skill", "SKILL.md"), `---
name: agent-skill
description: Agent skill.
---
Agent body.
`)
	writeSessionTestFile(t, filepath.Join(home, ".config", "opencode", "skills", "global-skill", "SKILL.md"), `---
name: global-skill
description: Global opencode skill.
---
Global body.
`)
	writeSessionTestFile(t, filepath.Join(home, ".codex", "skills", "codex-user-skill", "SKILL.md"), `---
name: codex-user-skill
description: Codex user skill.
---
Codex user body.
`)

	got := discoverSkills(workspace, home, filepath.Join(home, ".wuu"), nil)
	for _, name := range []string{"root-skill", "app-skill", "agent-skill", "global-skill", "codex-user-skill"} {
		if _, ok := skills.Find(got, name); !ok {
			t.Fatalf("skill %q not discovered in %+v", name, got)
		}
	}
}

func TestDiscoverSkillsIncludesClaudeCommands(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	wuuHome := filepath.Join(home, ".wuu")
	if err := os.Mkdir(filepath.Join(root, ".git"), 0755); err != nil {
		t.Fatalf("mkdir .git: %v", err)
	}

	// Project-chain Claude Code command with execution-binding frontmatter that
	// must be ignored, plus a $ARGUMENTS placeholder that must be normalized.
	writeSessionTestFile(t, filepath.Join(root, ".claude", "commands", "commit.md"), `---
description: Create a commit
argument-hint: <message>
allowed-tools: [Bash(git commit:*)]
model: claude-opus-4
---
Create a commit for $ARGUMENTS.
`)
	// Project-chain wuu command without frontmatter.
	writeSessionTestFile(t, filepath.Join(root, ".wuu", "commands", "plan.md"), "Draft a plan.")
	// User-level command under ~/.wuu/commands.
	writeSessionTestFile(t, filepath.Join(wuuHome, "commands", "greet.md"), "Say hello.")

	got := discoverSkills(root, home, wuuHome, nil)

	commit, ok := skills.Find(got, "commit")
	if !ok {
		t.Fatalf("commit command not discovered in %+v", got)
	}
	if commit.Description != "Create a commit" {
		t.Fatalf("Description = %q", commit.Description)
	}
	if commit.ArgumentHint != "<message>" {
		t.Fatalf("ArgumentHint = %q", commit.ArgumentHint)
	}
	if len(commit.AllowedTools) != 0 {
		t.Fatalf("AllowedTools = %v, want ignored", commit.AllowedTools)
	}
	if commit.Model != "" {
		t.Fatalf("Model = %q, want ignored", commit.Model)
	}
	if !strings.Contains(commit.Content, "${ARGUMENTS}") {
		t.Fatalf("Content did not normalize $ARGUMENTS: %q", commit.Content)
	}
	for _, name := range []string{"plan", "greet"} {
		if _, ok := skills.Find(got, name); !ok {
			t.Fatalf("command %q not discovered", name)
		}
	}
}

func TestNewSessionWiresPluginHooks(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	pluginRoot := filepath.Join(root, ".wuu", "plugins", "hook-kit")
	writeSessionTestFile(t, filepath.Join(pluginRoot, "plugin.json"), `{
  "id": "hook-kit",
  "hooks": {
    "PreToolUse": [
      {"matcher": "read_file", "command": "printf '{\"additional_context\":\"plugin hook ran\"}'"}
    ]
  }
}`)

	cfg := config.Config{
		DefaultProvider: "test",
		Providers: map[string]config.ProviderConfig{
			"test": {
				Type:      "openai-compatible",
				BaseURL:   "https://example.test/v1",
				APIKeyEnv: "TEST_WUU_KEY",
				Model:     "gpt-test",
			},
		},
	}
	grantSessionTestPlugin(t, &cfg, root, filepath.Join(home, "state"), "hook-kit")
	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config:     cfg,
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	out, err := rt.HookDispatcher.Dispatch(context.Background(), hooks.PreToolUse, &hooks.Input{ToolName: "read_file"})
	if err != nil {
		t.Fatalf("Dispatch plugin hook: %v", err)
	}
	if out.Context != "plugin hook ran" {
		t.Fatalf("plugin hook context = %q", out.Context)
	}
}

func TestMCPServersFromConfigAndPluginsPrefixesPluginServers(t *testing.T) {
	disabled := false
	plugins := []pluginpkg.Plugin{{
		Manifest: pluginpkg.Manifest{
			ID: "docs",
			MCPServers: map[string]config.MCPServerConfig{
				"search": {Command: "plugin-docs"},
			},
		},
	}}
	cfg := config.Config{
		MCPServers: map[string]config.MCPServerConfig{
			"search":                  {Command: "user-docs"},
			"plugin.docs.search":      {Enabled: &disabled},
			"plugin.cua-mac.computer": {},
		},
	}
	servers := mcpServersFromConfigAndPlugins(cfg, plugins)
	if servers["search"].Command != "user-docs" {
		t.Fatalf("user MCP server changed: %+v", servers)
	}
	if got := servers["plugin.docs.search"]; got.Command != "plugin-docs" || got.Enabled == nil || *got.Enabled {
		t.Fatalf("plugin MCP server missing or unprefixed: %+v", servers)
	}
	if _, ok := servers["plugin.cua-mac.computer"]; ok {
		t.Fatalf("inactive plugin MCP override must be ignored: %+v", servers)
	}
}

func TestMCPActivityBindingsFromPluginsUsesDeclaredKind(t *testing.T) {
	bindings := mcpActivityBindingsFromPlugins([]pluginpkg.Plugin{{
		Manifest: pluginpkg.Manifest{
			ID:            "cua-mac",
			ActivityKinds: []string{"cua"},
			MCPServers: map[string]config.MCPServerConfig{
				"computer": {Command: "wuu-cua-mac"},
			},
		},
	}})
	binding, ok := bindings["plugin.cua-mac.computer"]
	if !ok || binding.Kind != activity.KindCUA || binding.PluginID != "cua-mac" {
		t.Fatalf("cua binding = %+v, ok=%v", binding, ok)
	}
}

func TestNewThreadRuntimeOrdinarySpawnIsMemoryless(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	client := &sessionRecordingClient{}
	rt.WorkerClient = client
	threadRT, err := rt.NewThreadRuntime("thread-memoryless")
	if err != nil {
		t.Fatalf("NewThreadRuntime: %v", err)
	}
	defer func() {
		threadRT.AgentControl.StopAll()
		time.Sleep(100 * time.Millisecond)
	}()
	if _, err := threadRT.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "inspect_repo",
		Prompt:      "inspect the repo",
		Synchronous: true,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	req := client.LastRequest()
	if len(req.Messages) == 0 || strings.Contains(req.Messages[0].Content, "# Persistent Memory") {
		t.Fatalf("ordinary worker should not receive persistent memory prompt: %+v", req.Messages)
	}
}

func TestNewThreadRuntimePropagatesQueuedSpawnRestoreFailure(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() { _, _ = rt.Cleanup() }()
	if rt.AgentControl != nil {
		defer rt.AgentControl.Close()
	}
	rt.WorkerClient = &sessionRecordingClient{}

	threadID := "thread-corrupt-queue"
	harnessDir := filepath.Join(statepath.SessionArtifactDir(rt.StateDir, threadID), "harness")
	if err := os.MkdirAll(harnessDir, 0o755); err != nil {
		t.Fatalf("mkdir harness: %v", err)
	}
	if err := os.WriteFile(filepath.Join(harnessDir, "queue.json"), []byte("{"), 0o644); err != nil {
		t.Fatalf("write corrupt queue: %v", err)
	}

	threadRuntime, err := rt.NewThreadRuntime(threadID)
	if threadRuntime != nil {
		if threadRuntime.AgentControl != nil {
			threadRuntime.AgentControl.Close()
		}
		t.Fatal("NewThreadRuntime returned a runtime after queued-spawn restore failed")
	}
	if err == nil || !strings.Contains(err.Error(), "create thread agent control") || !strings.Contains(err.Error(), "restore queued spawns") {
		t.Fatalf("NewThreadRuntime error = %v, want propagated queued-spawn restore error", err)
	}
}

func TestNewThreadRuntimeToolLedgerFailureDoesNotTouchRestoredQueue(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defer func() {
		if rt.AgentControl != nil {
			rt.AgentControl.StopAll()
			rt.AgentControl.Close()
		}
		_, _ = rt.Cleanup()
	}()
	rt.WorkerClient = &sessionRecordingClient{}

	threadID := "thread-ledger-failure"
	workerID := "worker-restored"
	now := time.Now().UTC()
	meta := agentthread.Metadata{
		ID:        workerID,
		SessionID: threadID,
		ParentID:  threadID,
		Path:      agentthread.RootPath + "/restored_task",
		TaskName:  "restored_task",
		Role:      agentcontrol.DefaultSubagentType,
		Source: agentthread.Source{
			Kind:           agentthread.SourceThreadSpawn,
			ParentThreadID: threadID,
			ParentPath:     agentthread.RootPath,
			Depth:          2,
			EdgeStatus:     agentthread.EdgeOpen,
		},
		Status:    agentthread.StatusPending,
		CreatedAt: now,
		UpdatedAt: now,
	}
	payload, err := json.Marshal(map[string]any{
		"worker_id":   workerID,
		"worker_type": agentcontrol.DefaultSubagentType,
		"thread_meta": meta,
		"prompt":      "resume queued task",
		"isolation":   "inplace",
	})
	if err != nil {
		t.Fatalf("marshal queued spawn: %v", err)
	}
	harnessDir := filepath.Join(statepath.SessionArtifactDir(rt.StateDir, threadID), "harness")
	store := harness.NewStore(harnessDir)
	if err := store.UpsertQueueItem(harness.QueueItem{
		ID:        workerID,
		TaskID:    workerID,
		Kind:      "agent_spawn",
		Payload:   payload,
		CreatedAt: now,
		UpdatedAt: now,
	}); err != nil {
		t.Fatalf("persist queued spawn: %v", err)
	}
	queuePath := filepath.Join(harnessDir, "queue.json")
	queueBefore, err := os.ReadFile(queuePath)
	if err != nil {
		t.Fatalf("read queue before runtime creation: %v", err)
	}

	// Force the root ToolLedger preflight to fail. AgentControl must not be
	// created, because its constructor immediately restores and starts queued
	// workers.
	rt.SessionDir = ""
	threadRuntime, err := rt.NewThreadRuntime(threadID)
	if threadRuntime != nil {
		if threadRuntime.AgentControl != nil {
			threadRuntime.AgentControl.StopAll()
			threadRuntime.AgentControl.Close()
		}
		t.Fatal("NewThreadRuntime returned a runtime after tool ledger initialization failed")
	}
	if err == nil || !strings.Contains(err.Error(), "open tool ledger") {
		t.Fatalf("NewThreadRuntime error = %v, want tool ledger initialization failure", err)
	}

	// The old ordering created AgentControl before opening the root ledger;
	// its background queue starter would remove this item asynchronously.
	deadline := time.Now().Add(500 * time.Millisecond)
	for {
		queueAfter, readErr := os.ReadFile(queuePath)
		if readErr != nil {
			t.Fatalf("read queue after runtime failure: %v", readErr)
		}
		if string(queueAfter) != string(queueBefore) {
			t.Fatalf("restored queue changed after tool ledger failure\nbefore: %s\nafter:  %s", queueBefore, queueAfter)
		}
		if !time.Now().Before(deadline) {
			break
		}
		time.Sleep(10 * time.Millisecond)
	}
	items, err := store.ListQueueItems()
	if err != nil {
		t.Fatalf("list queue after runtime failure: %v", err)
	}
	if len(items) != 1 || items[0].ID != workerID {
		t.Fatalf("restored queue after tool ledger failure = %+v, want only %q", items, workerID)
	}
	if _, err := os.Stat(filepath.Join(statepath.SessionArtifactDir(rt.StateDir, threadID), "threads")); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("AgentControl thread store exists after preflight failure: %v", err)
	}
}

func TestNewThreadRuntimeWorkerUsesWorkerProfileToolSurface(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")
	t.Setenv("TEST_ANTHROPIC_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "openai",
			Providers: map[string]config.ProviderConfig{
				"openai": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-5-codex",
				},
				"anthropic": {
					Type:      "anthropic",
					BaseURL:   "https://api.anthropic.com",
					APIKeyEnv: "TEST_ANTHROPIC_KEY",
					Model:     "claude-sonnet-4-5",
				},
			},
			Agent: config.AgentConfig{
				ModelRoles: config.ModelRolesConfig{
					Worker: config.ModelRoleConfig{Provider: "anthropic", Model: "claude-sonnet-4-5"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !strings.Contains(rt.BaseSystemPrompt, "[Tool surface: openai_codex]") {
		t.Fatalf("main prompt should use main Codex surface:\n%s", rt.BaseSystemPrompt)
	}

	client := &sessionRecordingClient{}
	rt.WorkerClient = client
	threadRT, err := rt.NewThreadRuntime("thread-worker-surface")
	if err != nil {
		t.Fatalf("NewThreadRuntime: %v", err)
	}
	defer func() {
		threadRT.AgentControl.StopAll()
		time.Sleep(100 * time.Millisecond)
	}()
	if _, err := threadRT.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "inspect_repo",
		Prompt:      "inspect the repo",
		Synchronous: true,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	req := client.LastRequest()
	toolNames := map[string]bool{}
	for _, def := range req.Tools {
		toolNames[def.Name] = true
	}
	for _, want := range []string{"bash", "edit_file", "write_file"} {
		if !toolNames[want] {
			t.Fatalf("worker should receive %s from worker profile surface; tools=%v", want, toolNames)
		}
	}
	if toolNames["apply_patch"] {
		t.Fatalf("worker should not inherit Codex apply_patch surface; tools=%v", toolNames)
	}
	if len(req.Messages) == 0 {
		t.Fatal("worker sent no messages")
	}
	systemPrompt := req.Messages[0].Content
	for _, want := range []string{"[Tool surface: anthropic_claude]", "Use edit_file for targeted changes", "write_file for new files or complete rewrites"} {
		if !strings.Contains(systemPrompt, want) {
			t.Fatalf("worker system prompt should use worker profile fragment %q:\n%s", want, systemPrompt)
		}
	}
}

func TestNewThreadRuntimeLocalWorkerDoesNotTeachTerminalPaths(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "ollama",
			Providers: map[string]config.ProviderConfig{
				"ollama": {
					Type:    "openai-compatible",
					BaseURL: "http://127.0.0.1:11434/v1",
					APIKey:  "dummy",
					Model:   "llama-coder",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	client := &sessionRecordingClient{}
	rt.WorkerClient = client
	threadRT, err := rt.NewThreadRuntime("thread-local-worker-surface")
	if err != nil {
		t.Fatalf("NewThreadRuntime: %v", err)
	}
	defer func() {
		threadRT.AgentControl.StopAll()
		time.Sleep(100 * time.Millisecond)
	}()
	if _, err := threadRT.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "inspect_repo",
		Prompt:      "inspect the repo",
		Synchronous: true,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	req := client.LastRequest()
	toolNames := map[string]bool{}
	for _, def := range req.Tools {
		toolNames[def.Name] = true
	}
	for _, want := range []string{"read_file", "edit_file", "write_file"} {
		if !toolNames[want] {
			t.Fatalf("local worker should keep %s from generic edit surface; tools=%v", want, toolNames)
		}
	}
	for _, hidden := range []string{"bash", "run_shell", "run_test", "start_process", "git", "apply_patch"} {
		if toolNames[hidden] {
			t.Fatalf("local worker must not expose %s; tools=%v", hidden, toolNames)
		}
	}
	if len(req.Messages) == 0 {
		t.Fatal("worker sent no messages")
	}
	systemPrompt := req.Messages[0].Content
	if !strings.Contains(systemPrompt, "[Tool surface: generic") {
		t.Fatalf("local worker prompt missing generic surface fragment:\n%s", systemPrompt)
	}
	for _, banned := range []string{
		"bash",
		"run_shell",
		"run_test",
		"start_process",
		"command.bash",
		"terminal",
		"shell",
		"git",
		"git status",
		"git diff",
		"git commit",
		"npx vitest",
		"npm test",
		"npm run dev",
	} {
		if strings.Contains(systemPrompt, banned) {
			t.Fatalf("local worker prompt must not teach terminal path %q:\n%s", banned, systemPrompt)
		}
	}
}

func TestNewSessionDoesNotAppendRetiredConfigPrompt(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !strings.Contains(rt.BaseSystemPrompt, config.DefaultSystemPrompt()) {
		t.Fatalf("assembled prompt missing built-in base:\n%s", rt.BaseSystemPrompt)
	}
	if strings.Contains(rt.BaseSystemPrompt, "User Custom Instructions") {
		t.Fatalf("assembled prompt should not inject retired config prompt:\n%s", rt.BaseSystemPrompt)
	}
}

func TestNewSessionResolvesConfiguredVariantOptions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "xiaomi",
			Providers: map[string]config.ProviderConfig{
				"xiaomi": {
					Type:      "openai-compatible",
					BaseURL:   "https://token-plan-cn.xiaomimimo.com/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "mimo-v2.5-pro",
				},
			},
			Agent: config.AgentConfig{
				Variant: "high",
				Effort:  "low",
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if rt.StreamRunner.Variant != "high" {
		t.Fatalf("Variant = %q, want high", rt.StreamRunner.Variant)
	}
	if rt.StreamRunner.Effort != "" {
		t.Fatalf("legacy Effort should be empty when variant options are active, got %q", rt.StreamRunner.Effort)
	}
	if got := rt.StreamRunner.ProviderOptions["reasoningEffort"]; got != "high" {
		t.Fatalf("ProviderOptions reasoningEffort = %#v", got)
	}
	if rt.StreamRunner.ContextWindowOverride != 1048576 {
		t.Fatalf("ContextWindowOverride = %d", rt.StreamRunner.ContextWindowOverride)
	}
}

func TestNewSessionUsesConfiguredModelLimitForContextWindow(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "custom",
			Providers: map[string]config.ProviderConfig{
				"custom": {
					Type:      "anthropic",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "private-1m-model",
					Models: map[string]config.ProviderModelConfig{
						"private-1m-model": {
							Limit: &config.ProviderModelLimitConfig{
								Context: 1_000_000,
								Output:  128_000,
							},
						},
					},
				},
			},
			Agent: config.AgentConfig{Name: "Mia Agent"},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if rt.StreamRunner.ContextWindowOverride != 1_000_000 {
		t.Fatalf("ContextWindowOverride = %d", rt.StreamRunner.ContextWindowOverride)
	}
	if rt.StreamRunner.MaxInputTokens != 0 {
		t.Fatalf("MaxInputTokens = %d, want 0 without an explicit input limit", rt.StreamRunner.MaxInputTokens)
	}
	if rt.StreamRunner.OutputReserveTokens != 128_000 {
		t.Fatalf("OutputReserveTokens = %d", rt.StreamRunner.OutputReserveTokens)
	}
}

func TestNewSessionUnknownModelDisablesProactiveContextWindow(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "custom",
			Providers: map[string]config.ProviderConfig{
				"custom": {
					Type:      "anthropic",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "private-unknown-byo-model",
				},
			},
			Agent: config.AgentConfig{Name: "Mia Agent"},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if rt.StreamRunner.ContextWindowOverride != 0 {
		t.Fatalf("ContextWindowOverride = %d, want 0 for unknown BYOK model", rt.StreamRunner.ContextWindowOverride)
	}
	if rt.StreamRunner.MaxInputTokens != 0 {
		t.Fatalf("MaxInputTokens = %d, want 0 for unknown BYOK model", rt.StreamRunner.MaxInputTokens)
	}
}

func TestNewSessionUsesCatalogModelAPIIDAndOptions(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("OPENAI_API_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "openai",
			Providers: map[string]config.ProviderConfig{
				"openai": {
					Type:    "openai",
					BaseURL: "https://api.openai.com/v1",
					Model:   "gpt-5.5-fast",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if rt.StreamRunner.Model != "gpt-5.5-fast" {
		t.Fatalf("Model = %q", rt.StreamRunner.Model)
	}
	if rt.StreamRunner.APIModel != "gpt-5.5" {
		t.Fatalf("APIModel = %q", rt.StreamRunner.APIModel)
	}
	for _, want := range []string{
		"[Tool surface: openai_gpt]",
		"Use apply_patch for file changes and bash for command execution",
	} {
		if !strings.Contains(rt.BaseSystemPrompt, want) {
			t.Fatalf("BaseSystemPrompt missing harness adapter text %q:\n%s", want, rt.BaseSystemPrompt)
		}
	}
	for _, bad := range []string{
		"# Harness Adapter",
		"Provider/model:",
		"task-handling options inside wuu",
		"direct work, subagents, or workflows",
		"workflows only when",
		"matching saved workflow",
		"especially MCP tools, workflows",
		"workflows, scheduling",
	} {
		if strings.Contains(rt.BaseSystemPrompt, bad) {
			t.Fatalf("BaseSystemPrompt should not include generic workflow guidance %q:\n%s", bad, rt.BaseSystemPrompt)
		}
	}
	if got := rt.StreamRunner.ProviderOptions["serviceTier"]; got != "priority" {
		t.Fatalf("ProviderOptions serviceTier = %#v", got)
	}
	if rt.StreamRunner.MaxInputTokens != 922000 {
		t.Fatalf("MaxInputTokens = %d", rt.StreamRunner.MaxInputTokens)
	}
	if rt.StreamRunner.OutputReserveTokens != 128000 {
		t.Fatalf("OutputReserveTokens = %d", rt.StreamRunner.OutputReserveTokens)
	}
}

func TestNewSessionAutoUsesNativeDeferredForFirstPartyAnthropic(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "anthropic",
			Providers: map[string]config.ProviderConfig{
				"anthropic": {
					Type:    "anthropic",
					BaseURL: "https://api.anthropic.com",
					APIKey:  "abc",
					Model:   "claude-sonnet-4-5",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.Toolkit == nil || !rt.Toolkit.ToolSearchEnabled() {
		t.Fatal("first-party Anthropic auto mode should expose tool_search")
	}
	if !rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatal("first-party Anthropic auto mode should use native deferred loading")
	}
	if rt.StreamRunner == nil || !rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("first-party Anthropic runner should forward native deferred loading to provider requests")
	}
}

func TestNewSessionAutoUsesNativeDeferredForFirstPartyOpenAIResponses(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "openai",
			Providers: map[string]config.ProviderConfig{
				"openai": {
					Type:    "openai-compatible",
					BaseURL: "https://api.openai.com/v1",
					APIKey:  "abc",
					Model:   "gpt-5.4",
					WireAPI: "responses",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.ToolLoadingMode != config.ToolLoadingNative {
		t.Fatalf("ToolLoadingMode = %q, want native", rt.ToolLoadingMode)
	}
	if rt.Toolkit == nil || !rt.Toolkit.ToolSearchEnabled() {
		t.Fatal("first-party OpenAI Responses auto mode should expose tool_search")
	}
	if !rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatal("first-party OpenAI Responses auto mode should use native deferred loading")
	}
	if rt.StreamRunner == nil || !rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("first-party OpenAI Responses runner should forward native deferred loading to provider requests")
	}
}

func TestReconfigureToolLoadingClearsNativeDiscoveryForCompatibleProvider(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "openai",
			Providers: map[string]config.ProviderConfig{
				"openai": {
					Type:    "openai-compatible",
					BaseURL: "https://api.openai.com/v1",
					APIKey:  "abc",
					Model:   "gpt-5.4",
					WireAPI: "responses",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if !rt.NativeDeferredToolDiscovery || rt.StreamRunner == nil || !rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("fixture must start with OpenAI native deferred discovery")
	}

	kimi := config.ProviderConfig{Type: "anthropic", BaseURL: "https://api.kimi.com/coding", Model: "k3"}
	if err := rt.ReconfigureToolLoading(config.AgentConfig{}, kimi, "k3", nil); err != nil {
		t.Fatalf("ReconfigureToolLoading: %v", err)
	}
	if rt.ToolLoadingMode != config.ToolLoadingFlat || rt.ToolSearchEnabled || rt.NativeDeferredToolDiscovery {
		t.Fatalf("compatible provider should reset to flat loading, mode=%q search=%v native=%v", rt.ToolLoadingMode, rt.ToolSearchEnabled, rt.NativeDeferredToolDiscovery)
	}
	if rt.Toolkit.ToolSearchEnabled() || rt.Toolkit.NativeDeferredToolDiscovery() || rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("toolkit and runner retained native discovery after provider switch")
	}
	if rt.DeferredToolCatalogPrompt != "" {
		t.Fatalf("flat loading retained deferred catalog: %q", rt.DeferredToolCatalogPrompt)
	}
}

func TestNewSessionAutoFallsBackToFlatForUnsupportedFirstPartyOpenAIResponsesModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "openai",
			Providers: map[string]config.ProviderConfig{
				"openai": {
					Type:    "openai-compatible",
					BaseURL: "https://api.openai.com/v1",
					APIKey:  "abc",
					Model:   "gpt-test",
					WireAPI: "responses",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.ToolLoadingMode != config.ToolLoadingFlat {
		t.Fatalf("ToolLoadingMode = %q, want flat", rt.ToolLoadingMode)
	}
	if rt.Toolkit == nil || rt.Toolkit.ToolSearchEnabled() || rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatalf("unsupported OpenAI model should declare tools flat, tool_search=%v native=%v", rt.Toolkit.ToolSearchEnabled(), rt.Toolkit.NativeDeferredToolDiscovery())
	}
	if rt.StreamRunner == nil || rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("unsupported OpenAI model must not mark provider requests as native deferred")
	}
	// A flat surface declares every tool up front, so the deferred catalog
	// prompt that Wuu progressive loading needed must not be emitted at all.
	for _, unwanted := range []string{"# Deferred Tool Catalog", "<available-deferred-tools>"} {
		if strings.Contains(rt.BaseSystemPrompt, unwanted) {
			t.Fatalf("flat fallback must not ship the deferred catalog prompt, found %q:\n%s", unwanted, rt.BaseSystemPrompt)
		}
	}
	defs := rt.Toolkit.Definitions()
	if _, ok := sessionToolDefByName(defs, "tool_search"); ok {
		t.Fatalf("flat fallback must not expose tool_search, got %+v", defs)
	}
	for _, block := range rt.Toolkit.ContextBlocks() {
		if block.Kind == wuucontext.BlockAvailableDeferred {
			t.Fatalf("deferred catalog must not be emitted as request-only context: %+v", block)
		}
	}
}

func TestNewSessionAutoFlattensCompatibleOpenAIResponses(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "compatible",
			Providers: map[string]config.ProviderConfig{
				"compatible": {
					Type:    "openai-compatible",
					BaseURL: "https://compatible.example.com/v1",
					APIKey:  "abc",
					Model:   "gpt-5.4",
					WireAPI: "responses",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.ToolLoadingMode != config.ToolLoadingFlat {
		t.Fatalf("ToolLoadingMode = %q, want flat", rt.ToolLoadingMode)
	}
	if rt.Toolkit == nil {
		t.Fatal("expected toolkit")
	}
	if rt.Toolkit.ToolSearchEnabled() || rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatalf("compatible OpenAI Responses auto mode should use flat tools, tool_search=%v native=%v", rt.Toolkit.ToolSearchEnabled(), rt.Toolkit.NativeDeferredToolDiscovery())
	}
	if rt.StreamRunner == nil || rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("compatible OpenAI Responses auto mode should not mark provider requests as native deferred")
	}
	defs := rt.Toolkit.Definitions()
	if _, ok := sessionToolDefByName(defs, "tool_search"); ok {
		t.Fatalf("compatible OpenAI Responses flat mode should hide tool_search, got %+v", defs)
	}
	if _, ok := sessionToolDefByName(defs, "send_message"); ok {
		t.Fatalf("compatible OpenAI Responses flat mode must not expose plugin-owned send_message in the core toolkit, got %+v", defs)
	}
}

func TestNewSessionAllowsCompatibleEndpointToOptIntoNativeDeferred(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "anthropic",
			Providers: map[string]config.ProviderConfig{
				"anthropic": {
					Type:    "anthropic",
					BaseURL: "https://compatible.example.com/anthropic",
					APIKey:  "abc",
					Model:   "claude-sonnet-4-5",
				},
			},
			Agent: config.AgentConfig{ToolLoading: config.ToolLoadingNative},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.Toolkit == nil || !rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatal("explicit native tool_loading should enable native deferred loading on compatible Anthropic endpoints")
	}
	if rt.StreamRunner == nil || !rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("explicit native tool_loading should mark compatible Anthropic provider requests as native deferred")
	}
}

func TestNewSessionAllowsCompatibleOpenAIResponsesToOptIntoNativeDeferred(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "compatible",
			Providers: map[string]config.ProviderConfig{
				"compatible": {
					Type:    "openai-compatible",
					BaseURL: "https://compatible.example.com/v1",
					APIKey:  "abc",
					Model:   "gpt-5.4",
					WireAPI: "responses",
				},
			},
			Agent: config.AgentConfig{ToolLoading: config.ToolLoadingNative},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.ToolLoadingMode != config.ToolLoadingNative {
		t.Fatalf("ToolLoadingMode = %q, want native", rt.ToolLoadingMode)
	}
	if rt.Toolkit == nil {
		t.Fatal("expected toolkit")
	}
	if !rt.Toolkit.ToolSearchEnabled() || !rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatalf("explicit native should enable native tool loading, tool_search=%v native=%v", rt.Toolkit.ToolSearchEnabled(), rt.Toolkit.NativeDeferredToolDiscovery())
	}
	if rt.StreamRunner == nil || !rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("explicit native should mark compatible OpenAI provider requests as native deferred")
	}
}

// captureUnsupportedNativeWarnings redirects the fallback notice into buf and
// clears the process-level dedupe set so each test starts from a clean slate.
func captureUnsupportedNativeWarnings(t *testing.T, buf *bytes.Buffer) func() {
	t.Helper()
	previous := unsupportedNativeWarnWriter
	unsupportedNativeWarnWriter = buf
	resetUnsupportedNativeWarnings()
	return func() {
		unsupportedNativeWarnWriter = previous
		resetUnsupportedNativeWarnings()
	}
}

func TestNewSessionExplicitNativeFallsBackToFlatForUnsupportedOpenAIResponsesModel(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	var warnings bytes.Buffer
	restore := captureUnsupportedNativeWarnings(t, &warnings)
	defer restore()

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "compatible",
			Providers: map[string]config.ProviderConfig{
				"compatible": {
					Type:    "openai-compatible",
					BaseURL: "https://compatible.example.com/v1",
					APIKey:  "abc",
					Model:   "gpt-test",
					WireAPI: "responses",
				},
			},
			Agent: config.AgentConfig{ToolLoading: config.ToolLoadingNative},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	if rt.ToolLoadingMode != config.ToolLoadingFlat {
		t.Fatalf("ToolLoadingMode = %q, want flat", rt.ToolLoadingMode)
	}
	if rt.Toolkit == nil || rt.Toolkit.ToolSearchEnabled() || rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatalf("unsupported explicit OpenAI native should fall back to flat, tool_search=%v native=%v", rt.Toolkit.ToolSearchEnabled(), rt.Toolkit.NativeDeferredToolDiscovery())
	}
	if rt.StreamRunner == nil || rt.StreamRunner.NativeDeferredToolDiscovery {
		t.Fatal("unsupported explicit OpenAI native should not mark provider requests as native deferred")
	}
	// The user asked for deferred tools and is not getting them, so the
	// downgrade has to be visible rather than silent.
	notice := warnings.String()
	for _, want := range []string{"native", "flat", "gpt-test"} {
		if !strings.Contains(notice, want) {
			t.Fatalf("fallback notice missing %q, got %q", want, notice)
		}
	}
}

func TestUnsupportedNativeFallbackNoticeIsEmittedOncePerProviderModel(t *testing.T) {
	var warnings bytes.Buffer
	restore := captureUnsupportedNativeWarnings(t, &warnings)
	defer restore()

	providerCfg := config.ProviderConfig{
		Type:    "openai-compatible",
		BaseURL: "https://compatible.example.com/v1",
		APIKey:  "abc",
		WireAPI: "responses",
	}
	for range 3 {
		resolveToolLoadingModeForProvider(config.ToolLoadingNative, providerCfg, "gpt-test", nil)
	}
	if got := strings.Count(warnings.String(), "does not support provider-native"); got != 1 {
		t.Fatalf("notice count = %d, want 1 (dedupe across repeated provider switches):\n%s", got, warnings.String())
	}

	// A different unsupported pair is a different problem and reports again.
	resolveToolLoadingModeForProvider(config.ToolLoadingNative, providerCfg, "gpt-other", nil)
	if got := strings.Count(warnings.String(), "does not support provider-native"); got != 2 {
		t.Fatalf("notice count = %d, want 2 after switching model:\n%s", got, warnings.String())
	}
}

func TestNewSessionTreatsRetiredWuuToolSearchConfigAsAuto(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "generic",
			Providers: map[string]config.ProviderConfig{
				"generic": {
					Type:    "openai-compatible",
					BaseURL: "https://example.com/v1",
					APIKey:  "abc",
					Model:   "generic-coder",
				},
			},
			// An existing config still naming the removed mode must keep
			// starting rather than failing validation.
			Agent: config.AgentConfig{ToolLoading: config.ToolLoadingMode("wuu_tool_search")},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	// A generic compatible endpoint has no native discovery, so auto lands on
	// flat — the retired value no longer selects a loading strategy of its own.
	if rt.ToolLoadingPreference != config.ToolLoadingAuto {
		t.Fatalf("ToolLoadingPreference = %q, want auto", rt.ToolLoadingPreference)
	}
	if rt.ToolLoadingMode != config.ToolLoadingFlat {
		t.Fatalf("ToolLoadingMode = %q, want flat", rt.ToolLoadingMode)
	}
	if rt.Toolkit == nil {
		t.Fatal("expected toolkit")
	}
	if rt.Toolkit.ToolSearchEnabled() || rt.Toolkit.NativeDeferredToolDiscovery() {
		t.Fatalf("retired mode must not resurrect progressive loading, tool_search=%v native=%v", rt.Toolkit.ToolSearchEnabled(), rt.Toolkit.NativeDeferredToolDiscovery())
	}
	defs := rt.Toolkit.Definitions()
	if _, ok := sessionToolDefByName(defs, "tool_search"); ok {
		t.Fatalf("retired mode must not expose tool_search, got %+v", defs)
	}
	if _, ok := sessionToolDefByName(defs, "send_message"); ok {
		t.Fatalf("retired mode must not expose plugin-owned send_message in the core toolkit, got %+v", defs)
	}
}

func TestNewSessionFlattensToolSurfaceWhenToolLoadingFlat(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "generic",
			Providers: map[string]config.ProviderConfig{
				"generic": {
					Type:    "openai-compatible",
					BaseURL: "https://example.com/v1",
					APIKey:  "abc",
					Model:   "generic-coder",
				},
			},
			Agent: config.AgentConfig{ToolLoading: config.ToolLoadingFlat},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}
	defs := map[string]bool{}
	for _, def := range rt.Toolkit.Definitions() {
		defs[def.Name] = true
	}
	if defs["tool_search"] {
		t.Fatalf("flat surface should hide tool_search: %+v", defs)
	}
	if strings.Contains(rt.BaseSystemPrompt, "# Tool Discovery") {
		t.Fatalf("flat surface should not include tool_search guidance:\n%s", rt.BaseSystemPrompt)
	}
}

func TestNewSessionResolvesRoleModelSelections(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "custom",
			Providers: map[string]config.ProviderConfig{
				"custom": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "main-model",
					Models: map[string]config.ProviderModelConfig{
						"title-alias": {
							ID: "title-api-model",
						},
						"worker-alias": {
							ID: "worker-api-model",
							Variants: map[string]map[string]any{
								"deep": {"reasoningEffort": "high"},
							},
						},
					},
				},
			},
			Agent: config.AgentConfig{
				ModelRoles: config.ModelRolesConfig{
					Title:  config.ModelRoleConfig{Model: "title-alias", Effort: "low"},
					Worker: config.ModelRoleConfig{Model: "worker-alias", Variant: "deep"},
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	if rt.StreamRunner.Model != "main-model" {
		t.Fatalf("main runner model = %q", rt.StreamRunner.Model)
	}
	if rt.ModelRoles.Title.Inherited || rt.ModelRoles.Title.APIModel != "title-api-model" || rt.ModelRoles.Title.LegacyEffort != "low" {
		t.Fatalf("unexpected title role: %+v", rt.ModelRoles.Title)
	}
	if rt.ModelRoles.Worker.Inherited || rt.ModelRoles.Worker.APIModel != "worker-api-model" || rt.ModelRoles.Worker.Variant != "deep" {
		t.Fatalf("unexpected worker role: %+v", rt.ModelRoles.Worker)
	}
	if got := rt.ModelRoles.Worker.ProviderOptions["reasoningEffort"]; got != "high" {
		t.Fatalf("worker role provider options = %#v", rt.ModelRoles.Worker.ProviderOptions)
	}
	if rt.TitleClient == nil || rt.WorkerClient == nil {
		t.Fatalf("expected title and worker clients to be configured: title=%T worker=%T", rt.TitleClient, rt.WorkerClient)
	}
}

func TestNewSessionAppliesPermissionBoundary(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "custom",
			Providers: map[string]config.ProviderConfig{
				"custom": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "main-model",
				},
			},
			Agent: config.AgentConfig{
				PermissionMode: config.PermissionModeReadOnly,
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	_, err = rt.Toolkit.Execute(context.Background(), providers.ToolCall{
		Name:      "write_file",
		Arguments: `{"path":"blocked.txt","content":"nope"}`,
	})
	if err == nil || !strings.Contains(err.Error(), "error_kind=boundary_denied") {
		t.Fatalf("expected read-only runtime boundary, got %v", err)
	}
}

func TestNewThreadRuntimeWorkerInheritsCurrentPermissionBoundary(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	permissions := config.ResolvedPermissions{Mode: config.PermissionModeReadOnly}
	rt.Permissions = permissions
	ConfigureToolkitPermissions(rt.Toolkit, rt.Permissions)

	client := &sessionRecordingClient{
		streamBatches: [][]providers.StreamEvent{
			{
				{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "call-patch", Name: "apply_patch"}},
				{Type: providers.EventToolUseDelta, Content: `{"patchText":"*** Begin Patch\n*** Add File: blocked.txt\n+nope\n*** End Patch\n"}`},
				{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: "call-patch", Name: "apply_patch"}},
				{Type: providers.EventDone},
			},
			{
				{Type: providers.EventContentDelta, Content: "done"},
				{Type: providers.EventDone},
			},
		},
	}
	rt.WorkerClient = client
	threadRT, err := rt.NewThreadRuntime("thread-read-only-worker")
	if err != nil {
		t.Fatalf("NewThreadRuntime: %v", err)
	}
	defer func() {
		threadRT.AgentControl.StopAll()
		time.Sleep(100 * time.Millisecond)
	}()
	if _, err := threadRT.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "inspect_repo",
		Prompt:      "inspect the repo",
		Synchronous: true,
	}); err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	req := client.LastRequest()
	var joined strings.Builder
	for _, msg := range req.Messages {
		joined.WriteString(msg.Content)
		joined.WriteByte('\n')
	}
	content := joined.String()
	if !strings.Contains(content, "boundary_denied") ||
		!strings.Contains(content, "this agent is read-only") {
		t.Fatalf("worker did not inherit read-only permission boundary:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(root, "blocked.txt")); !os.IsNotExist(err) {
		t.Fatalf("read-only worker should not create blocked file, stat err=%v", err)
	}
}

// TestThreadWorkerWakeAppliesCurrentPermissionBoundary locks the wake side of
// permission propagation: a COMPLETED worker woken by a follow-up must run
// under the permissions in force at wake time, in both directions.
// Regression: the worker toolkit used to keep the boundary captured at spawn,
// so tightening the thread to read-only left a woken worker writable.
func TestThreadWorkerWakeAppliesCurrentPermissionBoundary(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	writeCall := func(id, path string) []providers.StreamEvent {
		patch := `{"patchText":"*** Begin Patch\n*** Add File: ` + path + `\n+payload\n*** End Patch\n"}`
		return []providers.StreamEvent{
			{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: id, Name: "apply_patch"}},
			{Type: providers.EventToolUseDelta, Content: patch},
			{Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: id, Name: "apply_patch"}},
			{Type: providers.EventDone},
		}
	}
	turnDone := []providers.StreamEvent{
		{Type: providers.EventContentDelta, Content: "done"},
		{Type: providers.EventDone},
	}
	client := &sessionRecordingClient{
		streamBatches: [][]providers.StreamEvent{
			turnDone,                              // spawn turn: complete without touching files
			writeCall("call-w1", "tightened.txt"), // wake 1 under read-only: must be denied
			turnDone,
			writeCall("call-w2", "loosened.txt"), // wake 2 under standard: must succeed
			turnDone,
		},
	}
	rt.WorkerClient = client
	threadRT, err := rt.NewThreadRuntime("thread-wake-permissions")
	if err != nil {
		t.Fatalf("NewThreadRuntime: %v", err)
	}
	defer func() {
		threadRT.AgentControl.StopAll()
		time.Sleep(100 * time.Millisecond)
	}()

	// Spawn under the standard boundary and let the worker complete.
	spawned, err := threadRT.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type:        agentcontrol.DefaultSubagentType,
		TaskName:    "wake_permissions",
		Prompt:      "idle worker",
		Synchronous: true,
	})
	if err != nil {
		t.Fatalf("Spawn: %v", err)
	}

	// Tighten the thread to read-only AFTER the worker completed — the same
	// reapply the app-server performs on the thread toolkit at turn start.
	ConfigureToolkitPermissions(threadRT.Toolkit, config.ResolvedPermissions{Mode: config.PermissionModeReadOnly})

	if _, err := threadRT.AgentControl.FollowupTask(context.Background(), spawned.AgentID, "write tightened.txt"); err != nil {
		t.Fatalf("FollowupTask (read-only): %v", err)
	}
	snap, err := threadRT.AgentControl.Wait(context.Background(), spawned.AgentID)
	if err != nil {
		t.Fatalf("Wait (read-only wake): %v", err)
	}
	if snap.Status != subagent.StatusCompleted {
		t.Fatalf("read-only wake turn = %+v, want completed", snap)
	}
	req := client.LastRequest()
	var joined strings.Builder
	for _, msg := range req.Messages {
		joined.WriteString(msg.Content)
		joined.WriteByte('\n')
	}
	content := joined.String()
	if !strings.Contains(content, "boundary_denied") ||
		!strings.Contains(content, "this agent is read-only") {
		t.Fatalf("woken worker kept its spawn-time writable boundary:\n%s", content)
	}
	if _, err := os.Stat(filepath.Join(root, "tightened.txt")); !os.IsNotExist(err) {
		t.Fatalf("woken read-only worker should not create tightened.txt, stat err=%v", err)
	}

	// Loosen back to standard — waking must propagate that direction too.
	ConfigureToolkitPermissions(threadRT.Toolkit, config.ResolvedPermissions{Mode: config.PermissionModeStandard})

	if _, err := threadRT.AgentControl.FollowupTask(context.Background(), spawned.AgentID, "write loosened.txt"); err != nil {
		t.Fatalf("FollowupTask (standard): %v", err)
	}
	snap, err = threadRT.AgentControl.Wait(context.Background(), spawned.AgentID)
	if err != nil {
		t.Fatalf("Wait (standard wake): %v", err)
	}
	if snap.Status != subagent.StatusCompleted {
		t.Fatalf("standard wake turn = %+v, want completed", snap)
	}
	data, err := os.ReadFile(filepath.Join(root, "loosened.txt"))
	if err != nil {
		t.Fatalf("woken worker kept the tightened boundary after loosening: %v", err)
	}
	if !strings.Contains(string(data), "payload") {
		t.Fatalf("loosened.txt content = %q, want it to contain %q", string(data), "payload")
	}
}

func TestSessionRefreshSystemPromptUpdatesRunnerPrompt(t *testing.T) {
	root := t.TempDir()
	kit, err := tools.New(root)
	if err != nil {
		t.Fatalf("tools.New: %v", err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", false)
	rt := &Session{
		RootDir:      root,
		StreamRunner: &agent.StreamRunner{SystemPrompt: "old prompt"},
		Toolkit:      kit,
	}

	prompt := rt.RefreshSystemPrompt("openai", "gpt-5-codex")

	for _, want := range []string{
		"[Tool surface: openai_codex]",
		"Use apply_patch for file changes and bash for command execution",
	} {
		if !strings.Contains(prompt, want) {
			t.Fatalf("refreshed prompt missing %q:\n%s", want, prompt)
		}
	}
	if rt.BaseSystemPrompt != prompt || rt.StreamRunner.SystemPrompt != prompt {
		t.Fatalf("refresh should update session and runner prompts")
	}
	if strings.Contains(prompt, "old prompt") {
		t.Fatalf("refreshed prompt should not keep stale runner prompt:\n%s", prompt)
	}
	for _, duplicated := range []string{"# Harness Adapter", "Provider/model:"} {
		if strings.Contains(prompt, duplicated) {
			t.Fatalf("refreshed prompt should not expose provider-brand adapter text %q:\n%s", duplicated, prompt)
		}
	}
}

func TestDiscoverInstructionsHonorsLegacyOptIn(t *testing.T) {
	home := t.TempDir()
	root := filepath.Join(home, "repo")
	if err := os.MkdirAll(filepath.Join(root, ".git"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(filepath.Join(root, ".claude"), 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(root, ".claude", "CLAUDE.md"), []byte("legacy project rule"), 0o644); err != nil {
		t.Fatal(err)
	}

	if files := discoverInstructions(root, home, config.InstructionFilesConfig{}); len(files) != 0 {
		t.Fatalf("default runtime instruction discovery should skip legacy files, got %+v", files)
	}

	includeLegacy := true
	files := discoverInstructions(root, home, config.InstructionFilesConfig{IncludeLegacyInstructions: &includeLegacy})
	if len(files) != 1 || files[0].Content != "legacy project rule" {
		t.Fatalf("legacy opt-in did not load legacy file: %+v", files)
	}
}

func TestDiscoverInstructionsKeepsUserGlobalFilesAndIgnoresProjectRedirect(t *testing.T) {
	home := t.TempDir()
	wuuHome := filepath.Join(home, ".wuu")
	root := filepath.Join(home, "repo")
	t.Setenv("WUU_HOME", wuuHome)
	if err := os.MkdirAll(wuuHome, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.MkdirAll(root, 0o755); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(wuuHome, "GLOBAL.md"), []byte("trusted global memory"), 0o600); err != nil {
		t.Fatal(err)
	}
	sshDir := filepath.Join(home, ".ssh")
	if err := os.MkdirAll(sshDir, 0o700); err != nil {
		t.Fatal(err)
	}
	if err := os.WriteFile(filepath.Join(sshDir, "id_rsa"), []byte("must not be loaded"), 0o600); err != nil {
		t.Fatal(err)
	}
	userConfig, err := json.Marshal(config.Config{
		DefaultProvider: "main",
		Providers: map[string]config.ProviderConfig{
			"main": {
				Type:      "openai-compatible",
				BaseURL:   "https://trusted.example/v1",
				APIKeyEnv: "TRUSTED_KEY",
				Model:     "trusted-model",
			},
		},
		Instructions: config.InstructionFilesConfig{
			Filenames: []string{"GLOBAL.md"},
			UserDirs:  []string{wuuHome},
		},
	})
	if err != nil {
		t.Fatalf("marshal user config: %v", err)
	}
	if err := os.WriteFile(filepath.Join(wuuHome, "config.json"), userConfig, 0o600); err != nil {
		t.Fatal(err)
	}
	projectConfig := `{
  "memory": {
    "filenames": ["id_rsa"],
    "user_dirs": ["~/.ssh"],
    "project_root_markers": ["Users"],
    "include_legacy_memory": true
  }
}`
	if err := os.WriteFile(filepath.Join(root, ".wuu.json"), []byte(projectConfig), 0o644); err != nil {
		t.Fatal(err)
	}

	cfg, _, err := config.LoadFrom(root, home)
	if err != nil {
		t.Fatalf("LoadFrom: %v", err)
	}
	files := discoverInstructions(root, home, cfg.Instructions)
	if len(files) != 1 || files[0].Content != "trusted global memory" || files[0].Path != filepath.Join(wuuHome, "GLOBAL.md") {
		t.Fatalf("unexpected memory files: %+v", files)
	}
	for _, file := range files {
		if strings.Contains(file.Content, "must not be loaded") || strings.Contains(file.Path, ".ssh") {
			t.Fatalf("project redirected global instruction discovery: %+v", files)
		}
	}
}

func TestEnvironmentSystemPromptSectionFreezesSessionDate(t *testing.T) {
	root := t.TempDir()
	const frozen = "2020-01-02"

	section := environmentSystemPromptSection(root, frozen)
	if !strings.Contains(section, "- Current date: "+frozen) {
		t.Fatalf("frozen session date not stamped into environment section:\n%s", section)
	}
	today := time.Now().Format("2006-01-02")
	if today != frozen && strings.Contains(section, today) {
		t.Fatalf("frozen environment section leaked today's date %q:\n%s", today, section)
	}

	// Standalone callers pass an empty session date and fall back to the
	// current date rather than emitting a blank field.
	fallback := environmentSystemPromptSection(root, "")
	if !strings.Contains(fallback, "- Current date: "+today) {
		t.Fatalf("empty session date should fall back to today %q:\n%s", today, fallback)
	}
}

// TestSystemPromptForThreadRootKeepsFrozenDate proves the cache-critical
// property: rebuilding a thread's system prompt rebases the CWD onto the thread
// root but keeps the session-start frozen date, so the cached system prefix
// stays byte-stable across thread rebuilds instead of churning on the wall
// clock (e.g. a session that crosses midnight).
func TestSystemPromptForThreadRootKeepsFrozenDate(t *testing.T) {
	sessionRoot := t.TempDir()
	threadRoot := t.TempDir()
	const frozen = "2019-07-05"

	base := buildBaseSystemPromptResult(
		sessionRoot,
		frozen,
		"base prompt",
		"",
		"anthropic",
		"claude-test",
		capability.Surface{},
		nil,
		"",
		"",
		nil,
	)
	sections := agentPromptSections(base.Sections)

	rewritten, _ := systemPromptForThreadRoot(base.Content, sections, threadRoot, frozen)
	if !strings.Contains(rewritten, "- Current date: "+frozen) {
		t.Fatalf("thread rebuild dropped the frozen session date:\n%s", rewritten)
	}
	if !strings.Contains(rewritten, "- Current working directory: "+threadRoot) {
		t.Fatalf("thread rebuild did not rebase CWD to %q:\n%s", threadRoot, rewritten)
	}

	// A second rebuild with the same frozen date is byte-identical: no drift.
	rewritten2, _ := systemPromptForThreadRoot(base.Content, sections, threadRoot, frozen)
	if rewritten != rewritten2 {
		t.Fatalf("thread system prompt drifted across identical rebuilds")
	}
}

func TestBuildBaseSystemPromptNoToolsSkipsToolLoadedGuidance(t *testing.T) {
	promptText := buildBaseSystemPrompt(
		t.TempDir(),
		"base prompt",
		"",
		"openai-codex",
		"gpt-5.5",
		capability.Surface{},
		nil,
		"",
		"",
		[]skills.Skill{{Name: "commit", Description: "Create a commit."}},
	)

	for _, bad := range []string{
		"Skills provide specialized instructions",
		"<available_skills>",
		"Create a commit",
		"Workflow guidance",
		"Release workflow",
		"`start_workflow`",
		"Tool Discovery",
	} {
		if strings.Contains(promptText, bad) {
			t.Fatalf("no-tools prompt should not advertise tool-loaded guidance %q:\n%s", bad, promptText)
		}
	}
}

func TestBuildBaseSystemPromptAddsCatalogForToolSearchSurface(t *testing.T) {
	surface := compiledSurfaceForProviderModel("openai", "gpt-5-codex")
	surface.DeferredToolCatalog = "# Deferred Tool Catalog\n\n<available-deferred-tools>\n- await_agents: Wait for helper agents. [tags: agent]\n</available-deferred-tools>"
	promptText := buildBaseSystemPrompt(
		t.TempDir(),
		"base prompt",
		"",
		"openai",
		"gpt-5-codex",
		surface,
		nil,
		"",
		"",
		nil,
	)

	for _, want := range []string{
		"# Deferred Tool Catalog",
		"<available-deferred-tools>",
		"await_agents: Wait for helper agents.",
	} {
		if !strings.Contains(promptText, want) {
			t.Fatalf("tool-search surface prompt missing %q:\n%s", want, promptText)
		}
	}
	for _, duplicatedManual := range []string{
		"# Tool Discovery",
		"select:<tool_name>",
		"Do not use `tool_search` for visible core tools",
	} {
		if strings.Contains(promptText, duplicatedManual) {
			t.Fatalf("tool-search usage belongs to its tool schema, not the system prompt %q:\n%s", duplicatedManual, promptText)
		}
	}
	for _, bad := range []string{
		"especially MCP tools, workflows",
		"workflows, scheduling",
		"task-handling options inside wuu",
		"direct work, subagents, or workflows",
		"workflows only when",
		"matching saved workflow",
	} {
		if strings.Contains(promptText, bad) {
			t.Fatalf("main prompt should not include generic workflow guidance %q:\n%s", bad, promptText)
		}
	}
}

func TestBuildBaseSystemPromptFiltersSkillsBySurface(t *testing.T) {
	surface := compiledSurfaceForProviderModel("ollama", "llama-coder")
	promptText := buildBaseSystemPrompt(
		t.TempDir(),
		"base prompt",
		"",
		"ollama",
		"llama-coder",
		surface,
		nil,
		"",
		"",
		[]skills.Skill{
			{
				Name:         "commit",
				Description:  "Create a commit.",
				WhenToUse:    "Use when asked to commit.",
				Content:      "Use bash to run git status.",
				AllowedTools: []string{"bash"},
			},
			{
				Name:         "misdeclared-shell",
				Description:  "Misdeclared shell workflow.",
				WhenToUse:    "Use when asked to inspect a repo.",
				Content:      "Git: run git-status before continuing.",
				AllowedTools: []string{"read_file"},
			},
			{
				Name:         "claude-style-shell",
				Description:  "Claude style tool declaration.",
				WhenToUse:    "Use when asked to inspect terminal output.",
				Content:      "Run the command.",
				AllowedTools: []string{"Bash(git status:*)"},
			},
			{
				Name:         "implementation-plan",
				Description:  "Plan the implementation.",
				WhenToUse:    "Use before broad edits.",
				Content:      "Create a scoped plan.",
				AllowedTools: []string{"read_file", "grep", "glob"},
			},
		},
	)

	if strings.Contains(promptText, "Create a commit") ||
		strings.Contains(promptText, "misdeclared-shell") ||
		strings.Contains(promptText, "claude-style-shell") ||
		strings.Contains(promptText, "Git:") ||
		strings.Contains(promptText, "git-status") ||
		strings.Contains(promptText, "Use bash to run git status") {
		t.Fatalf("local/no-shell prompt must not advertise terminal-only skills:\n%s", promptText)
	}
	if !strings.Contains(promptText, "implementation-plan") {
		t.Fatalf("local/no-shell prompt should keep compatible skills:\n%s", promptText)
	}
}

func TestBuildBaseSystemPromptDoesNotInjectLegacyWorkflowGuidance(t *testing.T) {
	surface := capability.Surface{
		ProfileName:    "portable_no_shell",
		Tools:          map[string]capability.Capability{"read_file": capability.CapabilityFileRead},
		Capabilities:   []capability.Capability{capability.CapabilityFileRead},
		SystemFragment: "Portable no-shell profile.",
	}
	promptText := buildBaseSystemPrompt(
		t.TempDir(),
		"base prompt",
		"",
		"ollama",
		"llama-coder",
		surface,
		nil,
		"",
		"",
		nil,
	)

	for _, bad := range []string{
		"Workflow guidance",
		"# Workflow orchestration",
		"Workflow catalog",
		"`start_workflow`",
		"- start_workflow:",
		"terminal-release",
		"portable-plan",
		"Plan a portable change.",
		"Git: release workflow.",
		"Use for planning.",
	} {
		if strings.Contains(promptText, bad) {
			t.Fatalf("workflow guidance must not be injected for legacy workflow-capable surfaces; found %q:\n%s", bad, promptText)
		}
	}
}

func TestBuildBaseSystemPromptLocalNoShellDoesNotTeachTerminalPaths(t *testing.T) {
	surface := compiledSurfaceForProviderModel("ollama", "llama-coder")
	promptText := buildBaseSystemPrompt(
		t.TempDir(),
		config.DefaultSystemPrompt(),
		"",
		"ollama",
		"llama-coder",
		surface,
		nil,
		"",
		"",
		nil,
	)

	for _, banned := range []string{
		"bash",
		"run_shell",
		"run_test",
		"start_process",
		"command.bash",
		"terminal",
		"shell",
		"git",
		"git status",
		"git diff",
		"git commit",
		"npx vitest",
		"npm test",
		"npm run dev",
	} {
		if strings.Contains(promptText, banned) {
			t.Fatalf("local/no-shell prompt must not teach terminal path %q:\n%s", banned, promptText)
		}
	}
}

// TestBuildBaseSystemPrompt_WorkerExcludesMainOnlyCoordination locks in the
// split between prompts.System() (invariants shared with workers) and
// prompts.SystemMain() (the small coordination contract used only by the main
// agent). Tool-specific manuals belong to the active tool surface instead.
// Subagent product guidance was extracted into the bundled Subagent plugin,
// so neither core prompt may carry it.
func TestBuildBaseSystemPrompt_WorkerExcludesMainOnlyCoordination(t *testing.T) {
	surface := compiledSurfaceForProviderModel("openai", "gpt-5")

	mainPrompt := buildBaseSystemPrompt(
		t.TempDir(),
		config.DefaultSystemPrompt(),
		"",
		"openai",
		"gpt-5",
		surface,
		nil, "", "", nil,
	)
	workerPrompt := buildBaseSystemPrompt(
		t.TempDir(),
		config.WorkerSystemPrompt(),
		"",
		"openai",
		"gpt-5",
		surface,
		nil, "", "", nil,
	)

	// Subagent results guidance moved to plugins/subagent; core prompts must
	// remain product-neutral.
	for _, banned := range []string{
		"# Subagent results",
		"completed subagent task does not mean the overall task is complete",
		"integrate the result and verify the overall work",
	} {
		if strings.Contains(mainPrompt, banned) {
			t.Fatalf("main agent prompt must not contain extracted Subagent guidance %q; got prompt:\n%s", banned, mainPrompt)
		}
		if strings.Contains(workerPrompt, banned) {
			t.Fatalf("worker prompt must not contain extracted Subagent guidance %q; got prompt:\n%s", banned, workerPrompt)
		}
	}
	// start_workflow is part of the legacy workflow suite and must not be
	// taught to ordinary project main agents.
	if strings.Contains(mainPrompt, "- start_workflow:") {
		t.Fatalf("project main prompt (no workflow capability) must not contain the start_workflow path bullet; got prompt:\n%s", mainPrompt)
	}

}

func TestResolveInputWindow_CapsCodexSubscriptionGPT5(t *testing.T) {
	got := ResolveInputWindow("gpt-5.5", config.ProviderConfig{
		Type:  "openai-codex",
		Model: "gpt-5.5",
	})
	if got != codexSubscriptionGPT5InputCap {
		t.Fatalf("ResolveInputWindow = %d, want %d", got, codexSubscriptionGPT5InputCap)
	}
}

func TestResolveInputWindow_CapsCodexSubscriptionGPT6Astra(t *testing.T) {
	got := ResolveInputWindow("gpt-6-astra", config.ProviderConfig{
		Type:  "openai-codex",
		Model: "gpt-6-astra",
	})
	if got != modelbudget.CodexSubscriptionGPT6InputCap {
		t.Fatalf("ResolveInputWindow = %d, want %d", got, modelbudget.CodexSubscriptionGPT6InputCap)
	}
}

func TestResolveWindowsFallBackToAPIModelLimits(t *testing.T) {
	provider := config.ProviderConfig{
		Type:  "openai-compatible",
		Model: "fast-alias",
		Models: map[string]config.ProviderModelConfig{
			"fast-alias": {
				ID: "base-model",
			},
			"base-model": {
				Limit: &config.ProviderModelLimitConfig{
					Context: 1_000_000,
					Input:   900_000,
				},
			},
		},
	}

	if got := ResolveContextWindow("fast-alias", provider, 0); got != 1_000_000 {
		t.Fatalf("ResolveContextWindow = %d, want 1000000", got)
	}
	if got := ResolveInputWindow("fast-alias", provider); got != 900_000 {
		t.Fatalf("ResolveInputWindow = %d, want 900000", got)
	}
}

func TestResolveInputWindowDoesNotSynthesizeFromContextWindow(t *testing.T) {
	got := ResolveInputWindow("private-1m-model", config.ProviderConfig{
		Type:          "anthropic",
		Model:         "private-1m-model",
		ContextWindow: 1_000_000,
	})
	if got != 0 {
		t.Fatalf("ResolveInputWindow = %d, want 0 without explicit input limit", got)
	}
}

func TestApplyWorkerToolFilter_HidesRecursiveAgentControls(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatalf("New toolkit: %v", err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", false)
	kit.SetAgentIdentity("worker-1", string(agentthread.RootPath)+"/worker-1")
	wt, err := agentcontrol.LookupWorkerType(agentcontrol.DefaultSubagentType)
	if err != nil {
		t.Fatalf("agent type: %v", err)
	}

	applyWorkerToolFilter(kit, wt)

	defs := map[string]bool{}
	for _, def := range kit.Definitions() {
		defs[def.Name] = true
	}
	for _, allowed := range []string{"read_file", "apply_patch", "bash"} {
		if !defs[allowed] {
			t.Fatalf("subagent toolkit should keep %s", allowed)
		}
	}
	for _, blocked := range []string{"spawn_agent", "send_message", "followup_task", "await_agents", "close_agent", "list_agents"} {
		if defs[blocked] {
			t.Fatalf("subagent toolkit should hide recursive control tool %s", blocked)
		}
	}
}

func TestApplyWorkerToolFilter_RestrictedWorkerKeepsBashFirstSurface(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatalf("New toolkit: %v", err)
	}
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", false)
	kit.SetAgentIdentity("worker-1", string(agentthread.RootPath)+"/worker-1")
	wt := agentcontrol.WorkerType{
		Name:         "restricted",
		AllowedTools: []string{"read_file", "grep", "glob", "bash", "agent_report"},
	}

	applyWorkerToolFilter(kit, wt)

	defs := map[string]bool{}
	for _, def := range kit.Definitions() {
		defs[def.Name] = true
	}
	for _, allowed := range []string{"read_file", "grep", "glob", "bash"} {
		if !defs[allowed] {
			t.Fatalf("restricted worker toolkit should keep %s; defs=%v", allowed, defs)
		}
	}
	for _, hidden := range []string{"run_shell", "run_test", "start_process", "git", "apply_patch", "edit_file", "write_file"} {
		if defs[hidden] {
			t.Fatalf("restricted worker toolkit should not expose %s; defs=%v", hidden, defs)
		}
	}
}

// TestWorkerDeferredToolCatalogPromptForToolkit locks in consistency-repair
// #13: worker prompts carry a deferred-tool catalog generated against the
// worker surface — deferred executor tools listed, the main-agent-only
// orchestration suite absent.
func TestWorkerDeferredToolCatalogPromptForToolkit(t *testing.T) {
	kit, err := tools.New(t.TempDir())
	if err != nil {
		t.Fatalf("New toolkit: %v", err)
	}
	// The session toolkit holds the MAIN surface; the helper must still
	// produce a worker-scoped catalog from it.
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5-codex", true)

	catalog, err := workerDeferredToolCatalogPromptForToolkit(kit, "openai", "gpt-5-codex", true)
	if err != nil {
		t.Fatalf("workerDeferredToolCatalogPromptForToolkit: %v", err)
	}
	if catalog == "" {
		t.Fatal("worker deferred tool catalog must not be empty when tool search is enabled")
	}
	for _, want := range []string{"thread_get"} {
		if !strings.Contains(catalog, want) {
			t.Errorf("worker catalog must list deferred executor tool %s:\n%s", want, catalog)
		}
	}
	for _, orchestration := range []string{"spawn_agent", "send_message", "followup_task", "await_agents", "close_agent", "list_agents"} {
		if strings.Contains(catalog, orchestration) {
			t.Errorf("worker catalog must not list orchestration tool %s:\n%s", orchestration, catalog)
		}
	}

	disabled, err := workerDeferredToolCatalogPromptForToolkit(kit, "openai", "gpt-5-codex", false)
	if err != nil {
		t.Fatalf("workerDeferredToolCatalogPromptForToolkit (tool search off): %v", err)
	}
	if disabled != "" {
		t.Fatalf("catalog must be empty when worker tool search is disabled, got:\n%s", disabled)
	}
}

func TestMCPToolOverridesFromConfig(t *testing.T) {
	readOnly := true
	concurrencySafe := false

	out := mcpToolOverrides(map[string]config.MCPToolOverride{
		"search": {
			ReadOnly:        &readOnly,
			ConcurrencySafe: &concurrencySafe,
			Capability:      capability.CapabilitySearchSemantic,
		},
	})

	override, ok := out["search"]
	if !ok {
		t.Fatal("missing converted override")
	}
	if override.ReadOnly == nil || *override.ReadOnly != true {
		t.Fatalf("ReadOnly = %v, want true", override.ReadOnly)
	}
	if override.ConcurrencySafe == nil || *override.ConcurrencySafe != false {
		t.Fatalf("ConcurrencySafe = %v, want false", override.ConcurrencySafe)
	}
	if override.Capability != capability.CapabilitySearchSemantic {
		t.Fatalf("Capability = %q, want %q", override.Capability, capability.CapabilitySearchSemantic)
	}
}

func TestBoundaryForMode(t *testing.T) {
	tests := []struct {
		mode           string
		wantEnforce    bool
		wantMutations  bool
		wantUnconfined bool
	}{
		{mode: "", wantEnforce: true, wantMutations: true},
		{mode: config.PermissionModeStandard, wantEnforce: true, wantMutations: true},
		{mode: config.PermissionModeReadOnly, wantEnforce: true, wantMutations: false},
		{mode: config.PermissionModeUnconfined, wantEnforce: false, wantMutations: true, wantUnconfined: true},
		{mode: "not-a-mode", wantEnforce: true, wantMutations: true},
	}
	for _, tt := range tests {
		got := BoundaryForMode(tt.mode)
		if got.Enforce != tt.wantEnforce || got.AllowMutations != tt.wantMutations {
			t.Fatalf("BoundaryForMode(%q) = %+v", tt.mode, got)
		}
	}
}

func TestNewThreadRuntimeCreatesIsolatedMutableRuntime(t *testing.T) {
	root := t.TempDir()
	home := t.TempDir()
	t.Setenv("WUU_HOME", filepath.Join(home, "state"))
	t.Setenv("TEST_WUU_KEY", "abc")

	rt, err := NewSession(Options{
		RootDir:    root,
		HomeDir:    home,
		ConfigPath: filepath.Join(root, ".wuu.json"),
		Config: config.Config{
			DefaultProvider: "test",
			Providers: map[string]config.ProviderConfig{
				"test": {
					Type:      "openai-compatible",
					BaseURL:   "https://example.test/v1",
					APIKeyEnv: "TEST_WUU_KEY",
					Model:     "gpt-test",
				},
			},
		},
	})
	if err != nil {
		t.Fatalf("NewSession: %v", err)
	}

	first, err := rt.NewThreadRuntime("thread-a")
	if err != nil {
		t.Fatalf("NewThreadRuntime first: %v", err)
	}
	second, err := rt.NewThreadRuntime("thread-b")
	if err != nil {
		t.Fatalf("NewThreadRuntime second: %v", err)
	}

	if rt.Toolkit.SessionID() != "" {
		t.Fatalf("base toolkit session should not be mutated, got %q", rt.Toolkit.SessionID())
	}
	if first.Toolkit == rt.Toolkit || second.Toolkit == rt.Toolkit || first.Toolkit == second.Toolkit {
		t.Fatal("thread runtimes must not share toolkit instances")
	}
	if first.Toolkit.SessionID() != "thread-a" || second.Toolkit.SessionID() != "thread-b" {
		t.Fatalf("unexpected thread toolkit sessions: first=%q second=%q", first.Toolkit.SessionID(), second.Toolkit.SessionID())
	}
	if first.Toolkit.ActiveSurface().ProfileName == "" || first.Toolkit.ActiveSurface().ProfileName != rt.Toolkit.ActiveSurface().ProfileName {
		t.Fatalf("thread toolkit should inherit active surface, got %q want %q", first.Toolkit.ActiveSurface().ProfileName, rt.Toolkit.ActiveSurface().ProfileName)
	}
	if first.StreamRunner == rt.StreamRunner || second.StreamRunner == rt.StreamRunner || first.StreamRunner == second.StreamRunner {
		t.Fatal("thread runtimes must not share stream runner instances")
	}
	if len(rt.BaseSystemPromptSections) == 0 {
		t.Fatal("base system prompt should expose section metadata")
	}
	if len(first.StreamRunner.SystemPromptSections) != len(rt.BaseSystemPromptSections) {
		t.Fatalf("thread stream runner lost system prompt sections: got %d want %d", len(first.StreamRunner.SystemPromptSections), len(rt.BaseSystemPromptSections))
	}
	if first.StreamRunner.SystemPromptSections[0].Key != rt.BaseSystemPromptSections[0].Key ||
		first.StreamRunner.SystemPromptSections[0].Hash != rt.BaseSystemPromptSections[0].Hash {
		t.Fatalf("thread stream runner copied wrong system prompt section: got %+v want %+v", first.StreamRunner.SystemPromptSections[0], rt.BaseSystemPromptSections[0])
	}
	if first.StreamRunner.PromptCacheKey != "thread-a" || second.StreamRunner.PromptCacheKey != "thread-b" {
		t.Fatalf("unexpected thread prompt cache keys: first=%q second=%q", first.StreamRunner.PromptCacheKey, second.StreamRunner.PromptCacheKey)
	}
	if first.AgentControl == nil || second.AgentControl == nil || first.AgentControl == second.AgentControl {
		t.Fatal("thread runtimes must have distinct agent control instances")
	}
	if first.AgentControl.SessionID() != "thread-a" || second.AgentControl.SessionID() != "thread-b" {
		t.Fatalf("unexpected agent control sessions: first=%q second=%q", first.AgentControl.SessionID(), second.AgentControl.SessionID())
	}
}

func TestMediaInputPolicyFromCapabilitiesPreservesUnknown(t *testing.T) {
	unknown := mediaInputPolicyFromCapabilities(modelroles.Capabilities{})
	if unknown.ImageKnown || unknown.FileKnown {
		t.Fatalf("missing modality evidence must remain unknown: %+v", unknown)
	}

	unsupported := mediaInputPolicyFromCapabilities(modelroles.Capabilities{
		ImageInputKnown: true,
		FileInputKnown:  true,
	})
	if !unsupported.ImageKnown || unsupported.Image || !unsupported.FileKnown || unsupported.File {
		t.Fatalf("explicit unsupported capabilities were not preserved: %+v", unsupported)
	}
}
