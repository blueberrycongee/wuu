package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

// A public configuration and independent process replace one built-in on a
// product thread; the fixture performs no file access of its own.
func TestToolReplacementProcess(t *testing.T) {
	t.Setenv("WUU_DERIVED_CONTEXT_LEDGERS", "on")
	fixture := os.Getenv("WUU_TOOL_REPLACEMENT_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/tool-replacement-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, ptc := range []bool{false, true} {
		t.Run(fmt.Sprintf("ptc=%t", ptc), func(t *testing.T) {
			root, home := t.TempDir(), t.TempDir()
			require.NoError(t, os.WriteFile(filepath.Join(root, "fixture.txt"), []byte("native-content"), 0600))
			baseKit, err := tools.New(root)
			require.NoError(t, err)
			schema, err := json.Marshal(baseKit.LookupTool("web_search").Definition().InputSchema)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
			plugin := testRuntimePlugin("fixture-reader")
			plugin.Root = fixture
			plugin.Runtime.Command = node
			plugin.Runtime.Args = []string{"tool.js"}
			plugins := []pluginpkg.Plugin{plugin}
			requests := make(chan []providers.ChatMessage, 4)
			count := 0
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var request struct {
					Messages []providers.ChatMessage `json:"messages"`
				}
				if err := json.NewDecoder(r.Body).Decode(&request); err != nil {
					http.Error(w, "bad request", 400)
					return
				}
				requests <- request.Messages
				count++
				w.Header().Set("Content-Type", "text/event-stream")
				if count == 1 {
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"main-search","type":"function","function":{"name":"web_search","arguments":"{\"query\":\"fixture\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
				} else {
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
				}
			}))
			defer model.Close()
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: ptc}}
			// Decode the public JSON so this test exercises the serialized configuration.
			require.NoError(t, json.Unmarshal([]byte(`{"tool_replacements":{"web_search":{"plugin_id":"fixture-reader","tool_id":"search"}}}`), &cfg.Agent))
			owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			t.Cleanup(func() { _, _ = owner.Cleanup() })
			_, err = owner.Sessions.CreateWithMetadata("tool-replacement", root)
			require.NoError(t, err)
			workerClient := &sessionRecordingClient{}
			owner.WorkerClient = workerClient
			thread, err := owner.NewThreadRuntime("tool-replacement")
			require.NoError(t, err)
			thread.Toolkit.SetBoundary(tools.UnconfinedBoundary())
			executor := thread.StreamRunner.Tools
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "tool-replacement", ExecutionID: "turn"})
			if !ptc {
				count := 0
				for _, definition := range executor.Definitions() {
					if definition.Name == "web_search" {
						count++
						require.Contains(t, definition.Description, "synthetic")
					}
				}
				require.Equal(t, 1, count)
				text, err := executor.Execute(ctx, providers.ToolCall{ID: "read", Name: "web_search", Arguments: `{"query":"fixture"}`})
				require.NoError(t, err)
				require.Contains(t, text, "fixture:result")
				require.NotContains(t, text, "native-content")
				require.Contains(t, text, `"status_code":"200"`, "provider fields must remain in the returned result")
				block, ok := thread.Toolkit.WebEvidenceContextBlock()
				require.True(t, ok)
				require.Contains(t, block.Content, "fixture-evidence")
				require.Contains(t, block.Content, "tool=web_search")
			} else {
				nested, err := thread.Toolkit.CodeModeNestedSurface()
				require.NoError(t, err)
				count := 0
				for _, definition := range nested {
					if definition.Name == "web_search" {
						count++
						require.Contains(t, definition.Description, "synthetic")
					}
				}
				require.Equal(t, 1, count)
				runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: executor, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
				defer runtime.Cancel()
				arguments, _ := json.Marshal(map[string]any{"code": `return await tools.web_search({query:"fixture"})`, "description": "read fixture"})
				messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "ptc-read", Name: "run_code", Arguments: string(arguments)}}, nil)
				require.NoError(t, err)
				require.Len(t, messages, 1)
				require.Contains(t, messages[0].Content, "fixture:result")
				require.NotContains(t, messages[0].Content, "native-content")
			}
			if !ptc {
				_, runErr := thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Search fixture"}}, nil)
				require.NoError(t, runErr)
				<-requests
				second := <-requests
				var visible string
				for _, message := range second {
					visible += message.Content
				}
				require.Contains(t, visible, "fixture:result")
				require.Contains(t, visible, "fixture-evidence")
			}
			if !ptc {
				_, err := thread.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "replacement_scope", Prompt: "Complete without tools", Synchronous: true})
				require.NoError(t, err)
				for _, definition := range workerClient.LastRequest().Tools {
					require.NotEqual(t, "web_search", definition.Name, "root-only tool must not appear in a real worker model request")
				}
			}
			if !ptc {
				side, err := owner.NewSideThreadRunner("side-replacement", root, ThreadModelSelection{}, "test side")
				require.NoError(t, err)
				sideText, err := side.Tools.Execute(ctx, providers.ToolCall{ID: "side-read", Name: "web_search", Arguments: `{"query":"fixture"}`})
				require.NoError(t, err)
				require.Contains(t, sideText, "fixture:result")
				require.Contains(t, sideText, "side-replacement")
			}
			callCtx := ctx
			if ptc {
				callCtx = toolctx.WithNestedCall(ctx)
			}
			listing, err := executor.Execute(callCtx, providers.ToolCall{ID: "list", Name: "list_files", Arguments: `{}`})
			require.NoError(t, err)
			require.Contains(t, listing, "fixture.txt")
			call := providers.ToolCall{ID: "denied", Name: "web_search", Arguments: `{"query":"fixture"}`}
			thread.Toolkit.SetAgentIdentity("child", "/root/child")
			for _, definition := range executor.Definitions() {
				require.NotEqual(t, "web_search", definition.Name, "root-only replacement must not be advertised to child")
			}
			if ptc {
				catalog, catalogErr := thread.Toolkit.CodeModeNestedSurface()
				if catalogErr == nil {
					for _, definition := range catalog {
						require.NotEqual(t, "web_search", definition.Name, "root-only replacement must not be discoverable by child")
					}
				}
			}
			_, err = executor.Execute(callCtx, call)
			require.Error(t, err)
			thread.Toolkit.SetAgentIdentity("tool-replacement", "/root")
			denier := &denyPluginAuthorizer{}
			thread.Toolkit.SetAuthorizer(denier)
			_, err = executor.Execute(callCtx, call)
			require.Error(t, err)
			require.Positive(t, denier.calls)
			thread.Toolkit.SetAuthorizer(nil)
			thread.Toolkit.DisableTools("web_search")
			_, err = executor.Execute(callCtx, call)
			require.Error(t, err)
			thread.Toolkit.EnableTools("web_search")
			var target string
			for _, def := range thread.PluginHost.ToolDefinitions() {
				registered, ok := thread.PluginHost.Tool(def.Name)
				if ok && registered.PluginID == plugin.ID && registered.Registration.ID == "search" {
					target = def.Name
				}
			}
			require.NotEmpty(t, target)
			thread.Toolkit.DisableTools(target)
			_, err = executor.Execute(callCtx, call)
			require.Error(t, err)
			thread.Toolkit.EnableTools(target)
			freezer, ok := executor.(agent.ToolSurfaceFreezer)
			require.True(t, ok)
			freezer.FreezeToolSurface()
			_, found := thread.PluginHost.RetirePlugin(ctx, plugin.ID, fmt.Errorf("removed"))
			require.True(t, found)
			_, err = executor.Execute(callCtx, call)
			require.Error(t, err, "retirement must revoke execution even during a frozen turn")
			freezer.UnfreezeToolSurface()
			for _, definition := range executor.Definitions() {
				require.NotEqual(t, "web_search", definition.Name, "retired replacement must not advertise stale or native schema")
			}
			if ptc {
				catalog, catalogErr := thread.Toolkit.CodeModeNestedSurface()
				if catalogErr == nil {
					for _, definition := range catalog {
						require.NotEqual(t, "web_search", definition.Name, "retired replacement must not be discoverable")
					}
				}
			}
			_, err = executor.Execute(callCtx, call)
			require.Error(t, err)
			require.False(t, strings.TrimSpace(err.Error()) == "")
		})
	}
}

