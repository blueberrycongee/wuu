package runtime

import (
	"bytes"
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"image"
	"image/png"
	"os"
	"path/filepath"
	"strings"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/codemode"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestCodeModeReadFileProjectedContinuation(t *testing.T) {
	t.Setenv("WUU_TOOL_RESULT_PROJECTION", "active")
	root := t.TempDir()
	var content strings.Builder
	for i := 1; i <= 600; i++ {
		fmt.Fprintf(&content, "record-%04d %s\n", i, strings.Repeat("x", 100))
	}
	if err := os.WriteFile(filepath.Join(root, "records.txt"), []byte(content.String()), 0600); err != nil {
		t.Fatal(err)
	}
	kit, err := tools.New(root)
	if err != nil {
		t.Fatal(err)
	}
	kit.SetSessionDir(t.TempDir())
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	// A non-default starting line and bounded range catch replay, gaps, and
	// continuation accidentally escaping the original request.
	source := `const result = await tools.read_file({path:"records.txt",offset:51,limit:400});
 const original = result.content[0].text;
 if (JSON.parse(original).num_lines !== 400) throw Error("canonical range changed");
 let page = JSON.parse(result.model_text), recovered = "", pages = 0;
 while (true) {
   recovered += page.content;
   if (++pages > 100) throw Error("continuation did not terminate");
   if (!page.continuation?.has_more) break;
   const next = await tools.read_file(page.continuation.next);
   page = JSON.parse(next.content[0].text);
 }
 if (pages < 2 || recovered !== original) throw Error("recovery lost or duplicated bytes");
 console.log("READ_CONTINUATION_OK");`
	args, _ := json.Marshal(map[string]any{"code": source, "description": "Read continuation"})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "read-run", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "READ_CONTINUATION_OK" {
		t.Fatalf("read continuation: %+v %v", messages, err)
	}

}

func TestCodeModeForwardsOneReadView(t *testing.T) {
	t.Setenv("WUU_TOOL_RESULT_PROJECTION", "active")
	root := t.TempDir()
	content := strings.Repeat("evidence to retain for the current task\n", 350) + "FINAL_EVIDENCE\n"
	require.NoError(t, os.WriteFile(filepath.Join(root, "evidence.txt"), []byte(content), 0600))
	kit, err := tools.New(root)
	require.NoError(t, err)
	kit.SetSessionID("read-view")
	kit.SetWorkingNotesHome(t.TempDir())
	kit.SetSessionDir(t.TempDir())
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	for _, name := range []string{"file", "checkpoint"} {
		t.Run(name, func(t *testing.T) {
			source := `const r = await tools.read_file({path: "evidence.txt"}); console.log(r.model_text);`
			if name == "checkpoint" {
				encoded, err := json.Marshal(content)
				require.NoError(t, err)
				source = `await tools.notes({action: "write", path: "checkpoint", revision: "", content: ` + string(encoded) + `});
const r = await tools.notes({action: "read", path: "checkpoint", limit: 16000}); console.log(r.model_text);`
			}
			args, err := json.Marshal(map[string]any{"code": source, "description": "Forward one read view"})
			require.NoError(t, err)
			messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: name, Name: "run_code", Arguments: string(args)}}, nil)
			require.NoError(t, err)
			require.Len(t, messages, 1)
			require.False(t, messages[0].ToolResult.IsError, messages[0].Content)
			view := providers.ProjectToolMessage(messages[0]).ToolText
			var page struct {
				Content string `json:"content"`
				Kind    string `json:"kind"`
			}
			require.NoError(t, json.Unmarshal([]byte(view), &page))
			require.Empty(t, page.Kind, "a single read view was archived again")
			require.Contains(t, page.Content, "FINAL_EVIDENCE")
		})
	}
}

func TestCodeModeReadFileImage(t *testing.T) {
	root := t.TempDir()
	var pngBytes bytes.Buffer
	require.NoError(t, png.Encode(&pngBytes, image.NewNRGBA(image.Rect(0, 0, 8, 4))))
	require.NoError(t, os.WriteFile(filepath.Join(root, "screen.png"), pngBytes.Bytes(), 0600))
	kit, err := tools.New(root)
	require.NoError(t, err)
	kit.SetBoundary(tools.UnconfinedBoundary())
	kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
	service := codemode.NewService(codemode.ServiceConfig{})
	defer service.Close()
	kit.ConfigurePTC(service, config.PTCConfig{Enabled: true})
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: kit, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	args, err := json.Marshal(map[string]any{"code": `await tools.read_file({path:"screen.png"})`, "description": "Read image"})
	require.NoError(t, err)
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "image-run", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.NotNil(t, messages[0].ToolResult)
	result := *messages[0].ToolResult
	require.False(t, result.IsError, result.TextProjection())
	projected := providers.ProjectToolResult(result)
	require.Len(t, projected.ObservationImages, 1)
	require.Equal(t, "image/png", projected.ObservationImages[0].MediaType)
	require.Equal(t, base64.StdEncoding.EncodeToString(pngBytes.Bytes()), projected.ObservationImages[0].Data)
	require.NotContains(t, projected.ToolText, "base64")
}
