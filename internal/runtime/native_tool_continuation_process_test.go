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
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

// The fake native search bounds this test to host routing and lifecycle; it
// never changes the production search transport or its network protections.
func TestNativeToolContinuationProcess(t *testing.T) {
	fixture := os.Getenv("WUU_NATIVE_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run the native continuation acceptance script")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, ptc := range []bool{false, true} {
		t.Run(fmt.Sprintf("ptc=%t", ptc), func(t *testing.T) {
			root := t.TempDir()
			kit, err := tools.New(root)
			require.NoError(t, err)
			schema, err := json.Marshal(kit.LookupTool("web_search").Definition().InputSchema)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
			plugin := testRuntimePlugin("fixture.native-continuation")
			plugin.Root = fixture
			plugin.Runtime.Command = node
			plugin.Runtime.Args = []string{"tool.js"}
			plugins := []pluginpkg.Plugin{plugin}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
			cfg.PTC.Enabled = ptc
			cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"web_search": {PluginID: plugin.ID, ToolID: "search"}}
			owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			thread, err := owner.NewThreadRuntime("native-continuation")
			require.NoError(t, err)
			defer owner.ReleasePluginGeneration(thread.PluginGeneration)
			defer thread.AgentControl.Close()
			replacement, ok := thread.Toolkit.LookupTool("web_search").(*replacementTool)
			require.True(t, ok)
			var calls atomic.Int32
			replacement.continueNative = func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
				calls.Add(1)
				var args struct {
					Query string `json:"query"`
				}
				require.NoError(t, json.Unmarshal([]byte(call.Arguments), &args))
				text := map[string]any{"action": "web_search", "query": args.Query, "evidence": map[string]any{"id": "native-evidence", "kind": "search", "source": "https://example.test", "source_tier": "search_index", "retrieved_at": "2026-10-29T00:00:00Z", "version_matched_to_repo": "unknown"}, "results": []any{}}
				data, err := json.Marshal(text)
				require.NoError(t, err)
				return toolresult.FromText(string(data)), nil
			}
			ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
			defer cancel()
			ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "native-continuation", ExecutionID: "turn"})
			runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
			defer runtime.Cancel()

			call := func(id, query string) ([]providers.ChatMessage, error) {
				tool := providers.ToolCall{ID: id, Name: "web_search", Arguments: fmt.Sprintf(`{"query":%q}`, query)}
				if ptc {
					args, _ := json.Marshal(map[string]any{"code": fmt.Sprintf(`return await tools.web_search({query:%q})`, query), "description": "search"})
					tool = providers.ToolCall{ID: id, Name: "run_code", Arguments: string(args)}
				}
				return runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{tool}, nil)
			}
			messages, err := call("call-success", "duplicate")
			require.NoError(t, err)
			require.Len(t, messages, 1)
			require.Contains(t, messages[0].Content, "final-evidence")
			require.Equal(t, int32(1), calls.Load())
			block, ok := thread.Toolkit.WebEvidenceContextBlock()
			require.True(t, ok)
			require.Contains(t, block.Content, "final-evidence")
			require.NotContains(t, block.Content, "native-evidence")
			messages, err = call("call-failure", "throw")
			require.NoError(t, err)
			require.Len(t, messages, 1)
			require.True(t, messages[0].ToolResult.IsError)
			require.Equal(t, int32(2), calls.Load())
			block, ok = thread.Toolkit.WebEvidenceContextBlock()
			require.True(t, ok)
			require.NotContains(t, block.Content, "native-evidence")
			messages, err = call("call-forgery", "forgery")
			require.NoError(t, err)
			require.Len(t, messages, 1)
			require.False(t, messages[0].ToolResult.IsError)
			require.Equal(t, int32(3), calls.Load())

			for _, query := range []string{"save", "late"} {
				messages, err = call("call-"+query, query)
				require.NoError(t, err)
				require.Len(t, messages, 1)
				require.False(t, messages[0].ToolResult.IsError)
			}
			require.Equal(t, int32(5), calls.Load())

			entered := make(chan struct{})
			replacement.continueNative = func(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
				close(entered)
				<-ctx.Done()
				return toolresult.Result{}, ctx.Err()
			}
			cancelCtx, cancelCall := context.WithCancel(ctx)
			done := make(chan error, 1)
			go func() {
				tool := providers.ToolCall{ID: "call-cancel", Name: "web_search", Arguments: `{"query":"cancel"}`}
				if ptc {
					args, _ := json.Marshal(map[string]any{"code": `return await tools.web_search({query:"cancel"})`, "description": "search"})
					tool = providers.ToolCall{ID: "call-cancel", Name: "run_code", Arguments: string(args)}
				}
				_, err := runtime.ExecuteFinalCalls(cancelCtx, []providers.ToolCall{tool}, nil)
				done <- err
			}()
			select {
			case <-entered:
			case <-ctx.Done():
				t.Fatal("native continuation did not start")
			}
			cancelCall()
			select {
			case <-done:
			case <-ctx.Done():
				t.Fatal("cancel did not stop native continuation")
			}
			block, ok = thread.Toolkit.WebEvidenceContextBlock()
			require.True(t, ok)
			require.NotContains(t, block.Content, "native-evidence")
		})
	}

}