// A child-enabled replacement must receive a real worker invocation, not only
// appear in the parent's tool surface.
func TestToolReplacementWorkerProcess(t *testing.T) {
	fixture := os.Getenv("WUU_TOOL_REPLACEMENT_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/tool-replacement-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	skillDir := filepath.Join(root, ".agents", "skills", "replacement-research")
	require.NoError(t, os.MkdirAll(skillDir, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(skillDir, "SKILL.md"), []byte("---\nname: replacement-research\ndescription: Research with the selected search provider.\nallowed-tools: [web_search]\n---\nUse web_search to gather evidence.\n"), 0600))
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("web_search").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture-reader")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"tool.js", "all"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	require.NoError(t, json.Unmarshal([]byte(`{"tool_replacements":{"web_search":{"plugin_id":"fixture-reader","tool_id":"search"}}}`), &cfg.Agent))
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	call := providers.ToolCall{ID: "worker-read", Name: "web_search", Arguments: `{"query":"fixture"}`}
	client := &sessionRecordingClient{streamBatches: [][]providers.StreamEvent{{{Type: providers.EventToolUseStart, ToolCall: &call}, {Type: providers.EventToolUseDelta, Content: call.Arguments}, {Type: providers.EventToolUseEnd, ToolCall: &call}, {Type: providers.EventDone}}}}
	owner.WorkerClient = client
	thread, err := owner.NewThreadRuntime("worker-parent")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	spawned, err := thread.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "worker_read", Prompt: "Search fixture", Synchronous: true})
	require.NoError(t, err)
	var result string
	for _, message := range client.LastRequest().Messages {
		if message.Role == "tool" {
			result += message.Content
		}
	}
	require.Contains(t, result, "fixture:result")
	require.Contains(t, result, "worker-parent")
	require.NotEmpty(t, spawned.AgentID)
	client.mu.Lock()
	first := client.first
	client.mu.Unlock()
	require.NotNil(t, first)
	var initialPrompt string
	for _, message := range first.Messages {
		if message.Role == "system" {
			initialPrompt += message.Content
		}
		require.NotEqual(t, "skill_catalog", message.Cause, "the first skill catalog must already match the executable surface")
	}
	require.Contains(t, initialPrompt, "replacement-research", "search-dependent skills must be in the worker's initial prompt")
	var searchAvailable bool
	for _, definition := range first.Tools {
		searchAvailable = searchAvailable || definition.Name == "web_search"
	}
	require.True(t, searchAvailable, "initial prompt and tool definitions must agree")
}

