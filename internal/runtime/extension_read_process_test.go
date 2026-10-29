package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"net/http"
	"net/http/httptest"
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

// Exercise the public selection with a separate runtime process and an existing
// same-session archive created before the replacement was selected.
func TestExtensionReadProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/extension-read-e2e.mjs")
	}
	t.Setenv("WUU_TOOL_RESULT_PROJECTION", "active")
	t.Setenv("WUU_DYNAMIC_CONTEXT_PROJECTION", "off")
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := filepath.Join(fixture, "legacy-workspace"), t.TempDir()
	for _, name := range []string{"native.txt", "external.txt", "bad.txt", "fail.txt", "pause.txt"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), []byte("host-native-data\n"), 0600))
	}
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("external-reader")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	old, err := owner.NewThreadRuntime("read-composition")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "read-composition", ExecutionID: "old-turn"})
	legacyArgs, err := os.ReadFile(filepath.Join(fixture, "legacy_cursor.json"))
	require.NoError(t, err)
	baseline, err := old.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "legacy-before", Name: "read_file", Arguments: string(legacyArgs)})
	require.NoError(t, err)
	require.Contains(t, baseline, "LEGACY_LINE")

	native, err := old.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "native", Name: "read_file", Arguments: `{"path":"native.txt"}`})
	require.NoError(t, err)
	require.Contains(t, native, "host-native-data")
	var producer string
	for _, d := range old.PluginHost.ToolDefinitions() {
		tool, ok := old.PluginHost.Tool(d.Name)
		if ok && tool.PluginID == plugin.ID && tool.Registration.ID == "produce" {
			producer = d.Name
		}
	}
	require.NotEmpty(t, producer)
	rt := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: old.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer rt.Cancel()
	messages, err := rt.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "archived", Name: producer, Arguments: `{}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &first))
	next := first["continuation"].(map[string]any)["next"].(map[string]any)
	cursorArgs, err := json.Marshal(next)
	require.NoError(t, err)
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins, plugin.ID)
	require.NoError(t, err)
	require.NoError(t, owner.ActivatePluginGeneration(candidate, nil))
	nextThread, err := owner.NewThreadRuntime("read-composition")
	require.NoError(t, err)
	newCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "read-composition", ExecutionID: "new-turn"})
	call := providers.ToolCall{ID: "external", Name: "read_file", Arguments: `{"path":"external.txt"}`}
	rich, ok := nextThread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	result, err := rich.ExecuteResult(newCtx, call)
	require.NoError(t, err)
	require.Contains(t, result.TextProjection(), "external:first")
	require.NotContains(t, result.TextProjection(), "host-native-data")
	require.Contains(t, result.TextProjection(), "extension")
	require.Contains(t, result.TextProjection(), "content_verified")
	block, found := nextThread.Toolkit.ActiveFilesContextBlock()
	if found {
		require.NotContains(t, block.Content, "external.txt")
	}
	legacyResult, err := nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "legacy-line", Name: "read_file", Arguments: string(legacyArgs)})
	require.NoError(t, err)
	require.Contains(t, legacyResult, "LEGACY_LINE")
	_, foundNative := nextThread.Toolkit.ActiveFilesContextBlock()
	require.True(t, foundNative, "native continuation must record observation")
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "external-same", Name: "read_file", Arguments: `{"path":"legacy.txt"}`})
	require.NoError(t, err)
	blockAfter, foundNative := nextThread.Toolkit.ActiveFilesContextBlock()
	if foundNative {
		require.NotContains(t, blockAfter.Content, "legacy.txt", "unverified replacement must not retain native observation")
	}
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "legacy-line-again", Name: "read_file", Arguments: string(legacyArgs)})
	require.NoError(t, err)
	blockBeforeImage, foundNative := nextThread.Toolkit.ActiveFilesContextBlock()
	require.True(t, foundNative)
	require.Contains(t, blockBeforeImage.Content, "legacy.txt")
	legacyPath := filepath.Join(root, "legacy.txt")
	legacyBytes, err := os.ReadFile(legacyPath)
	require.NoError(t, err)
	var switched bytes.Buffer
	require.NoError(t, png.Encode(&switched, image.NewNRGBA(image.Rect(0, 0, 3, 3))))
	require.NoError(t, os.WriteFile(legacyPath, switched.Bytes(), 0600))
	t.Cleanup(func() { _ = os.WriteFile(legacyPath, legacyBytes, 0600) })
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "external.png"), switched.Bytes(), 0600))
	imageResult, err := rich.ExecuteResult(newCtx, providers.ToolCall{ID: "external-image-after-text", Name: "read_file", Arguments: `{"path":"legacy.txt"}`})
	require.NoError(t, err)
	require.Len(t, providers.ProjectToolResult(imageResult).ObservationImages, 1)
	blockAfterImage, foundNative := nextThread.Toolkit.ActiveFilesContextBlock()
	if foundNative {
		require.NotContains(t, blockAfterImage.Content, "legacy.txt", "image result must invalidate the prior text observation")
	}
	require.NoError(t, os.WriteFile(legacyPath, legacyBytes, 0600))
	recovered, err := nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "legacy-cursor", Name: "read_file", Arguments: string(cursorArgs)})
	require.NoError(t, err)
	require.Contains(t, recovered, "archived")
	require.NoError(t, os.WriteFile(filepath.Join(root, ".env"), []byte("SECRET=private"), 0600))
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "secret", Name: "read_file", Arguments: `{"path":".env"}`})
	require.Error(t, err, "sensitive read must fail before extension")
	imagePath := filepath.Join(root, "image.bin")
	require.NoError(t, os.WriteFile(imagePath, []byte{0x89, 0x50, 0x4e, 0x47, 0x0d, 0x0a, 0x1a, 0x0a}, 0600))
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "image", Name: "read_file", Arguments: `{"path":"image.bin"}`})
	require.Error(t, err, "images must fail closed")
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "cursor", Name: "read_file", Arguments: `{"continuation":"plugin-cursor"}`})
	require.Error(t, err, "plugin cursors unsupported")
	for _, name := range []string{"fail.txt", "bad.txt"} {
		_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: name, Name: "read_file", Arguments: `{"path":"` + name + `"}`})
		require.Error(t, err)
	}
	nextThread.Toolkit.DisableTools("read_file")
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, call)
	require.Error(t, err)
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "disabled-legacy", Name: "read_file", Arguments: string(legacyArgs)})
	require.Error(t, err)
	nextThread.Toolkit.EnableTools("read_file")
	nextThread.Toolkit.SetAuthorizer(&denyPluginAuthorizer{})
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "denied-legacy", Name: "read_file", Arguments: string(legacyArgs)})
	require.Error(t, err, "host policy must apply to legacy cursor")
	nextThread.Toolkit.SetAuthorizer(nil)
	_, found = nextThread.PluginHost.RetirePlugin(ctx, plugin.ID, fmt.Errorf("removed"))
	require.True(t, found)
	_, err = nextThread.StreamRunner.Tools.Execute(newCtx, call)
	require.Error(t, err, "fresh read must not fall back after plugin retirement")
	nextThread.Toolkit.SetAuthorizer(&denyHiddenReadAuthorizer{})
	legacyAfter, err := nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "legacy-after-retire", Name: "read_file", Arguments: string(legacyArgs)})
	require.NoError(t, err, "host cursor must survive plugin retirement")
	require.Contains(t, legacyAfter, "LEGACY_LINE")
	recoveredAfter, err := nextThread.StreamRunner.Tools.Execute(newCtx, providers.ToolCall{ID: "archive-after-retire", Name: "recover_tool_result", Arguments: string(cursorArgs)})
	require.NoError(t, err, "archive recovery must survive plugin retirement")
	require.Contains(t, recoveredAfter, "archived")
}

func TestExtensionReadPTCProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := filepath.Join(fixture, "legacy-workspace")
	require.NoError(t, os.WriteFile(filepath.Join(root, "ptc.txt"), []byte("native data\n"), 0600))
	plugin := testRuntimePlugin("external-reader")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"fixture.js"}
	catalog := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: true}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &catalog, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("external-ptc")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 40*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "external-ptc", ExecutionID: "turn"})
	rt := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer rt.Cancel()
	source := `const r=await tools.read_file({path:"ptc.txt"}); if(r.structured_content.text!=="external:first\nexternal:second\n" || r.structured_content.source!=="extension" || r.structured_content.content_verified!==false) throw Error("wrong text or source"); console.log("EXTERNAL_PTC_OK")`
	args, err := json.Marshal(map[string]any{"code": source, "description": "Read through external provider"})
	require.NoError(t, err)
	messages, err := rt.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "ptc-read", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, "EXTERNAL_PTC_OK")
	legacyArgs, err := os.ReadFile(filepath.Join(fixture, "legacy_cursor.json"))
	require.NoError(t, err)
	_, found := thread.PluginHost.RetirePlugin(ctx, plugin.ID, fmt.Errorf("retired"))
	require.True(t, found)
	source = `const r=await tools.read_file(` + string(legacyArgs) + `); if(!r.content[0].text.includes("LEGACY_LINE")) throw Error("lost legacy cursor"); console.log("LEGACY_PTC_OK")`
	args, err = json.Marshal(map[string]any{"code": source, "description": "Continue host read"})
	require.NoError(t, err)
	messages, err = rt.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "legacy-ptc", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, "LEGACY_PTC_OK")
	source = `try { await tools.read_file({path:"ptc.txt"}); throw Error("fresh read unexpectedly succeeded") } catch (e) { if(String(e).includes("unexpectedly succeeded")) throw e; console.log("FRESH_DENIED") }`
	args, err = json.Marshal(map[string]any{"code": source, "description": "Reject fresh read after retirement"})
	require.NoError(t, err)
	messages, err = rt.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "fresh-after-retire", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, "FRESH_DENIED")
}

func TestExtensionReadModelProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := filepath.Join(fixture, "legacy-workspace")
	require.NoError(t, os.WriteFile(filepath.Join(root, "model.txt"), []byte("different native contents\n"), 0600))
	plugin := testRuntimePlugin("external-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	requests := make(chan []providers.ChatMessage, 2)
	count := 0
	model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
		var request struct {
			Messages []providers.ChatMessage `json:"messages"`
		}
		if json.NewDecoder(r.Body).Decode(&request) != nil {
			http.Error(w, "bad request", 400)
			return
		}
		requests <- request.Messages
		count++
		w.Header().Set("Content-Type", "text/event-stream")
		if count == 1 {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"tool_calls":[{"index":0,"id":"model-read","type":"function","function":{"name":"read_file","arguments":"{\"path\":\"model.txt\"}"}}]},"finish_reason":"tool_calls"}]}`+"\n\ndata: [DONE]\n\n")
		} else {
			fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
		}
	}))
	defer model.Close()
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	_, err = owner.Sessions.CreateWithMetadata("external-model", root)
	require.NoError(t, err)
	thread, err := owner.NewThreadRuntime("external-model")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "external-model", ExecutionID: "turn"})
	_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Read model.txt"}}, nil)
	require.NoError(t, err)
	<-requests
	var visible string
	for _, message := range <-requests {
		visible += message.Content
	}
	require.Contains(t, visible, "external:first")
	require.NotContains(t, visible, "different native contents")
}

func TestExtensionReadExampleProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(root, "real.txt"), []byte("\ufefffirst\r\nsecond🙂\nthird"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "large.txt"), []byte(strings.Repeat("x", 256*1024+1)), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "invalid.txt"), []byte{0xff, 0xfe}, 0600))
	var exampleImage bytes.Buffer
	require.NoError(t, png.Encode(&exampleImage, image.NewNRGBA(image.Rect(0, 0, 2, 3))))
	require.NoError(t, os.WriteFile(filepath.Join(root, "real-image.png"), exampleImage.Bytes(), 0600))
	plugin := testRuntimePlugin("local-text-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"local-text-reader.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("local-reader")
	require.NoError(t, err)
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "local-reader", ExecutionID: "turn"})
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "read-real", Name: "read_file", Arguments: `{"path":"real.txt","offset":2,"limit":1}`})
	require.NoError(t, err)
	var got map[string]any
	require.NoError(t, json.Unmarshal(result.StructuredContent, &got))
	require.Equal(t, "second🙂\n", got["text"])
	require.Equal(t, float64(3), got["total_lines"])
	require.Equal(t, "extension", got["source"])
	result, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "read-bom", Name: "read_file", Arguments: `{"path":"real.txt","offset":1,"limit":1}`})
	require.NoError(t, err)
	require.NoError(t, json.Unmarshal(result.StructuredContent, &got))
	require.Equal(t, "\ufefffirst\r\n", got["text"], "UTF-8 BOM and line endings must be preserved")
	result, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "read-image", Name: "read_file", Arguments: `{"path":"real-image.png"}`})
	require.NoError(t, err)
	projected := providers.ProjectToolResult(result)
	require.Len(t, projected.ObservationImages, 1)
	require.Equal(t, base64.StdEncoding.EncodeToString(exampleImage.Bytes()), projected.ObservationImages[0].Data)
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "read-large", Name: "read_file", Arguments: `{"path":"large.txt","offset":1,"limit":1}`})
	require.Error(t, err, "example rejects inputs above its documented bound")
	_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "read-invalid", Name: "read_file", Arguments: `{"path":"invalid.txt"}`})
	require.Error(t, err, "example rejects invalid UTF-8")
	// Managed artifacts stay on the pinned host reader even when the logical
	// read_file name is bound to an independent extension.
	sessionDir := thread.Toolkit.SessionDir()
	require.NoError(t, os.WriteFile(filepath.Join(sessionDir, "managed.txt"), []byte("pinned content"), 0600))
	outside := t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(outside, "managed.txt"), []byte("outside content"), 0600))
	pinned := sessionDir + "-pinned"
	require.NoError(t, os.Rename(sessionDir, pinned))
	if err := os.Symlink(outside, sessionDir); err != nil {
		require.NoError(t, os.Rename(pinned, sessionDir))
		t.Skipf("symlink unavailable: %v", err)
	}
	t.Cleanup(func() { _ = os.Remove(sessionDir); _ = os.Rename(pinned, sessionDir) })
	managedArgs, err := json.Marshal(map[string]string{"path": filepath.Join(sessionDir, "managed.txt")})
	require.NoError(t, err)
	managed, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "managed-read", Name: "read_file", Arguments: string(managedArgs)})
	require.NoError(t, err)
	require.Contains(t, managed.TextProjection(), "pinned content")
	require.NotContains(t, managed.TextProjection(), "outside content")
}

type denyHiddenReadAuthorizer struct{}

func (*denyHiddenReadAuthorizer) Authorize(_ context.Context, request tools.AuthorizationRequest) (tools.AuthorizationDecision, error) {
	if request.Tool.Name == "read_file" && request.Tool.Exposure == tools.ToolExposureHidden {
		return tools.AuthorizationDecision{Outcome: "deny", Reason: "hidden tool"}, nil
	}
	return tools.AuthorizationDecision{Outcome: "allow"}, nil
}
