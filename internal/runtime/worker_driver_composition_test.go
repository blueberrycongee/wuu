package runtime

import (
	"context"
	"errors"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/subagent"
)

// Workers must retain accepted executions through retirement, persist their
// binding before effects, and reject removed or incompatible providers on
// follow-up and restart. A failed metadata write must not start inference.
func TestWorkerDriverComposition(t *testing.T) {
	for _, name := range []string{"resume", "changed-default", "legacy", "incompatible", "removed", "persist-failure"} {
		t.Run(name, func(t *testing.T) {
			s, _ := workerCompositionSession(t, name == "removed")
			client := &defaultBindingModel{}
			manager := subagent.NewManagerWithOptions(client, "controlled", subagent.ManagerOptions{AcquireLoopDriver: s.driverResolver(nil)})
			path := filepath.Join(t.TempDir(), "worker.json")
			if name == "persist-failure" {
				block := filepath.Join(t.TempDir(), "not-a-directory")
				if err := os.WriteFile(block, nil, 0o600); err != nil {
					t.Fatal(err)
				}
				path = filepath.Join(block, "worker.json")
			}
			worker, err := manager.Spawn(context.Background(), subagent.SpawnOptions{ID: "worker", Prompt: "Say hello", Toolkit: workerBindingTools{}, HistoryPath: path})
			if err != nil {
				t.Fatal(err)
			}
			result, err := manager.Wait(context.Background(), worker.ID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "removed" || name == "persist-failure" {
				if result.Status != subagent.StatusFailed || result.Error == nil || client.calls != 0 {
					t.Fatalf("worker bypassed admission: result=%+v calls=%d", result, client.calls)
				}
				return
			}
			if result.Status != subagent.StatusCompleted || result.Result != "Hello" {
				t.Fatalf("worker result = %+v", result)
			}
			saved, err := subagent.LoadPersistedRun(path)
			if err != nil {
				t.Fatal(err)
			}
			if saved.DriverBinding == nil || saved.DriverBinding.ProviderID != loopdriver.DefaultDriverID || saved.DriverCheckpoint == nil || saved.DriverCheckpoint.DriverID != loopdriver.DefaultDriverID {
				t.Fatalf("worker lost its driver binding or checkpoint: %+v", saved)
			}
			if name == "incompatible" {
				saved.DriverBinding.DriverVersion = "99.0.0"
			}
			if name == "changed-default" || name == "legacy" {
				s.DriverProfile = "unavailable"
			}
			if name == "legacy" {
				saved.DriverBinding = nil
				saved.DriverCheckpoint = nil
			}
			restarted := subagent.NewManagerWithOptions(client, "controlled", subagent.ManagerOptions{AcquireLoopDriver: s.driverResolver(nil)})
			if _, err := restarted.Restore(subagent.RestoreOptions{Run: saved, Toolkit: workerBindingTools{}, HistoryPath: path}); err != nil {
				t.Fatal(err)
			}
			if _, err := restarted.Followup(context.Background(), worker.ID, "Say hello again"); err != nil {
				t.Fatal(err)
			}
			result, err = restarted.Wait(context.Background(), worker.ID)
			if err != nil {
				t.Fatal(err)
			}
			if name == "incompatible" {
				if result.Status != subagent.StatusFailed || result.Error == nil || !strings.Contains(result.Error.Error(), "binding") || client.calls != 1 {
					t.Fatalf("worker silently changed drivers: result=%+v calls=%d", result, client.calls)
				}
			} else if result.Status != subagent.StatusCompleted || client.calls != 2 {
				t.Fatalf("worker did not resume: result=%+v calls=%d", result, client.calls)
			}
			committed, err := subagent.LoadPersistedRun(path)
			if err != nil || committed.Recovery != nil {
				t.Fatalf("settled worker resurrected a stale checkpoint: %+v, %v", committed, err)
			}
		})
	}
}

func TestWorkerDriverRetainsGenerationUntilCleanup(t *testing.T) {
	s, generation := workerCompositionSession(t, false)
	client := &blockingWorkerBindingModel{started: make(chan struct{}), release: make(chan struct{})}
	manager := subagent.NewManagerWithOptions(client, "controlled", subagent.ManagerOptions{AcquireLoopDriver: s.driverResolver(nil)})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	worker, err := manager.Spawn(ctx, subagent.SpawnOptions{ID: "worker", Prompt: "Say hello", Toolkit: workerBindingTools{}, HistoryPath: filepath.Join(t.TempDir(), "worker.json")})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-ctx.Done():
		t.Fatal("worker never reached its selected model service")
	}
	_, replacement := workerCompositionSession(t, true)
	// The replacement session owns one reference; this session owns another.
	replacement.retain()
	s.pluginGenerationMu.Lock()
	s.pluginGeneration = replacement
	s.pluginGenerationMu.Unlock()
	s.releasePluginGeneration(generation)
	for _, status := range generation.host.Statuses() {
		if status.ID == loopdriver.DefaultDriverID && status.State != pluginhost.StateActive {
			t.Fatalf("provider retired before its worker: %+v", status)
		}
	}
	close(client.release)
	result, err := manager.Wait(ctx, worker.ID)
	if err != nil || result.Status != subagent.StatusCompleted {
		t.Fatalf("retained worker = %+v, %v", result, err)
	}
	for _, status := range generation.host.Statuses() {
		if status.ID == loopdriver.DefaultDriverID && status.State != pluginhost.StateStopped {
			t.Fatalf("provider leaked after worker cleanup: %+v", status)
		}
	}
	if _, err := manager.Followup(ctx, worker.ID, "Try another turn"); err != nil {
		t.Fatal(err)
	}
	result, err = manager.Wait(ctx, worker.ID)
	if err != nil || result.Status != subagent.StatusFailed || client.model.calls != 1 {
		t.Fatalf("removed provider accepted new work: result=%+v err=%v calls=%d", result, err, client.model.calls)
	}
}