func TestToolReplacementGenerationProcess(t *testing.T) {
	fixture := os.Getenv("WUU_TOOL_REPLACEMENT_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/tool-replacement-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("web_search").Definition().InputSchema)
	require.NoError(t, err)
	schemaPath := filepath.Join(fixture, "schema.json")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0600))
	plugin := testRuntimePlugin("fixture-reader")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"tool.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	require.NoError(t, json.Unmarshal([]byte(`{"tool_replacements":{"web_search":{"plugin_id":"fixture-reader","tool_id":"search"}}}`), &cfg.Agent))
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	old, err := owner.NewThreadRuntime("old-generation")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "old-generation", ExecutionID: "turn"})
	call := providers.ToolCall{ID: "read-old", Name: "web_search", Arguments: `{"query":"fixture"}`}
	result, err := old.StreamRunner.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, result, "fixture:result")
	missing := cfg
	missing.Agent.ToolReplacements = map[string]config.ToolReplacement{"web_search": {PluginID: "missing-reader", ToolID: "search"}}
	badCandidate, missingErr := owner.PreflightDiscoveredExtensions(missing, plugins)
	if badCandidate != nil {
		defer badCandidate.close()
	}
	require.Error(t, missingErr)
	result, err = old.StreamRunner.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, result, "fixture:result")
	require.NoError(t, os.WriteFile(schemaPath, []byte(`{"type":"object"}`), 0600))
	candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins, plugin.ID)
	if candidate != nil {
		defer candidate.close()
	}
	require.Error(t, err)
	result, err = old.StreamRunner.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, result, "fixture:result")
	require.NoError(t, os.WriteFile(schemaPath, schema, 0600))
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(server.URL), 0600))
	type outcome struct {
		value string
		err   error
	}
	pending := make(chan outcome, 1)
	go func() {
		value, callErr := old.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "in-flight", Name: "web_search", Arguments: `{"query":"fixture"}`})
		pending <- outcome{value, callErr}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("old call did not reach barrier")
	}
	sourcePath := filepath.Join(fixture, "tool.js")
	source, err := os.ReadFile(sourcePath)
	require.NoError(t, err)
	updated := strings.Replace(string(source), "fixture:", "next:", 1)
	require.NotEqual(t, string(source), updated)
	require.NoError(t, os.WriteFile(sourcePath, []byte(updated), 0600))
	defer os.WriteFile(sourcePath, source, 0600)
	candidate, err = owner.PreflightDiscoveredExtensions(cfg, plugins, plugin.ID)
	require.NoError(t, err)
	require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
	close(release)
	select {
	case result := <-pending:
		require.NoError(t, result.err)
		require.Contains(t, result.value, "fixture:result")
	case <-ctx.Done():
		t.Fatal("old call did not complete")
	}
	next, err := owner.NewThreadRuntime("new-generation")
	require.NoError(t, err)
	newCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "new-generation", ExecutionID: "turn-new"})
	result, err = next.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "read-new", Name: "web_search", Arguments: call.Arguments})
	require.NoError(t, err)
	require.Contains(t, result, "next:result")
	result, err = old.StreamRunner.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, result, "fixture:result")
	removed := cfg
	removed.Agent.ToolReplacements = nil
	candidate, err = owner.PreflightDiscoveredExtensions(removed, plugins, plugin.ID)
	require.NoError(t, err)
	require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
	without, err := owner.NewThreadRuntime("no-replacement")
	require.NoError(t, err)
	var description string
	for _, definition := range without.StreamRunner.Tools.Definitions() {
		if definition.Name == "web_search" {
			description = definition.Description
		}
	}
	require.Contains(t, description, "DuckDuckGo")
	result, err = old.StreamRunner.Tools.Execute(ctx, call)
	require.NoError(t, err)
	require.Contains(t, result, "fixture:result")
}

