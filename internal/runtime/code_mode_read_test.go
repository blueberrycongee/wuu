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
	source := `let args = {path: "records.txt", offset: 51, limit: 400};
 let expected = 51, pages = 0;
 while (args) {
   const result = await tools.read_file(args);
   if (pages === 0 && JSON.parse(result.content[0].text).num_lines !== 400) throw new Error("raw binding result was truncated");
   const page = JSON.parse(result.model_text ?? result.content[0].text);
   const lines = page.content.trimEnd().split("\n");
   if (page.start_line !== expected || page.num_lines !== lines.length) throw new Error("bad page metadata");
   for (const line of lines) {
     const match = line.match(/^\s*(\d*)\|record-(\d+) x+$/);
     if (!match || (match[1] && Number(match[1]) !== expected) || Number(match[2]) !== expected) throw new Error("gap or duplicate at " + expected);
     expected++;
   }
   if (page.range.end_line !== expected - 1) throw new Error("bad range");
   args = page.continuation?.has_more ? page.continuation.next : null;
   if (++pages > 100) throw new Error("continuation did not terminate");
 }
 if (pages < 2 || expected !== 451) throw new Error("incomplete requested range: pages=" + pages + " next=" + expected);
 console.log("READ_CONTINUATION_OK");`
	args, _ := json.Marshal(map[string]any{"code": source, "description": "Read continuation"})
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "read-run", Name: "run_code", Arguments: string(args)}}, nil)
	if err != nil || len(messages) != 1 || messages[0].Content != "READ_CONTINUATION_OK" {
		t.Fatalf("read continuation: %+v %v", messages, err)
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