func TestNativeToolContinuationGenerationProcess(t *testing.T) {
	fixture := os.Getenv("WUU_NATIVE_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run the native continuation acceptance script")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("web_search").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.native-continuation")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"tool.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"web_search": {PluginID: plugin.ID, ToolID: "search"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	old, err := owner.NewThreadRuntime("old-native")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(old.PluginGeneration)
	defer old.AgentControl.Close()
	oldReplacement, ok := old.Toolkit.LookupTool("web_search").(*replacementTool)
	require.True(t, ok)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	entered, release := make(chan struct{}), make(chan struct{})
	makeResult := func(source string) toolresult.Result {
		return toolresult.FromText(fmt.Sprintf(`{"action":"web_search","query":"generation","evidence":{"id":"native","kind":"search","source":%q,"source_tier":"search_index","retrieved_at":"2026-10-29T00:00:00Z","version_matched_to_repo":"unknown"},"results":[]}`, source))
	}
	oldReplacement.continueNative = func(c context.Context, _ providers.ToolCall) (toolresult.Result, error) {
		close(entered)
		select {
		case <-release:
			return makeResult("https://old.example"), nil
		case <-c.Done():
			return toolresult.Result{}, c.Err()
		}
	}
	type outcome struct {
		text string
		err  error
	}
	done := make(chan outcome, 1)
	oldCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "old-native", ExecutionID: "turn"})
	go func() {
		v, e := old.StreamRunner.Tools.Execute(oldCtx, providers.ToolCall{ID: "old", Name: "web_search", Arguments: `{"query":"generation"}`})
		done <- outcome{v, e}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("old continuation did not start")
	}
	candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins, plugin.ID)
	require.NoError(t, err)
	require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
	next, err := owner.NewThreadRuntime("new-native")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(next.PluginGeneration)
	defer next.AgentControl.Close()
	newReplacement, ok := next.Toolkit.LookupTool("web_search").(*replacementTool)
	require.True(t, ok)
	newReplacement.continueNative = func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		return makeResult("https://new.example"), nil
	}
	nextCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "new-native", ExecutionID: "turn"})
	value, err := next.StreamRunner.Tools.Execute(nextCtx, providers.ToolCall{ID: "new", Name: "web_search", Arguments: `{"query":"generation"}`})
	require.NoError(t, err)
	require.Contains(t, value, "https://new.example")
	close(release)
	select {
	case result := <-done:
		require.NoError(t, result.err)
		require.Contains(t, result.text, "https://old.example")
	case <-ctx.Done():
		t.Fatal("old continuation did not complete")
	}
}

func TestNativeToolContinuationRevokedAfterAdmission(t *testing.T) {
	fixture := os.Getenv("WUU_NATIVE_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run the native continuation acceptance script")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("web_search").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	entered, release := make(chan struct{}), make(chan struct{})
	server := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
		close(entered)
		<-release
		w.WriteHeader(http.StatusNoContent)
	}))
	defer server.Close()
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(server.URL), 0600))
	plugin := testRuntimePlugin("fixture.native-continuation")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"tool.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"web_search": {PluginID: plugin.ID, ToolID: "search"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("revoked-native")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	replacement, ok := thread.Toolkit.LookupTool("web_search").(*replacementTool)
	require.True(t, ok)
	var calls atomic.Int32
	replacement.continueNative = func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		calls.Add(1)
		return toolresult.Result{}, nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "revoked-native", ExecutionID: "turn"})
	done := make(chan error, 1)
	go func() {
		_, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "revoked", Name: "web_search", Arguments: `{"query":"revoke"}`})
		done <- err
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("plugin did not pause")
	}
	thread.Toolkit.SetAuthorizer(&denyPluginAuthorizer{})
	close(release)
	select {
	case err := <-done:
		require.Error(t, err)
	case <-ctx.Done():
		t.Fatal("revoked continuation did not return")
	}
	require.Zero(t, calls.Load())
	_, ok = thread.Toolkit.WebEvidenceContextBlock()
	require.False(t, ok)
}

func TestNativeToolContinuationRejectsOrdinaryPlugin(t *testing.T) {
	fixture := os.Getenv("WUU_NATIVE_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run the native continuation acceptance script")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("web_search").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.native-continuation")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"tool.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("ordinary-plugin")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	var name string
	for _, definition := range thread.PluginHost.ToolDefinitions() {
		tool, ok := thread.PluginHost.Tool(definition.Name)
		if ok && tool.PluginID == plugin.ID {
			name = definition.Name
		}
	}
	require.NotEmpty(t, name)
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "ordinary-plugin", ExecutionID: "turn"})
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "ordinary", Name: name, Arguments: `{"query":"ordinary"}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.True(t, messages[0].ToolResult.IsError)
	require.Contains(t, messages[0].Content, "native continuation owner mismatch")
}

func TestNativeToolContinuationExampleProcess(t *testing.T) {
	fixture := os.Getenv("WUU_NATIVE_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run the native continuation acceptance script")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	plugin := testRuntimePlugin("example.native-search")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"native-search-decorator.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"web_search": {PluginID: plugin.ID, ToolID: "search"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("example-native")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	replacement, ok := thread.Toolkit.LookupTool("web_search").(*replacementTool)
	require.True(t, ok)
	replacement.continueNative = func(context.Context, providers.ToolCall) (toolresult.Result, error) {
		return toolresult.FromText(`{"action":"web_search","query":"example","evidence":{"id":"original","kind":"search","source":"https://example.test","source_tier":"search_index","retrieved_at":"2026-10-29T00:00:00Z","version_matched_to_repo":"unknown"},"results":[{"title":"source","url":"https://example.test","snippet":"controlled"}]}`), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "example-native", ExecutionID: "turn"})
	value, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "example", Name: "web_search", Arguments: `{"query":"example"}`})
	require.NoError(t, err)
	require.Contains(t, value, "[Example] source")
	block, ok := thread.Toolkit.WebEvidenceContextBlock()
	require.True(t, ok)
	require.Contains(t, block.Content, "id=original")
}
