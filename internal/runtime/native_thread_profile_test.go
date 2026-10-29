package runtime

import (
	"context"
	"encoding/json"
	"os"
	"path/filepath"
	"testing"

	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/hooks"
	"github.com/blueberrycongee/wuu/internal/modelprofile"
	"github.com/blueberrycongee/wuu/internal/modelroles"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/userspace"
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
	generation := &PluginGeneration{userBinding: userspace.Binding{UserSpaceID: "local", ProductDomain: "personal"}, active: []pluginpkg.Plugin{plugin}, requiredPlugins: map[string]bool{plugin.ID: true}}
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

func TestColdNativeGenerationRejectsCrossDomainBeforeInitialization(t *testing.T) {
	personal := userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}
	coding := userspace.Binding{}
	otherUser := userspace.Binding{UserSpaceID: "bob", ProductDomain: "personal"}
	otherProduct := userspace.Binding{UserSpaceID: "alice", ProductDomain: "coding"}
	for _, tc := range []struct {
		name          string
		owner, caller userspace.Binding
	}{
		{"personal-to-coding", personal, coding}, {"coding-to-personal", coding, personal}, {"other-user", personal, otherUser}, {"other-product", personal, otherProduct},
	} {
		t.Run(tc.name, func(t *testing.T) {
			root := t.TempDir()
			home := t.TempDir()
			g := &PluginGeneration{userBinding: tc.owner}
			caller := &Session{RootDir: root, WuuHome: home, UserSpaceID: tc.caller.UserSpaceID, ProductDomain: tc.caller.ProductDomain}
			_, _, err := caller.newThreadNativeToolkit(context.Background(), g, root)
			require.ErrorContains(t, err, "native generation domain changed")
			require.False(t, g.nativeDependencies.initialized, "cross-domain call initialized the generation")
			owner := &Session{RootDir: root, WuuHome: home, UserSpaceID: tc.owner.UserSpaceID, ProductDomain: tc.owner.ProductDomain}
			_, cleanup, err := owner.newThreadNativeToolkit(context.Background(), g, root)
			require.NoError(t, err)
			_ = cleanup(context.Background())
			_, _ = g.nativeDependencies.close()
		})
	}
}

func TestCrossDomainGenerationActivationHasNoEffects(t *testing.T) {
	personal := userspace.Binding{UserSpaceID: "alice", ProductDomain: "personal"}
	for _, tc := range []struct {
		name           string
		source, target userspace.Binding
	}{
		{"personal-to-coding", personal, userspace.Binding{}}, {"coding-to-personal", userspace.Binding{}, personal}, {"other-user", personal, userspace.Binding{UserSpaceID: "bob", ProductDomain: "personal"}}, {"other-product", personal, userspace.Binding{UserSpaceID: "alice", ProductDomain: "coding"}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			owner := &Session{UserSpaceID: tc.target.UserSpaceID, ProductDomain: tc.target.ProductDomain}
			candidate := &PluginGeneration{userBinding: tc.source, host: pluginhost.New(), hooks: hooks.NewDispatcher(nil)}
			committed := false
			err := owner.ActivatePluginGeneration(candidate, func() error { committed = true; return nil })
			require.ErrorContains(t, err, "plugin generation domain changed")
			require.False(t, committed)
			require.Nil(t, owner.pluginGeneration)
			require.False(t, candidate.nativeDependencies.initialized)
			owner.pluginGeneration = candidate
			_, err = owner.cloneForThreadModel()
			require.ErrorContains(t, err, "plugin generation domain changed")
		})
	}
}

func TestCapturedGenerationKeepsUserHostBinding(t *testing.T) {
	owner := &Session{UserSpaceID: "alice", ProductDomain: "personal", PluginHost: pluginhost.New(), HookDispatcher: hooks.NewDispatcher(nil)}
	captured := owner.capturePluginGeneration()
	require.True(t, captured.userBinding.Matches(owner.UserBinding()))
	require.NoError(t, owner.releasePluginGeneration(captured))
}
