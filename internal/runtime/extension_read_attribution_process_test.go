package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestExtensionReadAttributionProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := filepath.Join(fixture, "legacy-workspace")
	require.NoError(t, os.WriteFile(filepath.Join(root, "large.txt"), []byte("native placeholder"), 0600))
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("external-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	catalog := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: true}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("external-attribution")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "external-attribution", ExecutionID: "turn"})
	turn := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer turn.Cancel()
	invoke := func(call providers.ToolCall) providers.ChatMessage {
		t.Helper()
		messages, err := turn.ExecuteFinalCalls(ctx, []providers.ToolCall{call}, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		return messages[0]
	}
	direct := invoke(providers.ToolCall{ID: "direct", Name: "read_file", Arguments: `{"path":"large.txt"}`})
	require.NotNil(t, direct.ToolResult)
	var structured map[string]any
	require.NoError(t, json.Unmarshal(direct.ToolResult.StructuredContent, &structured))
	require.Equal(t, "extension", structured["source"])
	require.Equal(t, false, structured["content_verified"])
	code := `const r=await tools.read_file({path:"large.txt"}); console.log(r.structured_content.text.toUpperCase())`
	args, _ := json.Marshal(map[string]any{"code": code, "description": "Read extension evidence"})
	ptc := invoke(providers.ToolCall{ID: "ptc", Name: "run_code", Arguments: string(args)})
	var wrap, clean, outer, inner string
	for _, d := range thread.PluginHost.ToolDefinitions() {
		registration, ok := thread.PluginHost.Tool(d.Name)
		if ok && registration.PluginID == plugin.ID {
			switch registration.Registration.ID {
			case "wrap":
				wrap = d.Name
			case "clean":
				clean = d.Name
			case "outer":
				outer = d.Name
			case "inner":
				inner = d.Name
			}
		}
	}
	require.NotEmpty(t, wrap)
	wrapped := invoke(providers.ToolCall{ID: "wrap", Name: wrap, Arguments: `{}`})
	small := invoke(providers.ToolCall{ID: "wrap-small", Name: wrap, Arguments: `{"small":true}`})
	require.Contains(t, small.Content, "Unverified tool content")
	require.NotEmpty(t, outer)
	require.NotEmpty(t, inner)
	innerArgs, _ := json.Marshal(map[string]string{"name": inner})
	deep := invoke(providers.ToolCall{ID: "deep", Name: outer, Arguments: string(innerArgs)})
	require.Contains(t, deep.Content, "Unverified tool content")
	require.NotEmpty(t, clean)
	unrelated := invoke(providers.ToolCall{ID: "clean", Name: clean, Arguments: `{}`})
	require.NotContains(t, unrelated.Content, "Unverified tool content")
	for index, first := range []providers.ChatMessage{direct, ptc, wrapped} {
		var page map[string]any
		require.NoError(t, json.Unmarshal([]byte(first.Content), &page), "index=%d len=%d prefix=%.300s", index, len(first.Content), first.Content)
		require.Equal(t, "extension", page["source"])
		require.Equal(t, false, page["content_verified"])
		next := page["continuation"].(map[string]any)["next"].(map[string]any)
		count := 0
		for {
			raw, _ := json.Marshal(next)
			require.Less(t, count, 20, "recovery did not advance")
			msg := invoke(providers.ToolCall{ID: fmt.Sprintf("page-%d-%d", index, count), Name: "read_file", Arguments: string(raw)})
			page = make(map[string]any)
			if msg.ToolResult != nil && msg.ToolResult.IsError {
				t.Fatalf("recovery error: %.500s", msg.Content)
			}
			require.NoError(t, json.Unmarshal([]byte(msg.Content), &page))
			require.Equal(t, "extension", page["source"])
			require.Equal(t, false, page["content_verified"])
			count++
			cont := page["continuation"].(map[string]any)
			if cont["has_more"] != true {
				break
			}
			next = cont["next"].(map[string]any)
		}
		require.Greater(t, count, 1)
	}
}