// The documented standalone example must work without the test fixture's schema file.
func TestToolReplacementExampleProcess(t *testing.T) {
	fixture := os.Getenv("WUU_TOOL_REPLACEMENT_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/tool-replacement-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	plugin := testRuntimePlugin("example-search")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"synthetic-web-search.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	require.NoError(t, json.Unmarshal([]byte(`{"tool_replacements":{"web_search":{"plugin_id":"example-search","tool_id":"search"}}}`), &cfg.Agent))
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("example")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "example", ExecutionID: "turn"})
	for _, input := range []struct {
		name, arguments, version string
	}{
		{"version hint", `{"query":"test","version_hint":" react@19 "}`, "react@19"},
		{"package fields", `{"query":"test","package_context":{"name":" react ","version":" 19 ","ecosystem":" npm "}}`, "npm react@19"},
		{"blank name", `{"query":"test","package_context":{"name":" ","version":" 19 ","ecosystem":" npm "}}`, "npm 19"},
		{"blank version", `{"query":"test","package_context":{"name":" react ","version":" ","ecosystem":" npm "}}`, "npm react"},
		{"unicode hint whitespace", `{"query":"test","version_hint":"\u0085react@19\u0085"}`, "react@19"},
		{"unicode package whitespace", `{"query":"test","package_context":{"name":"\u0085react\u0085","version":"\u008519\u0085","ecosystem":"\u0085npm\u0085"}}`, "npm react@19"},
		{"preserve non-whitespace", `{"query":"test","version_hint":"\ufeffreact@19\ufeff"}`, "\ufeffreact@19\ufeff"},
		{"non-whitespace query", `{"query":"\ufeff","version_hint":"react@19"}`, "react@19"},
	} {
		t.Run(input.name, func(t *testing.T) {
			result, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "example", Name: "web_search", Arguments: input.arguments})
			require.NoError(t, err)
			require.Contains(t, result, "Synthetic example")
			require.Contains(t, result, `"version_matched_to_repo":"`+input.version+`"`)
		})
	}
}
