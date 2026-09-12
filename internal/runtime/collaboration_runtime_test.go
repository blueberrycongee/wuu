package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"net/http"
	"net/http/httptest"
	"os"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/mcp"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/statepath"
	"github.com/blueberrycongee/wuu/internal/subagent"
	"github.com/blueberrycongee/wuu/internal/tools"
)

func collaborationTestSession(t *testing.T) (*Session, *sessionRecordingClient) {
	t.Helper()
	home := t.TempDir()
	t.Setenv("WUU_HOME", home)
	root := t.TempDir()
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	provider := config.ProviderConfig{Type: "openai-compatible", Model: "gpt-test", BaseURL: "https://example.invalid/v1"}
	roles, err := modelroles.Resolve(config.Config{}, modelroles.ResolveOptions{ProviderName: "primary", ProviderConfig: provider, Model: provider.Model})
	if err != nil {
		t.Fatal(err)
	}
	client := &sessionRecordingClient{}
	return &Session{
		ProviderName: "primary", Model: provider.Model, RootDir: root,
		WuuHome: home, SessionDir: statepath.SessionsDir(home), SessionDate: "2026-09-12",
		ModelRoles: roles, Toolkit: kit, Permissions: config.ResolvedPermissions{Mode: config.PermissionModeUnconfined},
		StreamRunner: &agent.StreamRunner{Client: client, ProviderName: "primary", Model: provider.Model, APIModel: provider.Model},
	}, client
}

func collaborationTestThread(t *testing.T, s *Session, id string, selected ThreadModelSelection) *ThreadRuntime {
	t.Helper()
	root := t.TempDir()
	thread, err := s.NewNamedAgentThreadRuntime(id, root, filepath.Join(root, "memory"), "Identity orientation for this worker.", selected)
	if err != nil {
		t.Fatal(err)
	}
	t.Cleanup(func() {
		if thread.AgentControl != nil {
			thread.AgentControl.StopAll()
			thread.AgentControl.Close()
		}
	})
	return thread
}

