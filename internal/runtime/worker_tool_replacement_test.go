package runtime

import (
	"context"
	"testing"

	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/agentthread"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/pluginhost"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/skills"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

type workerSearchClient struct {
	pluginToolTestClient
	schema map[string]any
}

func (c *workerSearchClient) Tools() []pluginhost.ToolRegistration {
	return []pluginhost.ToolRegistration{{ID: "search", Description: "Search worker evidence", InputSchema: c.schema, ExecutionScopes: c.scopes,
		Activity: &pluginhost.ToolActivityMetadata{ReadOnly: true, ConcurrencySafe: true, Risk: "low"}}}
}

func TestWorkerReplacementPromptMatchesExecutionScope(t *testing.T) {
	for _, tc := range []struct {
		name    string
		scopes  []string
		worker  agentcontrol.WorkerType
		allowed bool
	}{
		{name: "child enabled", scopes: []string{"root", "child"}, allowed: true},
		{name: "root only", scopes: []string{"root"}},
		{name: "worker allowlist", scopes: []string{"root", "child"}, worker: agentcontrol.WorkerType{AllowedTools: []string{"read_file"}}},
	} {
		t.Run(tc.name, func(t *testing.T) {
			kit, err := tools.New(t.TempDir())
			require.NoError(t, err)
			kit.ConfigureSurfaceForProviderModel("openai", "gpt-5", true)
			client := &workerSearchClient{pluginToolTestClient: pluginToolTestClient{scopes: tc.scopes}, schema: kit.LookupTool("web_search").Definition().InputSchema}
			host := pluginhost.New(client)
			bindings := map[string]config.ToolReplacement{"web_search": {PluginID: client.ID(), ToolID: "search"}}
			require.NoError(t, configureToolReplacements(newPluginToolExecutor(kit, host, "parent", kit.RootDir()), kit, bindings))
			meta := agentthread.Metadata{ID: "child", Path: "/root/research"}
			surface, err := workerToolSurfaceForToolkit(kit, host, "openai", "gpt-5", true, tc.worker, meta)
			require.NoError(t, err)
			all := []skills.Skill{{Name: "replacement-research", Description: "Research with selected search", AllowedTools: []string{"web_search"}}}

			// Match WorkerFactory's identity, filtering and lazy native binding.
			worker, err := kit.CloneForRoot(kit.RootDir())
			require.NoError(t, err)
			worker.ConfigureSurfaceForProviderModel("openai", "gpt-5", false)
			worker.SetAgentIdentity(meta.ID, meta.Path)
			worker.SetSkills(all)
			worker.SetSkillRefresh(func(context.Context, bool) ([]skills.Skill, error) { return all, nil })
			applyWorkerToolFilter(worker, tc.worker)
			executor := newPluginToolExecutor(worker, host, "parent", worker.RootDir())
			require.NoError(t, configureToolReplacements(executor, worker, worker.ReplacementBindings()))
			require.Equal(t, tc.allowed, worker.SupportsTool("web_search"))
			_, advertised := surface.Tools["web_search"]
			require.Equal(t, tc.allowed, advertised)
			visible := promptservice.FilterSkillsForSurface(all, surface)
			require.Equal(t, tc.allowed, len(visible) == 1)
			require.Equal(t, promptservice.FilterSkillsForSurface(all, worker.ActiveSurface()), visible)
			correction, err := worker.PrepareModelStep(context.Background(), nil)
			require.NoError(t, err)
			require.Empty(t, correction, "the initial prompt must not need a catalog correction")
			if !tc.allowed {
				_, err := executor.Execute(context.Background(), providers.ToolCall{Name: "web_search", Arguments: `{"query":"hello"}`})
				require.Error(t, err)
				require.False(t, client.executed)
			}
			require.True(t, kit.SupportsTool("web_search"), "worker preparation must not attenuate the parent")
		})
	}
}