func TestWorkerDriverRecoversBeforeFirstTerminalSnapshot(t *testing.T) {
	s, _ := workerCompositionSession(t, false)
	client := &blockingWorkerBindingModel{started: make(chan struct{}), release: make(chan struct{})}
	manager := subagent.NewManagerWithOptions(client, "controlled", subagent.ManagerOptions{AcquireLoopDriver: s.driverResolver(nil)})
	ctx, cancel := context.WithTimeout(context.Background(), 10*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "parent", ExecutionID: "parent-execution"})
	path := filepath.Join(t.TempDir(), "worker.json")
	worker, err := manager.Spawn(ctx, subagent.SpawnOptions{ID: "worker", Prompt: "Say hello", Toolkit: workerBindingTools{}, HistoryPath: path})
	if err != nil {
		t.Fatal(err)
	}
	select {
	case <-client.started:
	case <-ctx.Done():
		t.Fatal("worker never reached its selected model service")
	}
	interrupted, err := subagent.LoadPersistedRun(path)
	if err != nil || interrupted.Status != subagent.StatusInterrupted || interrupted.DriverBinding == nil || interrupted.DriverCheckpoint == nil || interrupted.DriverExecutionID == "parent-execution" {
		t.Fatalf("missing first execution recovery: %+v, %v", interrupted, err)
	}
	if _, err := os.Stat(path); !errors.Is(err, os.ErrNotExist) {
		t.Fatalf("pending checkpoint published a terminal snapshot: %v", err)
	}
	manager.StopAll()
	if _, err := manager.Wait(context.Background(), worker.ID); err != nil {
		t.Fatal(err)
	}
	// Rebuild from the durable image captured while the writer was in flight.
	restartedClient := &defaultBindingModel{}
	restarted := subagent.NewManagerWithOptions(restartedClient, "controlled", subagent.ManagerOptions{AcquireLoopDriver: s.driverResolver(nil)})
	if _, err := restarted.Restore(subagent.RestoreOptions{Run: interrupted, Toolkit: workerBindingTools{}, HistoryPath: path}); err != nil {
		t.Fatal(err)
	}
	if _, err := restarted.Followup(context.Background(), worker.ID, "Continue"); err != nil {
		t.Fatal(err)
	}
	result, err := restarted.Wait(context.Background(), worker.ID)
	if err != nil || result.Status != subagent.StatusCompleted || result.Result != "Hello" || restartedClient.calls != 1 {
		t.Fatalf("first execution did not recover: %+v, %v", result, err)
	}
}

func workerCompositionSession(t *testing.T, removed bool, factories ...NativeProviderFactory) (*Session, *PluginGeneration) {
	t.Helper()
	var selection *composition.Config
	if len(factories) > 0 {
		selection = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{nativeModelModule: false, factories[0].ID: true}}}, Services: []composition.Binding{{Service: selectedModelService, Major: 1, Provider: factories[0].ID}}}
	}
	if removed {
		modules := append(testStateModules(), kernelHostModule)
		selection = &composition.Config{Version: composition.Version, Modules: &modules}
	}
	root := t.TempDir()
	factories = withCapacityProviders(factories, NewCapacityResources(0))
	home, state := testWorkspaceState(t, root)
	host, kernel, err := buildPluginHost(nil, root, "workspace", home, state, nil, startPluginClient, nil, nil, selection, "", factories...)
	if err != nil {
		t.Fatal(err)
	}
	if err := host.Activate(context.Background()); err != nil {
		t.Fatal(err)
	}
	generation := &PluginGeneration{host: host, driverGateways: kernel.driverGateways, state: kernel.state}
	generation.retain()
	s := &Session{pluginGeneration: generation, Documents: kernel.state.documents, Sessions: kernel.state.sessions, InferenceJournalRuntime: kernel.state.journal}
	t.Cleanup(func() { s.releasePluginGeneration(s.pluginGeneration) })
	return s, generation
}

type workerBindingTools struct{}

func (workerBindingTools) Definitions() []providers.ToolDefinition { return nil }
func (workerBindingTools) Execute(context.Context, providers.ToolCall) (string, error) {
	return "", errors.New("unexpected tool call")
}

type blockingWorkerBindingModel struct {
	model   defaultBindingModel
	started chan struct{}
	release chan struct{}
}

func (m *blockingWorkerBindingModel) Chat(context.Context, providers.ChatRequest) (providers.ChatResponse, error) {
	return providers.ChatResponse{}, errors.New("unexpected non-streaming call")
}
func (m *blockingWorkerBindingModel) StreamChat(ctx context.Context, request providers.ChatRequest) (<-chan providers.StreamEvent, error) {
	close(m.started)
	select {
	case <-m.release:
	case <-ctx.Done():
		return nil, ctx.Err()
	}
	return m.model.StreamChat(ctx, request)
}
