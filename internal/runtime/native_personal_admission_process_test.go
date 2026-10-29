package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"net/http"
	"net/http/httptest"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/composition"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolservice"
	"github.com/blueberrycongee/wuu/internal/workingnotes"
	"github.com/stretchr/testify/require"
)

func TestPersonalDelegatedNativeAdmissionProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run native admission acceptance")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	for _, delegated := range []bool{false, true} {
		t.Run(fmt.Sprintf("delegated=%t", delegated), func(t *testing.T) {
			home := t.TempDir()
			root := t.TempDir()
			marker := filepath.Join(t.TempDir(), "admit")
			secret := filepath.Join(home, "sessions", "coding.txt")
			require.NoError(t, os.MkdirAll(filepath.Dir(secret), 0700))
			require.NoError(t, os.WriteFile(secret, []byte("coding-secret"), 0600))
			var calls atomic.Int32
			transcripts := make(chan []providers.ChatMessage, 3)
			model := httptest.NewServer(http.HandlerFunc(func(w http.ResponseWriter, r *http.Request) {
				var input struct {
					Messages []providers.ChatMessage `json:"messages"`
				}
				_ = json.NewDecoder(r.Body).Decode(&input)
				transcripts <- input.Messages
				w.Header().Set("Content-Type", "text/event-stream")
				if calls.Add(1) == 1 {
					args, _ := json.Marshal(map[string]string{"path": secret})
					nested, _ := json.Marshal(string(args))
					notesArgs, _ := json.Marshal(`{"action":"read","path":"private.md"}`)
					fmt.Fprintf(w, "data: {\"choices\":[{\"index\":0,\"delta\":{\"tool_calls\":[{\"index\":0,\"id\":\"read-secret\",\"type\":\"function\",\"function\":{\"name\":\"read_file\",\"arguments\":%s}},{\"index\":1,\"id\":\"read-notes\",\"type\":\"function\",\"function\":{\"name\":\"notes\",\"arguments\":%s}}]},\"finish_reason\":\"tool_calls\"}]}\n\ndata: [DONE]\n\n", nested, notesArgs)
				} else {
					fmt.Fprint(w, `data: {"choices":[{"index":0,"delta":{"content":"done"},"finish_reason":"stop"}]}`+"\n\ndata: [DONE]\n\n")
				}
			}))
			defer model.Close()
			plugin := testRuntimePlugin("lazy-personal-fixture")
			plugin.Root = fixture
			plugin.Runtime.Command = node
			plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
			plugin.MCPServers = map[string]config.MCPServerConfig{"forbidden": {Command: "/nonexistent/forbidden-mcp"}}
			plugins := []pluginpkg.Plugin{plugin}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: model.URL, APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
			owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: home, UserSpaceID: "local", ProductDomain: "personal", Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			require.True(t, owner.pluginGeneration.userBinding.Matches(owner.UserBinding()))
			clone, err := owner.cloneForThreadModel()
			require.NoError(t, err)
			require.True(t, clone.pluginGeneration.userBinding.Matches(clone.UserBinding()))
			owner.ReleasePluginGeneration(clone.pluginGeneration)
			candidate, err := owner.PreflightDiscoveredExtensions(cfg, plugins)
			require.NoError(t, err)
			require.True(t, candidate.userBinding.Matches(owner.UserBinding()))
			require.NoError(t, candidate.close())
			require.Nil(t, owner.Toolkit)
			if delegated {
				require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
			}
			thread, err := owner.NewThreadRuntime("personal-main")
			require.NoError(t, err)
			defer func() {
				if c := thread.NativeAgentControl(); c != nil {
					c.BeginShutdown()
					c.StopAll()
					_ = c.WaitOwnedWorkerExecutions(context.Background())
					c.Close()
				}
				_ = thread.CloseNativeToolScope(context.Background())
				_ = thread.CloseArtifacts()
				owner.ReleasePluginGeneration(thread.PluginGeneration)
			}()
			kit, control := thread.NativeResources()
			if !delegated {
				require.Nil(t, kit)
				require.Nil(t, control)
				side, err := owner.NewSideThreadRunner("external-side", owner.RootDir, ThreadModelSelection{}, "side prompt", "personal-main")
				require.NoError(t, err)
				defer CloseSideThreadTools(context.Background(), side)
				sideCtx := loopdriver.WithExecutionContext(context.Background(), loopdriver.ExecutionContext{SessionID: "external-side", ExecutionID: "turn"})
				result, err := side.Tools.Execute(sideCtx, providers.ToolCall{ID: "external", Name: "fixture_custom", Arguments: `{}`})
				require.NoError(t, err)
				require.Contains(t, result, "external provider works")
				require.False(t, owner.pluginGeneration.nativeDependencies.initialized)
				return
			}
			require.NotNil(t, kit)
			require.NotNil(t, control)
			require.Nil(t, kit.MCPManager())
			ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
			defer cancel()
			ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "personal-main", ExecutionID: "turn"})
			thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
			secretArg, _ := json.Marshal(map[string]string{"path": secret})
			result, readErr := thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "main-secret", Name: "read_file", Arguments: string(secretArg)})
			require.NotContains(t, result, "coding-secret")
			require.Contains(t, fmt.Sprint(readErr)+result, "protected")
			_, err = thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: "main-notes", Name: "notes", Arguments: `{"action":"write","path":"private.md","content":"personal-main-note","revision":""}`})
			require.NoError(t, err)
			saved, err := (workingnotes.Store{Home: owner.ProductDataRoot()}).Read("personal-main")
			require.NoError(t, err)
			require.NotNil(t, saved)
			require.Contains(t, *saved, "personal-main-note")
			shared, err := (workingnotes.Store{Home: home}).Read("personal-main")
			require.NoError(t, err)
			require.Nil(t, shared)
			side, err := owner.NewSideThreadRunner("personal-side", owner.RootDir, ThreadModelSelection{}, "side prompt", "personal-main")
			require.NoError(t, err)
			defer CloseSideThreadTools(context.Background(), side)
			sideCtx := loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "personal-side", ExecutionID: "side-turn"})
			result, readErr = side.Tools.Execute(sideCtx, providers.ToolCall{ID: "side-secret", Name: "read_file", Arguments: string(secretArg)})
			require.NotContains(t, result, "coding-secret")
			require.Contains(t, fmt.Sprint(readErr)+result, "protected")
			require.NoError(t, (workingnotes.Store{Home: owner.ProductDataRoot()}).CompareAndSwap("personal-side", nil, `{"side.md":"personal-side-note"}`))
			result, err = side.Tools.Execute(sideCtx, providers.ToolCall{ID: "side-notes", Name: "notes", Arguments: `{"action":"read","path":"side.md"}`})
			require.NoError(t, err)
			require.Contains(t, result, "personal-side-note")
			_, err = control.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "personal_worker", Prompt: "read", Synchronous: true})
			require.NoError(t, err)
			<-transcripts
			select {
			case messages := <-transcripts:
				var text strings.Builder
				for _, m := range messages {
					text.WriteString(m.Content)
				}
				require.NotContains(t, text.String(), "coding-secret")
				require.Contains(t, text.String(), "protected")
				require.Contains(t, text.String(), `tool \"notes\" is not available`)
				require.NotContains(t, text.String(), "personal-main-note")
			case <-ctx.Done():
				t.Fatal(ctx.Err())
			}
		})
	}
}

