package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/png"
	"io"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

// Uses the actual SDK process, execution worker, model transport and PTC.
func TestRemoteImageReaderProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	encode := func(c color.NRGBA) []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
		img.SetNRGBA(0, 0, c)
		var b bytes.Buffer
		require.NoError(t, png.Encode(&b, img))
		return b.Bytes()
	}
	native := encode(color.NRGBA{R: 255, A: 255})
	alternate := encode(color.NRGBA{B: 255, A: 255})
	require.NotEqual(t, native, alternate)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "alternate.png"), alternate, 0600))
	for _, ptc := range []bool{false, true} {
		t.Run(fmt.Sprintf("ptc=%t", ptc), func(t *testing.T) {
			local, remote := t.TempDir(), t.TempDir()
			for _, name := range []string{"image.png", "bad.png", "too-large.png", "pause.png", "success-parent.png", "failed-parent.png"} {
				require.NoError(t, os.WriteFile(filepath.Join(remote, name), native, 0600))
			}
			require.NoError(t, os.WriteFile(filepath.Join(remote, "forge.txt"), []byte("ordinary text\n"), 0600))
			require.NoError(t, os.WriteFile(filepath.Join(remote, "invalid.png"), []byte{137, 80, 78, 71, 13, 10, 26, 10, 0}, 0600))
			require.NoError(t, os.WriteFile(filepath.Join(remote, "oversized-native.png"), native, 0600))
			require.NoError(t, os.Truncate(filepath.Join(remote, "oversized-native.png"), 20*1024*1024+1))
			require.NoError(t, os.WriteFile(filepath.Join(local, "image.png"), alternate, 0600))
			kit, err := tools.New(local)
			require.NoError(t, err)
			schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
			plugin := testRuntimePlugin("fixture.remote-image")
			plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"image-fixture.js"}
			plugins := []pluginpkg.Plugin{plugin}
			requests := make(chan []byte, 4)
			calls := 0
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "bad input", 400)
					return
				}
				requests <- body
				calls++
				w.Header().Set("Content-Type", "text/event-stream")
				if calls == 1 {
					name, args := "read_file", `{"path":"image.png"}`
					if ptc {
						name = "run_code"
						encoded, _ := json.Marshal(map[string]string{"code": `await tools.read_file({path:"image.png"})`, "description": "Read image"})
						args = string(encoded)
					}
					frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "image-call", "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
				} else {
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
				}
			}))
			defer model.Close()
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: ptc}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
			cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
			owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			_, err = owner.Sessions.CreateWithMetadata("remote-image", local)
			require.NoError(t, err)
			require.NoError(t, owner.PinExecutionEnvironment("remote-image", ""))
			thread, err := owner.NewThreadRuntime("remote-image")
			require.NoError(t, err)
			defer owner.ReleasePluginGeneration(thread.PluginGeneration)
			defer thread.AgentControl.Close()
			thread.Toolkit.SetPermissionMode("standard")
			thread.Toolkit.SetGitAttributionEnabled(true)
			ctx, cancel := context.WithTimeout(context.Background(), 50*time.Second)
			defer cancel()
			ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "remote-image", ExecutionID: "turn"})
			_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Inspect image"}}, nil)
			require.NoError(t, err)
			<-requests
			request := string(<-requests)
			require.Equal(t, 1, strings.Count(request, base64.StdEncoding.EncodeToString(alternate)), "replacement image reaches the model once")
			require.NotContains(t, request, base64.StdEncoding.EncodeToString(native), "worker image is not substituted for plugin output")
			rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
			require.True(t, ok)
			for _, name := range []string{"forge.txt", "invalid.png", "oversized-native.png", "too-large.png", "bad.png"} {
				result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: name, Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, name)})
				require.Error(t, err, name)
				require.Empty(t, providers.ProjectToolResult(result).ObservationImages, name)
			}
			_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "range", Name: "read_file", Arguments: `{"path":"image.png","offset":2}`})
			require.Error(t, err)
			thread.Toolkit.DisableTools("read_file")
			_, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "disabled", Name: "read_file", Arguments: `{"path":"image.png"}`})
			require.Error(t, err)
			thread.Toolkit.EnableTools("read_file")
			if !ptc {
				entered, release := make(chan struct{}, 1), make(chan struct{})
				barrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					entered <- struct{}{}
					<-release
					_, _ = w.Write([]byte("ok"))
				}))
				defer barrier.Close()
				require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(barrier.URL), 0600))
				var revoked atomic.Bool
				thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, _ providers.ToolCall) error {
					if info.Name == "read_file" && revoked.Load() {
						return fmt.Errorf("revoked during direct image replacement")
					}
					return nil
				})
				type resultAndError struct {
					result toolresult.Result
					err    error
				}
				done := make(chan resultAndError, 1)
				go func() {
					result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "direct-pause", Name: "read_file", Arguments: `{"path":"pause.png"}`})
					done <- resultAndError{result, err}
				}()
				select {
				case <-entered:
				case <-time.After(10 * time.Second):
					close(release)
					t.Fatal("plugin did not finish its native continuation")
				}
				revoked.Store(true)
				close(release)
				select {
				case outcome := <-done:
					require.ErrorContains(t, outcome.err, "revoked during direct image replacement")
					require.Empty(t, providers.ProjectToolResult(outcome.result).ObservationImages)
				case <-time.After(10 * time.Second):
					t.Fatal("revoked direct read did not settle")
				}
				thread.Toolkit.SetPermissionRequestHook(nil)
			}
			if ptc {
				actor, _ := thread.Toolkit.ExecutionActor()
				remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
				require.True(t, ok)
				makePriorObservation := func(name string) executionenv.ToolRequest {
					t.Helper()
					filename := filepath.Join(remote, name)
					require.NoError(t, os.WriteFile(filename, []byte("prior native text\n"), 0600))
					req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: fmt.Sprintf(`{"path":%q}`, name)}}
					_, err := thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
					require.NoError(t, err)
					require.NoError(t, os.WriteFile(filename, native, 0600))
					return req
				}
				checkObservation := func(req executionenv.ToolRequest, expected bool) {
					t.Helper()
					ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
					require.NoError(t, err)
					changed, err := ticket.Commit(ctx)
					require.NoError(t, err)
					require.Equal(t, expected, changed)
				}
				for _, name := range []string{"failed-parent.png", "success-parent.png"} {
					req := makePriorObservation(name)
					_ = os.Remove(filepath.Join(fixture, "observed-image"))
					code := fmt.Sprintf(`await tools.read_file({path:%q}); console.log("CHILD_OK");`, name)
					if name == "failed-parent.png" {
						code += ` throw new Error("controlled parent failure")`
					}
					args, err := json.Marshal(map[string]string{"code": code, "description": "Read image"})
					require.NoError(t, err)
					run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: name, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
					messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: name, Name: "run_code", Arguments: string(args)}}, nil)
					run.Cancel()
					require.NoError(t, err)
					require.Len(t, messages, 1)
					observed, err := os.ReadFile(filepath.Join(fixture, "observed-image"))
					require.NoError(t, err)
					require.Equal(t, name, string(observed))
					if name == "failed-parent.png" {
						require.NotContains(t, messages[0].Content, "CHILD_OK")
						require.Empty(t, providers.ProjectToolResult(*messages[0].ToolResult).ObservationImages)
					} else {
						require.Contains(t, messages[0].Content, "CHILD_OK")
						require.Len(t, providers.ProjectToolResult(*messages[0].ToolResult).ObservationImages, 1)
					}
					require.Equal(t, name == "failed-parent.png", messages[0].ToolResult.IsError)
					checkObservation(req, name == "failed-parent.png")
				}
				req := makePriorObservation("pause.png")
				entered, release := make(chan struct{}, 1), make(chan struct{})
				barrier := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, _ *http.Request) {
					entered <- struct{}{}
					<-release
					_, _ = w.Write([]byte("ok"))
				}))
				defer barrier.Close()
				require.NoError(t, os.WriteFile(filepath.Join(fixture, "control.txt"), []byte(barrier.URL), 0600))
				var revoked atomic.Bool
				thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, _ providers.ToolCall) error {
					if info.Name == "read_file" && revoked.Load() {
						return fmt.Errorf("revoked during image replacement")
					}
					return nil
				})
				run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "revoked-image", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
				defer run.Cancel()
				done := make(chan []providers.ChatMessage, 1)
				fail := make(chan error, 1)
				go func() {
					messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "pause", Name: "read_file", Arguments: req.Call.Arguments}}, nil)
					if err != nil {
						fail <- err
						return
					}
					done <- messages
				}()
				select {
				case <-entered:
				case <-time.After(10 * time.Second):
					t.Fatal("plugin did not stage remote image")
				}
				revoked.Store(true)
				close(release)
				select {
				case err := <-fail:
					require.Error(t, err)
				case messages := <-done:
					require.Len(t, messages, 1)
					require.True(t, messages[0].ToolResult.IsError)
				case <-time.After(10 * time.Second):
					t.Fatal("revoked read did not settle")
				}
				thread.Toolkit.SetPermissionRequestHook(nil)
				checkObservation(req, true)
			}
		})
	}
	t.Run("image-contract-required", func(t *testing.T) {
		local, remote := t.TempDir(), t.TempDir()
		require.NoError(t, os.WriteFile(filepath.Join(remote, "image.png"), native, 0600))
		require.NoError(t, os.WriteFile(filepath.Join(fixture, "disable-image"), nil, 0600))
		defer os.Remove(filepath.Join(fixture, "disable-image"))
		plugin := testRuntimePlugin("fixture.remote-image")
		plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"image-fixture.js"}
		plugins := []pluginpkg.Plugin{plugin}
		cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
		cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
		owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
		require.NoError(t, err)
		defer owner.Cleanup()
		require.NoError(t, owner.PinExecutionEnvironment("text-only", ""))
		thread, err := owner.NewThreadRuntime("text-only")
		require.NoError(t, err)
		defer owner.ReleasePluginGeneration(thread.PluginGeneration)
		defer thread.AgentControl.Close()
		ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
		defer cancel()
		ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "text-only", ExecutionID: "turn"})
		rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
		require.True(t, ok)
		result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "read", Name: "read_file", Arguments: `{"path":"image.png"}`})
		require.ErrorContains(t, err, "remote external image is unavailable")
		require.Empty(t, providers.ProjectToolResult(result).ObservationImages)
	})
}

// Exercise the published example with an independently built worker and SDK.
func TestRemoteImageReaderExampleProcess(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	var b bytes.Buffer
	require.NoError(t, png.Encode(&b, image.NewNRGBA(image.Rect(0, 0, 4, 3))))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "image.png"), b.Bytes(), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "text.txt"), []byte("remote line\n"), 0600))
	plugin := testRuntimePlugin("fixture.remote-image-example")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"remote-image-reader.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("image-example", ""))
	thread, err := owner.NewThreadRuntime("image-example")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("read_only")
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "image-example", ExecutionID: "turn"})
	rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
	require.True(t, ok)
	result, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "image", Name: "read_file", Arguments: `{"path":"image.png"}`})
	require.NoError(t, err)
	projection := providers.ProjectToolResult(result)
	require.Len(t, projection.ObservationImages, 1)
	require.Contains(t, projection.ToolText, `"source":"extension"`)
	text, err := rich.ExecuteResult(ctx, providers.ToolCall{ID: "text", Name: "read_file", Arguments: `{"path":"text.txt"}`})
	require.NoError(t, err)
	require.Contains(t, text.TextProjection(), "[remote] remote line")
}
