package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/workingnotes"
	"github.com/stretchr/testify/require"
)

func TestThreadNativeToolkitUsesResolvedProviderAndAPIModel(t *testing.T) {
	root := t.TempDir()
	owner := &Session{RootDir: root, WuuHome: t.TempDir(), ProviderName: "custom-alias", Model: "friendly-label", ModelRoles: modelroles.Set{Main: modelroles.Selection{RuleProvider: "openai", APIModel: "gpt-5-codex"}}}
	generation := &PluginGeneration{}
	kit, cleanup, err := owner.newThreadNativeToolkit(context.Background(), generation, root)
	require.NoError(t, err)
	defer func() { _ = cleanup(context.Background()); _, _ = generation.nativeDependencies.close() }()
	require.Equal(t, string(modelprofile.ProfileOpenAICodex), kit.ActiveSurface().ProfileName)
}

// A delegated native toolkit must retain the user host's domain restrictions,
// including in worker clones, without launching plugin-provided MCP servers.
func TestDelegatedNativeToolkitKeepsUserHostBoundary(t *testing.T) {
	home, root, data := t.TempDir(), t.TempDir(), t.TempDir()
	protected := filepath.Join(home, "sessions", "private.txt")
	require.NoError(t, os.MkdirAll(filepath.Dir(protected), 0700))
	require.NoError(t, os.WriteFile(protected, []byte("coding-secret"), 0600))
	plugin := pluginpkg.Plugin{Manifest: pluginpkg.Manifest{ID: "mcp-forbidden", MCPServers: map[string]config.MCPServerConfig{"required": {Command: "/nonexistent/forbidden-mcp"}}}}
	generation := &PluginGeneration{active: []pluginpkg.Plugin{plugin}, requiredPlugins: map[string]bool{plugin.ID: true}}
	owner := &Session{RootDir: root, WuuHome: home, DataRoot: data, UserSpaceID: "local", ProductDomain: "personal", ProviderName: "openai", Model: "gpt-5"}
	kit, cleanup, err := owner.newThreadNativeToolkit(context.Background(), generation, root)
	require.NoError(t, err)
	defer func() { _ = cleanup(context.Background()); _, _ = generation.nativeDependencies.close() }()
	require.Nil(t, kit.MCPManager(), "user host must not start plugin MCP")
	otherDomain := &Session{RootDir: root, WuuHome: home}
	_, _, err = otherDomain.newThreadNativeToolkit(context.Background(), generation, root)
	require.ErrorContains(t, err, "native generation domain changed")
	codingGeneration := &PluginGeneration{}
	_, codingCleanup, err := otherDomain.newThreadNativeToolkit(context.Background(), codingGeneration, root)
	require.NoError(t, err)
	defer func() { _ = codingCleanup(context.Background()); _, _ = codingGeneration.nativeDependencies.close() }()
	_, _, err = owner.newThreadNativeToolkit(context.Background(), codingGeneration, root)
	require.ErrorContains(t, err, "native generation domain changed")
	kit.SetSessionID("personal-thread")
	kit.SetAuthorizer(nil)
	kit.SetBoundary(tools.UnconfinedBoundary())
	arg, err := json.Marshal(map[string]string{"path": protected})
	require.NoError(t, err)
	result, err := kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: string(arg)})
	require.True(t, err != nil || result.IsError, "protected coding history was read")
	result, err = kit.ExecuteResult(context.Background(), providers.ToolCall{Name: "notes", Arguments: `{"action":"write","path":"private.md","content":"personal-only","revision":""}`})
	require.NoError(t, err)
	require.False(t, result.IsError, result.TextProjection())
	personal, err := (workingnotes.Store{Home: data}).Read("personal-thread")
	require.NoError(t, err)
	require.NotNil(t, personal)
	require.Contains(t, *personal, "personal-only")
	shared, err := (workingnotes.Store{Home: home}).Read("personal-thread")
	require.NoError(t, err)
	require.Nil(t, shared, "personal notes reached shared coding state")
	worker, err := kit.CloneForRoot(root)
	require.NoError(t, err)
	worker.SetBoundary(tools.UnconfinedBoundary())
	result, err = worker.ExecuteResult(context.Background(), providers.ToolCall{Name: "read_file", Arguments: string(arg)})
	require.True(t, err != nil || result.IsError, "worker clone read protected coding history")
	result, err = worker.ExecuteResult(context.Background(), providers.ToolCall{Name: "notes", Arguments: `{"action":"read","path":"private.md"}`})
	require.NoError(t, err)
	require.False(t, result.IsError, result.TextProjection())
	require.Contains(t, result.TextProjection(), "personal-only")
}
