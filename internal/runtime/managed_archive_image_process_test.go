package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/png"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
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

func TestManagedArchiveImageProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := filepath.Join(fixture, "legacy-workspace")
	require.NoError(t, os.MkdirAll(root, 0700))
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("external-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: true}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("archive-image")
	require.NoError(t, err)
	archiveDir := filepath.Join(thread.Toolkit.SessionDir(), "tool-results")
	require.NoError(t, os.MkdirAll(archiveDir, 0700))
	var data bytes.Buffer
	require.NoError(t, png.Encode(&data, image.NewNRGBA(image.Rect(0, 0, 2, 2))))
	archive := filepath.Join(archiveDir, strings.Repeat("a", 64)+".txt")
	plain := filepath.Join(thread.Toolkit.SessionDir(), "normal.png")
	require.NoError(t, os.WriteFile(archive, data.Bytes(), 0600))
	require.NoError(t, os.WriteFile(plain, data.Bytes(), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "archive-image", ExecutionID: "turn"})
	rt := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer rt.Cancel()
	invoke := func(call providers.ToolCall) providers.ChatMessage {
		t.Helper()
		msgs, err := rt.ExecuteFinalCalls(ctx, []providers.ToolCall{call}, nil)
		require.NoError(t, err)
		require.Len(t, msgs, 1)
		return msgs[0]
	}
	args, err := json.Marshal(map[string]string{"path": archive})
	require.NoError(t, err)
	direct := invoke(providers.ToolCall{ID: "archive", Name: "read_file", Arguments: string(args)})
	require.NotNil(t, direct.ToolResult)
	require.False(t, direct.ToolResult.IsError, direct.Content)
	require.Len(t, providers.ProjectToolResult(*direct.ToolResult).ObservationImages, 1)
	require.Contains(t, direct.Content, `"source":"tool_result_archive"`)
	require.Contains(t, direct.Content, `"content_verified":false`)
	code, _ := json.Marshal(map[string]string{"code": `await tools.read_file(` + string(args) + `); console.log("image consumed")`, "description": "Read archive image"})
	ptc := invoke(providers.ToolCall{ID: "archive-ptc", Name: "run_code", Arguments: string(code)})
	require.NotNil(t, ptc.ToolResult)
	require.False(t, ptc.ToolResult.IsError, ptc.Content)
	require.Len(t, providers.ProjectToolResult(*ptc.ToolResult).ObservationImages, 1)
	require.Contains(t, ptc.Content, "Unverified tool content")
	var wrap, clean string
	for _, d := range thread.PluginHost.ToolDefinitions() {
		r, ok := thread.PluginHost.Tool(d.Name)
		if ok && r.PluginID == plugin.ID {
			if r.Registration.ID == "imagewrap" {
				wrap = d.Name
			}
			if r.Registration.ID == "clean" {
				clean = d.Name
			}
		}
	}
	require.NotEmpty(t, wrap)
	nested := invoke(providers.ToolCall{ID: "nested-archive", Name: wrap, Arguments: string(args)})
	require.Contains(t, nested.Content, "nested image observed")
	require.Contains(t, nested.Content, "Unverified tool content")
	require.NotEmpty(t, clean)
	unrelated := invoke(providers.ToolCall{ID: "clean", Name: clean, Arguments: `{}`})
	require.NotContains(t, unrelated.Content, "Unverified tool content")
	ordinaryArgs, _ := json.Marshal(map[string]string{"path": plain})
	ordinary := invoke(providers.ToolCall{ID: "ordinary", Name: "read_file", Arguments: string(ordinaryArgs)})
	require.NotNil(t, ordinary.ToolResult)
	require.Len(t, providers.ProjectToolResult(*ordinary.ToolResult).ObservationImages, 1)
	require.NotContains(t, ordinary.Content, "Unverified tool content")
	require.NotContains(t, ordinary.Content, "tool_result_archive")
	thread.Toolkit.DisableTools("read_file")
	denied := invoke(providers.ToolCall{ID: "disabled", Name: "read_file", Arguments: string(args)})
	require.NotNil(t, denied.ToolResult)
	require.True(t, denied.ToolResult.IsError)
	require.Empty(t, providers.ProjectToolResult(*denied.ToolResult).ObservationImages)
}
