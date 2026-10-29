package runtime

import (
	"bytes"
	"context"
	"encoding/json"
	"image"
	"image/color"
	"image/png"
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
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/stretchr/testify/require"
)

func TestManagedReadPluginProcess(t *testing.T) {
	fixture := os.Getenv("WUU_MANAGED_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/managed-read-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	plugin := testRuntimePlugin("managed-read-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	first, err := owner.NewThreadRuntime("managed-first")
	require.NoError(t, err)
	defer first.CloseArtifacts()
	defer owner.ReleasePluginGeneration(first.PluginGeneration)
	other, err := owner.NewThreadRuntime("managed-other")
	require.NoError(t, err)
	defer other.CloseArtifacts()
	defer owner.ReleasePluginGeneration(other.PluginGeneration)
	var reader string
	for _, d := range first.PluginHost.ToolDefinitions() {
		r, ok := first.PluginHost.Tool(d.Name)
		if ok && r.PluginID == plugin.ID && r.Registration.ID == "read" {
			reader = d.Name
		}
	}
	require.NotEmpty(t, reader)
	first.Toolkit.SetImageInputSupported(true)
	firstDir, otherDir := first.Toolkit.SessionDir(), other.Toolkit.SessionDir()
	makePNG := func(c color.RGBA) []byte {
		t.Helper()
		imageData := image.NewRGBA(image.Rect(0, 0, 1, 1))
		imageData.SetRGBA(0, 0, c)
		var buf bytes.Buffer
		require.NoError(t, png.Encode(&buf, imageData))
		return buf.Bytes()
	}
	require.NoError(t, os.WriteFile(filepath.Join(firstDir, "image.png"), makePNG(color.RGBA{R: 255, A: 255}), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(firstDir, "read.txt"), []byte("PINNED-SESSION"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(otherDir, "secret.txt"), []byte("FOREIGN-SESSION"), 0600))
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "managed-first", ExecutionID: "turn"})
	turn := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: first.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer turn.Cancel()
	read := func(id, path string) providers.ChatMessage {
		t.Helper()
		args, _ := json.Marshal(map[string]string{"path": path})
		messages, err := turn.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: id, Name: reader, Arguments: string(args)}}, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		return messages[0]
	}
	imageData := func(message providers.ChatMessage) string {
		t.Helper()
		require.NotNil(t, message.ToolResult)
		for _, part := range message.ToolResult.Content {
			if part.Type == toolresult.ContentTypeImage {
				return part.Data
			}
		}
		t.Fatalf("image result absent: %+v", message.ToolResult)
		return ""
	}
	originalImage := imageData(read("image", filepath.Join(firstDir, "image.png")))
	page := read("normal", filepath.Join(firstDir, "read.txt"))
	require.Contains(t, page.Content, "PINNED-SESSION")
	foreign := read("foreign", filepath.Join(otherDir, "secret.txt"))
	require.NotContains(t, foreign.Content, "FOREIGN-SESSION")
	require.True(t, foreign.ToolResult != nil && foreign.ToolResult.IsError, "foreign=%+v", foreign)
	pinned := firstDir + "-pinned"
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "read.txt"), []byte("REPLACEMENT"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(outside, "image.png"), makePNG(color.RGBA{B: 255, A: 255}), 0600))
	require.NoError(t, os.Rename(firstDir, pinned))
	if err := os.Symlink(outside, firstDir); err == nil {
		page = read("moved", filepath.Join(firstDir, "read.txt"))
		require.Contains(t, page.Content, "PINNED-SESSION")
		require.NotContains(t, page.Content, "REPLACEMENT")
		require.Equal(t, originalImage, imageData(read("moved-image", filepath.Join(firstDir, "image.png"))))
		require.NoError(t, os.Remove(firstDir))
	} else {
		t.Logf("symlink unavailable: %v", err)
	}
	require.NoError(t, os.Rename(pinned, firstDir))
	first.Toolkit.DisableTools("read_file")
	denied := read("disabled", filepath.Join(firstDir, "read.txt"))
	require.NotContains(t, denied.Content, "PINNED-SESSION")
	require.True(t, denied.ToolResult != nil && denied.ToolResult.IsError, "denied=%+v", denied)
}
