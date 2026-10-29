package runtime

import (
	"context"
	"encoding/json"
	"errors"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/credentialstore"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/stretchr/testify/require"
)

// Runs an independently installed SDK plugin through the selected prompt graph.
func TestPromptSectionDecoratorProcess(t *testing.T) {
	fixture := os.Getenv("WUU_PROMPT_SECTION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/prompt-section-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	home, state := testWorkspaceState(t, root)
	plugin := testRuntimePlugin("independent.prompt-section")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"prompt-section-decorator.js"}
	selection := &composition.Config{Version: composition.Version, Services: []composition.Binding{
		{Service: promptservice.ServiceName, Major: 1, Provider: plugin.ID},
		{Consumer: plugin.ID, Service: promptservice.ServiceName, Major: 1, Provider: promptservice.NativeModule},
	}}
	ctx := context.Background()
	host, _, err := buildPluginHost([]pluginpkg.Plugin{plugin}, root, "prompt-decorator", home, state, nil, startPluginClient, nil, nil, selection, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	require.NoError(t, host.Activate(ctx))
	for _, role := range []string{"main", "worker"} {
		t.Run(role, func(t *testing.T) {
			request := promptservice.Request{Role: role, CWD: "/项目", Date: "2026-10-29"}
			if role == "worker" {
				request.Worker = &promptservice.WorkerContext{Prompt: "worker task", Isolation: "inplace"}
			}
			original, err := promptservice.Assemble(request)
			require.NoError(t, err)
			result, err := assembleSelectedPrompt(ctx, host, request, nil)
			require.NoError(t, err)
			require.Contains(t, result.Content, "# Environment\n- Current working directory: /项目\n- Current date: 2026-10-29")
			require.NotEqual(t, original.Content, result.Content)
			require.Equal(t, len(original.Sections), len(result.Sections))
			for i, section := range result.Sections {
				require.Equal(t, original.Sections[i].Key, section.Key)
				if section.Key != "environment" {
					require.Equal(t, original.Sections[i].Hash, section.Hash)
				}
			}
			if role == "worker" {
				require.Contains(t, result.Content, "worker task")
			}
		})
	}
	// Contributions with the same key make the target ambiguous, so no native
	// prompt may silently pass through after the adapter rejects the input.
	_, err = assembleSelectedPrompt(ctx, host, promptservice.Request{Role: "main", Contributions: []promptservice.Contribution{{Key: "environment", Content: "conflict", Static: true}}}, nil)
	require.Error(t, err)
	retired, found := host.RetirePlugin(ctx, promptservice.NativeModule, errors.New("removed"))
	require.True(t, found)
	require.NoError(t, retired.Err)
	_, err = assembleSelectedPrompt(ctx, host, promptservice.Request{Role: "main"}, nil)
	require.Error(t, err)
	require.False(t, strings.TrimSpace(err.Error()) == "")
}

// The selected decorator is exercised through main and worker model requests.
func TestPromptSectionDecoratorModelInputs(t *testing.T) {
	fixture := os.Getenv("WUU_PROMPT_SECTION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/prompt-section-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	requests := make(chan []providers.ChatMessage, 4)
	endpoint := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
			http.Error(w, err.Error(), 400)
			return
		}
		requests <- request.Messages
		w.Header().Set("Content-Type", "text/event-stream")
		fmt.Fprint(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"content\":\"done\"},\"finish_reason\":\"stop\"}]}\n\ndata: [DONE]\n\n")
	}))
	defer endpoint.Close()
	plugin := testRuntimePlugin("independent.prompt-section")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"prompt-section-decorator.js"}
	catalog := []pluginpkg.Plugin{plugin}
	cfg := config.Default()
	disabledSkills := false
	cfg.Skills.Enabled = &disabledSkills
	cfg.DefaultProvider = "fixture"
	cfg.Providers = map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", WireAPI: "chat", BaseURL: endpoint.URL, APIKey: "synthetic", Model: "gpt-4o"}}
	cfg.Composition = &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true, credentialstore.OSModule: false, credentialstore.FileModule: true}, Services: []composition.Binding{{Service: promptservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: promptservice.ServiceName, Major: 1, Provider: promptservice.NativeModule}}}}}
	root, err := filepath.EvalSymlinks(t.TempDir())
	require.NoError(t, err)
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	_, err = owner.Sessions.Create("prompt-model-input")
	require.NoError(t, err)
	thread, err := owner.NewThreadRuntime("prompt-model-input")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "system", Content: thread.StreamRunner.SystemPrompt}, {Role: "user", Content: "Answer briefly."}}, nil)
	require.NoError(t, err)
	check := func(role string) {
		t.Helper()
		select {
		case messages := <-requests:
			var content string
			for _, message := range messages {
				if message.Role == "system" {
					content += message.Content
				}
			}
			require.Contains(t, content, "# Environment\n- Current working directory: "+root+"\n- Current date:", role)
			require.NotContains(t, content, "# Environment\n\n- Current working directory:", role)
		case <-ctx.Done():
			t.Fatalf("%s model request not received: %v", role, ctx.Err())
		}
	}
	check("main")
	_, err = thread.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "check", Prompt: "Answer briefly.", Synchronous: true})
	require.NoError(t, err)
	check("worker")
}
