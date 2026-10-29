package runtime

import (
	"context"
	"encoding/base64"
	"encoding/json"
	"fmt"
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
	"github.com/stretchr/testify/require"
)

func TestResultRecoveryProcess(t *testing.T) {
	fixture := os.Getenv("WUU_RESULT_RECOVERY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/result-recovery-e2e.mjs")
	}
	t.Setenv("WUU_TOOL_RESULT_PROJECTION", "active")
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	plugin := testRuntimePlugin("recovery-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"fixture.js"}
	example := testRuntimePlugin("recovery-example")
	example.Root = fixture
	example.Runtime.Command = node
	example.Runtime.Args = []string{"result-recovery.js"}
	plugins := []pluginpkg.Plugin{plugin, example}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("recovery-session")
	require.NoError(t, err)
	var producer, orchestrator, exampleTool string
	for _, definition := range thread.PluginHost.ToolDefinitions() {
		registration, ok := thread.PluginHost.Tool(definition.Name)
		if ok && registration.PluginID == example.ID && registration.Registration.ID == "recover" {
			exampleTool = definition.Name
		}
		if ok && registration.PluginID == plugin.ID {
			switch registration.Registration.ID {
			case "produce":
				producer = definition.Name
			case "recover":
				orchestrator = definition.Name
			}
		}
	}
	require.NotEmpty(t, producer)
	require.NotEmpty(t, orchestrator)
	require.NotEmpty(t, exampleTool)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "recovery-session", ExecutionID: "turn"})
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	invoke := func(name, args, id string) providers.ChatMessage {
		t.Helper()
		messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: id, Name: name, Arguments: args}}, nil)
		require.NoError(t, err)
		require.Len(t, messages, 1)
		return messages[0]
	}
	first := invoke(producer, `{}`, "produce")
	var initial map[string]any
	require.NoError(t, json.Unmarshal([]byte(first.Content), &initial))
	require.Equal(t, "archived_tool_result", initial["kind"])
	firstPage, ok := initial["content"].(string)
	require.True(t, ok)
	next := initial["continuation"].(map[string]any)["next"].(map[string]any)
	token := next["continuation"].(string)
	full := strings.Repeat("证据🙂\n", 5000)
	for _, name := range []string{"recover_tool_result", "read_file", orchestrator, exampleTool} {
		t.Run(name, func(t *testing.T) {
			current := token
			var recovered strings.Builder
			recovered.WriteString(firstPage)
			for page := 0; page < 100; page++ {
				raw, _ := json.Marshal(map[string]any{"continuation": current})
				msg := invoke(name, string(raw), fmt.Sprintf("%s-%d", name, page))
				var value map[string]any
				require.NoError(t, json.Unmarshal([]byte(msg.Content), &value), "page %q", msg.Content)
				chunk, _ := value["content"].(string)
				if value["encoding"] == "base64" {
					data, decodeErr := base64.StdEncoding.DecodeString(value["content_base64"].(string))
					require.NoError(t, decodeErr)
					chunk = string(data)
				}
				require.NotEmpty(t, chunk)
				recovered.WriteString(chunk)
				continuation := value["continuation"].(map[string]any)
				if continuation["has_more"] != true {
					break
				}
				current = continuation["next"].(map[string]any)["continuation"].(string)
			}
			if full != recovered.String() {
				t.Fatalf("recovery mismatch: got %d bytes, want %d; tail=%q", recovered.Len(), len(full), recovered.String()[max(0, recovered.Len()-80):])
			}
		})
	}
	raw, _ := json.Marshal(map[string]any{"continuation": token})
	other, err := owner.NewThreadRuntime("another-session")
	require.NoError(t, err)
	otherCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "another-session", ExecutionID: "other-turn"})
	_, err = other.StreamRunner.Tools.Execute(otherCtx, providers.ToolCall{ID: "foreign", Name: "recover_tool_result", Arguments: string(raw)})
	require.Error(t, err)
	thread.Toolkit.DisableTools("recover_tool_result")
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "disabled-recovery", Name: "recover_tool_result", Arguments: string(raw)})
	require.Error(t, err)
	thread.Toolkit.EnableTools("recover_tool_result")
	thread.Toolkit.DisableTools("read_file")
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "disabled", Name: "recover_tool_result", Arguments: string(raw)})
	require.Error(t, err)
	thread.Toolkit.EnableTools("read_file")
	canceled, cancelCall := context.WithCancel(ctx)
	cancelCall()
	_, err = thread.StreamRunner.Tools.Execute(canceled, providers.ToolCall{ID: "canceled", Name: "recover_tool_result", Arguments: string(raw)})
	require.Error(t, err)
	artifact := initial["artifact_ref"].(string)
	require.Equal(t, "tool-results", filepath.Base(filepath.Dir(artifact)))
	require.NoError(t, os.WriteFile(artifact, []byte("changed"), 0600))
	_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "stale", Name: "recover_tool_result", Arguments: string(raw)})
	require.Error(t, err)
}

func TestResultRecoveryPTCProcess(t *testing.T) {
	fixture := os.Getenv("WUU_RESULT_RECOVERY_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/result-recovery-e2e.mjs")
	}
	t.Setenv("WUU_TOOL_RESULT_PROJECTION", "active")
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root, home := t.TempDir(), t.TempDir()
	plugin := testRuntimePlugin("recovery-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, PTC: config.PTCConfig{Enabled: true}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	thread, err := owner.NewThreadRuntime("recovery-ptc")
	require.NoError(t, err)
	var producer string
	for _, def := range thread.PluginHost.ToolDefinitions() {
		reg, ok := thread.PluginHost.Tool(def.Name)
		if ok && reg.PluginID == plugin.ID && reg.Registration.ID == "produce" {
			producer = def.Name
		}
	}
	require.NotEmpty(t, producer)
	ctx, cancel := context.WithTimeout(context.Background(), 45*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "recovery-ptc", ExecutionID: "turn"})
	runtime := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer runtime.Cancel()
	messages, err := runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "produce", Name: producer, Arguments: `{}`}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	var first map[string]any
	require.NoError(t, json.Unmarshal([]byte(messages[0].Content), &first))
	token := first["continuation"].(map[string]any)["next"].(map[string]any)["continuation"].(string)
	quoted, _ := json.Marshal(token)
	code := `const r = await tools.recover_tool_result({continuation:` + string(quoted) + `}); const p=JSON.parse(r.content[0].text); if(!p.content.startsWith("证据🙂")) throw Error("wrong recovery"); console.log("PTC_RECOVERY_OK")`
	args, _ := json.Marshal(map[string]any{"code": code, "description": "Recover archived result"})
	messages, err = runtime.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "run", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.Contains(t, messages[0].Content, "PTC_RECOVERY_OK")
}
