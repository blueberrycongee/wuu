package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/color"
	"image/gif"
	"image/png"
	"io"
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

func TestExtensionReadImageModelProcess(t *testing.T) {
	fixture := os.Getenv("WUU_EXTENSION_READ_FIXTURE")
	if fixture == "" {
		t.Skip("run extension-read e2e")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	encode := func(c color.NRGBA) []byte {
		img := image.NewNRGBA(image.Rect(0, 0, 4, 3))
		img.SetNRGBA(0, 0, c)
		var buf bytes.Buffer
		require.NoError(t, png.Encode(&buf, img))
		return buf.Bytes()
	}
	native, external := encode(color.NRGBA{R: 255, A: 255}), encode(color.NRGBA{B: 255, A: 255})
	require.NotEqual(t, native, external)
	root := filepath.Join(fixture, "legacy-workspace")
	kit, err := tools.New(root)
	require.NoError(t, err)
	schema, err := json.Marshal(kit.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "plugin-image.png"), native, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "evil-image.png"), native, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(root, "plain-error.png"), native, 0600))
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "external.png"), external, 0600))
	frame := image.NewPaletted(image.Rect(0, 0, 2, 2), color.Palette{color.Black, color.White})
	var staticGIF, animatedGIF bytes.Buffer
	require.NoError(t, gif.Encode(&staticGIF, frame, nil))
	require.NoError(t, gif.EncodeAll(&animatedGIF, &gif.GIF{Image: []*image.Paletted{frame, frame}, Delay: []int{0, 0}}))
	for _, name := range []string{"static.gif", "animated.gif"} {
		require.NoError(t, os.WriteFile(filepath.Join(root, name), staticGIF.Bytes(), 0600))
	}
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "static.gif"), staticGIF.Bytes(), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "animated.gif"), animatedGIF.Bytes(), 0600))
	for _, ptc := range []bool{false, true} {
		t.Run(fmt.Sprintf("ptc=%t", ptc), func(t *testing.T) {
			plugin := testRuntimePlugin("external-reader")
			plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
			plugins := []pluginpkg.Plugin{plugin}
			requests := make(chan []byte, 2)
			count := 0
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				body, err := io.ReadAll(r.Body)
				if err != nil {
					http.Error(w, "bad request", 400)
					return
				}
				requests <- body
				count++
				w.Header().Set("Content-Type", "text/event-stream")
				if count == 1 {
					name, args := "read_file", `{"path":"plugin-image.png"}`
					if ptc {
						name = "run_code"
						value, _ := json.Marshal(map[string]string{"code": `await tools.read_file({path:"plugin-image.png"})`, "description": "Read image"})
						args = string(value)
					}
					frame, _ := json.Marshal(map[string]any{"choices": []any{map[string]any{"index": 0, "delta": map[string]any{"tool_calls": []any{map[string]any{"index": 0, "id": "image-call", "type": "function", "function": map[string]any{"name": name, "arguments": args}}}}, "finish_reason": "tool_calls"}}})
					fmt.Fprintf(w, "data: %s\n\ndata: [DONE]\n\n", frame)
				} else {
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
				}
			}))
			defer model.Close()
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: ptc}}
			cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
			owner, err := NewSession(Options{RootDir: root, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			_, err = owner.Sessions.CreateWithMetadata("image-model", root)
			require.NoError(t, err)
			thread, err := owner.NewThreadRuntime("image-model")
			require.NoError(t, err)
			ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
			defer cancel()
			ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "image-model", ExecutionID: "turn"})
			if ptc {
				rt := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
				defer rt.Cancel()
				args, _ := json.Marshal(map[string]string{"code": `await tools.read_file({path:"evil-image.png"})`, "description": "Reject rich error"})
				messages, err := rt.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "evil-ptc", Name: "run_code", Arguments: string(args)}}, nil)
				require.NoError(t, err)
				require.Len(t, messages, 1)
				if messages[0].ToolResult != nil {
					require.Empty(t, providers.ProjectToolResult(*messages[0].ToolResult).ObservationImages, "an error cannot smuggle image output")
				}
			} else {
				rich, ok := thread.StreamRunner.Tools.(agent.RichToolExecutor)
				require.True(t, ok)
				result, _ := rich.ExecuteResult(ctx, providers.ToolCall{ID: "evil-direct", Name: "read_file", Arguments: `{"path":"evil-image.png"}`})
				require.Empty(t, providers.ProjectToolResult(result).ObservationImages, "an error cannot smuggle image output")
				result, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "plain-error", Name: "read_file", Arguments: `{"path":"plain-error.png"}`})
				require.NoError(t, err)
				require.True(t, result.IsError)
				require.Contains(t, result.TextProjection(), "reader denied image")
				result, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "static-gif", Name: "read_file", Arguments: `{"path":"static.gif"}`})
				require.NoError(t, err)
				require.Len(t, providers.ProjectToolResult(result).ObservationImages, 1)
				result, err = rich.ExecuteResult(ctx, providers.ToolCall{ID: "animated-gif", Name: "read_file", Arguments: `{"path":"animated.gif"}`})
				require.ErrorContains(t, err, "animated GIF")
				require.Empty(t, providers.ProjectToolResult(result).ObservationImages)
			}
			_, err = thread.StreamRunner.RunWithCallback(ctx, []providers.ChatMessage{{Role: "user", Content: "Inspect plugin-image.png"}}, nil)
			require.NoError(t, err)
			<-requests
			actual := string(<-requests)
			require.Equal(t, 1, strings.Count(actual, base64.StdEncoding.EncodeToString(external)), "actual extension image must reach model once")
			require.NotContains(t, actual, base64.StdEncoding.EncodeToString(native), "host must not substitute native image")
		})
	}
}
