package runtime

import (
	"context"
	"encoding/json"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestNativeToolContinuationSharedCallbackCapabilityProcess(t *testing.T) {
	fixture := os.Getenv("WUU_NATIVE_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("needs compiled fixture")
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
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"tool.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"web_search": {PluginID: plugin.ID, ToolID: "search"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("cross-call")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	replacement, ok := thread.Toolkit.LookupTool("web_search").(*replacementTool)
	require.True(t, ok)
	nativeCalls := make(chan string, 2)
	replacement.continueNative = func(_ context.Context, call providers.ToolCall) (toolresult.Result, error) {
		var args struct {
			Query string `json:"query"`
		}
		_ = json.Unmarshal([]byte(call.Arguments), &args)
		nativeCalls <- args.Query
		payload := map[string]any{"action": "web_search", "query": args.Query, "evidence": map[string]any{"id": "native", "kind": "search", "source": "https://example.test", "source_tier": "search_index", "retrieved_at": "2026-10-29T00:00:00Z", "version_matched_to_repo": "unknown"}, "results": []any{}}
		data, _ := json.Marshal(payload)
		return toolresult.FromText(string(data)), nil
	}
	ctx, cancel := context.WithTimeout(context.Background(), 20*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "cross-call", ExecutionID: "turn"})
	type answer struct {
		name, text string
		err        error
	}
	answers := make(chan answer, 2)
	for _, name := range []string{"share-first", "share-second"} {
		go func(name string) {
			text, err := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: name, Name: "web_search", Arguments: `{"query":"` + name + `"}`})
			answers <- answer{name, text, err}
		}(name)
	}
	got := map[string]answer{}
	for range 2 {
		select {
		case a := <-answers:
			got[a.name] = a
		case <-ctx.Done():
			t.Fatal("callbacks did not finish")
		}
	}
	require.NoError(t, got["share-second"].err)
	require.ErrorContains(t, got["share-first"].err, "already used")
	require.Equal(t, "share-first", <-nativeCalls)
	require.Empty(t, nativeCalls, "sharing must not invoke the second callback's native operation")
}