func TestCollaborationExecutionDoesNotInheritInteractiveExtensions(t *testing.T) {
	s, client := collaborationTestSession(t)
	var callbacks atomic.Int32
	s.Toolkit.SetOnFileChanged(func(string) { callbacks.Add(1) })
	s.Toolkit.SetPermissionRequestHook(func(context.Context, *tools.Toolkit, tools.ToolInfo, providers.ToolCall) error {
		callbacks.Add(1)
		return errors.New("interactive permission hook")
	})
	s.Toolkit.SetCodeModeService(&codemode.Service{})
	s.Toolkit.SetCodeModeOnly(true)
	s.Toolkit.SetMCPManager(mcp.NewManager())
	s.Skills = []skills.Skill{{Name: "interactive-skill", Source: "plugin:interactive"}}
	s.Toolkit.SetSkills(s.Skills)
	s.StreamRunner.SystemPrompt = "interactive injected prompt"
	s.StreamRunner.LoopDriver = loopdriver.FailClosedDriver{Profile: "interactive-driver"}
	s.StreamRunner.CompactionRegistry = agent.NewCompactionRegistry()
	s.StreamRunner.CompactionRegistry.Register(&generationCompactionProvider{key: "interactive"})
	s.StreamRunner.DisableAutoCompact = true
	s.StreamRunner.BeforeModelStep = func(context.Context, int, []providers.ChatMessage) ([]providers.ChatMessage, error) {
		return nil, errors.New("interactive model hook")
	}
	s.StreamRunner.BeforeRequest = func(context.Context, *providers.ChatRequest) error { return errors.New("interactive request hook") }
	s.StreamRunner.BeforeCompact = func(context.Context, agent.CompactReason) error { return errors.New("interactive compaction hook") }
	s.StreamRunner.AfterCompact = func(context.Context, agent.CompactReason, error) error {
		return errors.New("interactive compaction hook")
	}
	s.StreamRunner.AfterTurn = func(context.Context, *agent.StreamRunner, []providers.ChatMessage, agent.LoopResult) {
		callbacks.Add(1)
	}
	s.HookDispatcher = hooks.NewDispatcher(hooks.NewRegistry(map[hooks.Event][]hooks.HookConfig{
		hooks.SubagentStart: {{Command: "exit 12"}},
		hooks.SubagentStop:  {{Command: "exit 13"}},
	}))

	thread := collaborationTestThread(t, s, "isolated", ThreadModelSelection{})
	runner := thread.StreamRunner
	if _, err := runner.RunWithCallback(context.Background(), []providers.ChatMessage{{Role: "user", Content: "Run independently."}}, nil); err != nil {
		t.Fatalf("collaboration execution inherited interactive policy: %v", err)
	}
	for _, message := range client.LastRequest().Messages {
		if strings.Contains(message.Content, "interactive injected prompt") || strings.Contains(message.Content, "interactive-skill") {
			t.Fatalf("interactive instructions reached collaboration model: %s", message.Content)
		}
	}
	if runner.LoopDriver != nil || runner.CompactionRegistry != nil || runner.BeforeCompact != nil || runner.AfterCompact != nil || runner.DisableAutoCompact {
		t.Fatal("collaboration inherited interactive execution or compaction policy")
	}
	if thread.Toolkit.CodeModeService() != nil || thread.Toolkit.CodeModeOnly() || thread.Toolkit.MCPManager() != nil || len(thread.Toolkit.Skills()) != 0 {
		t.Fatal("collaboration inherited interactive tool dependencies")
	}
	if _, err := runner.Tools.Execute(context.Background(), providers.ToolCall{ID: "write-root", Name: "apply_patch", Arguments: `{"patchText":"*** Begin Patch\n*** Add File: root.txt\n+independent\n*** End Patch\n"}`}); err != nil {
		t.Fatalf("collaboration write failed: %v", err)
	}
	if contents, err := os.ReadFile(filepath.Join(thread.Toolkit.RootDir(), "root.txt")); err != nil || strings.TrimSpace(string(contents)) != "independent" {
		t.Fatalf("collaboration did not write its own workspace: %q, %v", contents, err)
	}
	worker, err := thread.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type: agentcontrol.DefaultSubagentType, TaskName: "independent_worker", Prompt: "Complete your task.",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished, err := thread.AgentControl.Wait(ctx, worker.AgentID)
	if err != nil || finished.Status != subagent.StatusCompleted {
		t.Fatalf("child inherited interactive lifecycle hook: %+v, %v", finished, err)
	}
	if callbacks.Load() != 0 {
		t.Fatalf("interactive callbacks executed %d times", callbacks.Load())
	}
	request := client.LastRequest()
	for _, message := range request.Messages {
		if strings.Contains(message.Content, "interactive-skill") || strings.Contains(message.Content, "interactive injected prompt") {
			t.Fatalf("interactive instructions reached child: %s", message.Content)
		}
	}
	for _, definition := range request.Tools {
		switch definition.Name {
		case "exec", "wait", "spawn_agent", "followup_task", "send_message_to_agent", "wait_agent", "list_agents", "close_agent", "interrupt_agent":
			t.Fatalf("interactive execution tool %q reached child", definition.Name)
		}
	}
	for _, name := range []string{"exec", "wait", "spawn_agent", "followup_task", "send_message_to_agent", "wait_agent", "list_agents", "close_agent", "interrupt_agent"} {
		if thread.Toolkit.SupportsTool(name) {
			t.Fatalf("interactive execution tool %q remained callable in collaboration", name)
		}
	}
}