func TestPersonalSideLateNativeBoundaryProcess(t *testing.T) {
	fixture := os.Getenv("WUU_CONTINUATION_FIXTURE")
	if fixture == "" {
		t.Skip("run native admission acceptance")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	home, root, marker := t.TempDir(), t.TempDir(), filepath.Join(t.TempDir(), "admit")
	secret := filepath.Join(home, "sessions", "coding.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(secret), 0700))
	require.NoError(t, os.WriteFile(secret, []byte("coding-secret"), 0600))
	plugin := testRuntimePlugin("late-personal-fixture")
	plugin.Root = fixture
	plugin.Runtime.Command = node
	plugin.Runtime.Args = []string{"lazy-native.js", "thread-admission", marker}
	plugin.MCPServers = map[string]config.MCPServerConfig{"forbidden": {Command: "/nonexistent/forbidden-mcp"}}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "fixture"}}, Composition: &composition.Config{Version: composition.Version, Profile: "headless", Patches: []composition.Patch{{Modules: map[string]bool{plugin.ID: true}, Services: []composition.Binding{{Service: toolservice.ServiceName, Major: 1, Provider: plugin.ID}, {Consumer: plugin.ID, Service: toolservice.ServiceName, Major: 1, Provider: toolservice.DefaultModule}}}}}}
	owner, err := NewSession(Options{RootDir: root, HomeDir: home, WuuHome: home, UserSpaceID: "local", ProductDomain: "personal", Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	state, err := owner.RetainState()
	require.NoError(t, err)
	defer state.Close()
	side, release, err := owner.NewSideThreadRunnerForState(state, "personal-late-side", owner.RootDir, ThreadModelSelection{}, "side prompt", "personal-parent")
	require.NoError(t, err)
	defer func() {
		_ = CloseSideThreadTools(context.Background(), side)
		if release != nil {
			_ = release()
		}
	}()
	require.False(t, owner.pluginGeneration.nativeDependencies.initialized)
	require.NoError(t, os.WriteFile(marker, []byte("ready"), 0600))
	require.NoError(t, (workingnotes.Store{Home: owner.ProductDataRoot()}).CompareAndSwap("personal-late-side", nil, `{"side.md":"late-personal-note"}`))
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "personal-late-side", ExecutionID: "turn"})
	result, err := side.Tools.Execute(ctx, providers.ToolCall{ID: "notes", Name: "notes", Arguments: `{"action":"read","path":"side.md"}`})
	require.NoError(t, err)
	require.Contains(t, result, "late-personal-note")
	args, _ := json.Marshal(map[string]string{"path": secret})
	result, err = side.Tools.Execute(ctx, providers.ToolCall{ID: "secret", Name: "read_file", Arguments: string(args)})
	require.NotContains(t, result, "coding-secret")
	require.Contains(t, fmt.Sprint(err)+result, "protected")
	owner.pluginGeneration.nativeDependencies.mu.Lock()
	manager := owner.pluginGeneration.nativeDependencies.manager
	owner.pluginGeneration.nativeDependencies.mu.Unlock()
	require.Nil(t, manager, "late Personal admission started MCP")
	shared, err := (workingnotes.Store{Home: home}).Read("personal-late-side")
	require.NoError(t, err)
	require.Nil(t, shared)
}
