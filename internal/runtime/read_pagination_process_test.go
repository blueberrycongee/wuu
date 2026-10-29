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
	"sync"
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

func TestExtensionReadPaginationProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := filepath.Join(fixture, "legacy-workspace")
	require.NoError(t, os.MkdirAll(root, 0700))
	require.NoError(t, os.WriteFile(filepath.Join(root, "paged.txt"), []byte("native source\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "cancel-page.txt"), []byte("native source\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "long-paged.txt"), []byte("native source\n"), 0600))
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("external-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("pages")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "pages", ExecutionID: "turn"})
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	entered := make(chan struct{}, 1)
	release := make(chan struct{})
	var releaseOnce sync.Once
	gate := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) { entered <- struct{}{}; <-release }))
	defer gate.Close()
	defer releaseOnce.Do(func() { close(release) })
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(gate.URL), 0600))
	cancelCtx, cancelPage := context.WithCancel(ctx)
	type canceledResult struct {
		result toolresult.Result
		err    error
	}
	done := make(chan canceledResult, 1)
	go func() {
		result, err := rich.ExecuteResult(cancelCtx, providers.ToolCall{ID: "cancel-page", Name: "read_file", Arguments: `{"path":"cancel-page.txt"}`})
		done <- canceledResult{result, err}
	}()
	select {
	case <-entered:
	case <-ctx.Done():
		t.Fatal("plugin did not enter the page callback")
	}
	cancelPage()
	releaseOnce.Do(func() { close(release) })
	canceled := <-done
	require.Error(t, canceled.err)
	require.NotContains(t, canceled.result.TextProjection(), "wuu-read-v1:")
	read := func(id, args string) (toolresult.Result, error) {
		return rich.ExecuteResult(ctx, providers.ToolCall{ID: id, Name: "read_file", Arguments: args})
	}
	turn := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer turn.Cancel()
	messages, err := turn.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "long-page", Name: "read_file", Arguments: `{"path":"long-paged.txt"}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	var archived map[string]any
	require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &archived))
	require.Equal(t, "extension", archived["source"])
	var recovered strings.Builder
	recovered.WriteString(archived["content"].(string))
	archiveCursor := archived["continuation"].(map[string]any)
	for i := 0; archiveCursor["has_more"] == true; i++ {
		require.Less(t, i, 20, "archive must make progress")
		args, err := json.Marshal(archiveCursor["next"])
		require.NoError(t, err)
		messages, err = turn.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: fmt.Sprintf("archive-%d", i), Name: "recover_tool_result", Arguments: string(args)}}, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		var page map[string]any
		require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &page))
		require.Equal(t, "extension", page["source"])
		recovered.WriteString(page["content"].(string))
		archiveCursor = page["continuation"].(map[string]any)
	}
	var recoveredPage map[string]any
	require.NoError(t, json.NewDecoder(strings.NewReader(recovered.String())).Decode(&recoveredPage))
	pluginNext := recoveredPage["continuation"].(map[string]any)["next"]
	pluginArgs, err := json.Marshal(pluginNext)
	require.NoError(t, err)
	last, err := read("long-page-next", string(pluginArgs))
	require.NoError(t, err)
	require.Contains(t, last.TextProjection(), "finish")
	first, err := read("first", `{"path":"paged.txt","limit":4}`)
	require.NoError(t, err)
	require.NotContains(t, first.TextProjection(), "private-")
	var page map[string]any
	require.NoError(t, json.Unmarshal(first.StructuredContent, &page))
	require.Equal(t, "one\ntwo\n", page["text"])
	token := page["continuation"].(map[string]any)["next"].(map[string]any)["continuation"].(string)
	require.True(t, strings.HasPrefix(token, "wuu-read-v1:"))
	nextArgs, _ := json.Marshal(map[string]string{"continuation": token})
	second, err := read("next", string(nextArgs))
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(second.StructuredContent, &page))
	require.Equal(t, "three\nfour\n", page["text"])
	require.NotContains(t, string(second.StructuredContent), "private-")
	again, err := read("retry", string(nextArgs))
	require.NoError(t, err)
	require.Equal(t, second.TextProjection(), again.TextProjection())
	_, err = read("forged", `{"continuation":"wuu-read-v1:forged"}`)
	require.Error(t, err)
	extra, _ := json.Marshal(map[string]string{"continuation": token, "path": "paged.txt"})
	_, err = read("altered", string(extra))
	require.Error(t, err)
	otherThread, err := owner.NewThreadRuntime("other-pages")
	require.NoError(t, err)
	_, err = otherThread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "cross-session", Name: "read_file", Arguments: string(nextArgs)})
	require.Error(t, err, "a different conversation cannot consume this cursor")
	thread.Toolkit.DisableTools("read_file")
	_, err = read("disabled", string(nextArgs))
	require.Error(t, err)
	thread.Toolkit.EnableTools("read_file")
	var pluginName string
	for _, definition := range thread.PluginHost.ToolDefinitions() {
		registered, ok := thread.PluginHost.Tool(definition.Name)
		if ok && registered.PluginID == plugin.ID && registered.Registration.ID == "read" {
			pluginName = definition.Name
		}
	}
	require.NotEmpty(t, pluginName)
	thread.Toolkit.DisableTools(pluginName)
	_, err = read("disabled-provider", string(nextArgs))
	require.Error(t, err)
	thread.Toolkit.EnableTools(pluginName)
	thread.Toolkit.SetAuthorizer(&denyResolvedPagePath{})
	_, err = read("denied-path", string(nextArgs))
	require.Error(t, err, "host must authorize recovered path")
	thread.Toolkit.SetAuthorizer(nil)
	originalActor, originalPath := thread.Toolkit.ExecutionActor()
	thread.Toolkit.SetAgentIdentity("other", "/root/other")
	_, err = read("other-actor", string(nextArgs))
	require.Error(t, err)
	thread.Toolkit.SetAgentIdentity(originalActor, originalPath)
	candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins, plugin.ID)
	require.NoError(t, err)
	require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
	next, err := owner.NewThreadRuntime("pages")
	require.NoError(t, err)
	_, err = next.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "new-generation", Name: "read_file", Arguments: string(nextArgs)})
	require.Error(t, err, "new generation must not consume old plugin state")
	oldPage, err := read("retained-generation", string(nextArgs))
	require.NoError(t, err, "the retained owner can still consume its own cursor")
	require.Contains(t, oldPage.TextProjection(), "three")
	_, found := thread.PluginHost.RetirePlugin(ctx, plugin.ID, fmt.Errorf("retired"))
	require.True(t, found)
	_, err = read("retired", string(nextArgs))
	require.Error(t, err)
}

type denyResolvedPagePath struct{}

func (*denyResolvedPagePath) Authorize(_ context.Context, request tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if request.Tool.Name == "read_file" && strings.Contains(request.Arguments, `"path":"paged.txt"`) {
		return tools.AuthorizationDecision{Outcome: "deny", Reason: "path denied"}, nil
	}
	return tools.AuthorizationDecision{Outcome: "allow"}, nil
}