func TestCollaborationWorkerUsesSessionModelAfterInteractiveDefaultsChange(t *testing.T) {
	s, client := collaborationTestSession(t)
	other := &sessionRecordingClient{}
	s.WorkerClient = other
	s.ModelRoles.Worker = modelroles.Selection{Provider: "ordinary-worker", Model: "ordinary-model", APIModel: "ordinary-model"}
	thread := collaborationTestThread(t, s, "pinned-worker", ThreadModelSelection{})
	s.StreamRunner.Client = other
	s.StreamRunner.Model = "changed-interactive-model"
	s.Model = "changed-interactive-model"
	s.ModelRoles.Worker.Model = "changed-worker-model"
	worker, err := thread.AgentControl.Spawn(context.Background(), agentcontrol.SpawnRequest{
		Type: agentcontrol.DefaultSubagentType, TaskName: "pinned_child", Prompt: "Complete your task.",
	})
	if err != nil {
		t.Fatal(err)
	}
	ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
	defer cancel()
	finished, err := thread.AgentControl.Wait(ctx, worker.AgentID)
	if err != nil || finished.Status != subagent.StatusCompleted {
		t.Fatalf("worker: %+v, %v", finished, err)
	}
	if request := client.LastRequest(); request.Model != "gpt-test" {
		t.Fatalf("child model = %q, want the collaboration session's model", request.Model)
	}
	if request := other.LastRequest(); request.Model != "" {
		t.Fatalf("interactive worker transport was used: %q", request.Model)
	}
}

func TestCollaborationRecoveryUsesPinnedProviderAndRejectsMissingProvider(t *testing.T) {
	s, _ := collaborationTestSession(t)
	requests := make(chan string, 4)
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Model string `json:"model"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			t.Error(err)
		}
		requests <- request.Model
		w.Header().Set("Content-Type", "text/event-stream")
		_, _ = w.Write([]byte("data: {\"choices\":[{\"delta\":{\"content\":\"done\"},\"finish_reason\":null}]}\n\ndata: {\"choices\":[{\"delta\":{},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n"))
	}))
	defer server.Close()
	t.Setenv("COLLABORATION_TEST_KEY", "test")
	cfg := config.Config{DefaultProvider: "other", Providers: map[string]config.ProviderConfig{
		"pinned": {Type: "openai-compatible", BaseURL: server.URL, APIKeyEnv: "COLLABORATION_TEST_KEY", Model: "provider-default"},
		"other":  {Type: "openai-compatible", BaseURL: server.URL, APIKeyEnv: "COLLABORATION_TEST_KEY", Model: "other-default"},
	}}
	s.ConfigLoadMode = ConfigLoadFile
	s.ConfigPath = filepath.Join(t.TempDir(), "config.json")
	writeConfig := func() {
		t.Helper()
		data, err := json.Marshal(cfg)
		if err != nil {
			t.Fatal(err)
		}
		if err := os.WriteFile(s.ConfigPath, data, 0o600); err != nil {
			t.Fatal(err)
		}
	}
	writeConfig()
	selected := ThreadModelSelection{Provider: "pinned", Model: "selected-model"}
	first := collaborationTestThread(t, s, "first-model", selected)
	provider := cfg.Providers["pinned"]
	provider.Model = "changed-default"
	cfg.Providers["pinned"] = provider
	writeConfig()
	recovered := collaborationTestThread(t, s, "recovered-model", first.Selection)
	for _, thread := range []*ThreadRuntime{first, recovered} {
		ctx, cancel := context.WithTimeout(context.Background(), 5*time.Second)
		_, err := thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Use the selected model."}}, nil)
		cancel()
		if err != nil {
			t.Fatal(err)
		}
		if model := <-requests; model != selected.Model {
			t.Fatalf("wire model = %q, want %q", model, selected.Model)
		}
		if thread.Selection.Provider != selected.Provider || thread.ExecutionProfile != CollaborationRuntimeVersion {
			t.Fatalf("lost execution identity: %+v", thread.Selection)
		}
	}
	delete(cfg.Providers, "pinned")
	writeConfig()
	root := t.TempDir()
	if _, err := s.NewNamedAgentThreadRuntime("missing-model", root, filepath.Join(root, "memory"), "", first.Selection); !errors.Is(err, ErrThreadProviderUnavailable) {
		t.Fatalf("missing pinned provider error = %v, want no default fallback", err)
	}
}
