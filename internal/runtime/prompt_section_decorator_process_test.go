package runtime

import (
	"context"
	"errors"
	"os"
	"os/exec"
	"strings"
	"testing"

	"github.com/blueberrycongee/wuu/internal/composition"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/promptservice"
	"github.com/stretchr/testify/require"
)

// Runs an independently installed SDK plugin through the selected prompt graph.
func TestPromptSectionDecoratorProcess(t *testing.T) {
	fixture := os.Getenv("WUU_PROMPT_SECTION_FIXTURE")
	if fixture == "" {
		t.Skip("run node scripts/prompt-section-e2e.mjs")
	}
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	root := t.TempDir()
	home, state := testWorkspaceState(t, root)
	plugin := testRuntimePlugin("independent.prompt-section")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"prompt-section-decorator.js"}
	selection := &composition.Config{Version: composition.Version, Services: []composition.Binding{
		{Service: promptservice.ServiceName, Major: 1, Provider: plugin.ID},
		{Consumer: plugin.ID, Service: promptservice.ServiceName, Major: 1, Provider: promptservice.NativeModule},
	}}
	ctx := context.Background()
	host, _, err := buildPluginHost([]pluginpkg.Plugin{plugin}, root, "prompt-decorator", home, state, nil, startPluginClient, nil, nil, selection, "")
	require.NoError(t, err)
	t.Cleanup(func() { _ = host.Close(context.Background()) })
	require.NoError(t, host.Activate(ctx))
	for _, role := range []string{"main", "worker"} {
		t.Run(role, func(t *testing.T) {
			request := promptservice.Request{Role: role, CWD: "/项目", Date: "2026-10-29"}
			if role == "worker" {
				request.Worker = &promptservice.WorkerContext{Prompt: "worker task", Isolation: "inplace"}
			}
			original, err := promptservice.Assemble(request)
			require.NoError(t, err)
			result, err := assembleSelectedPrompt(ctx, host, request, nil)
			require.NoError(t, err)
			require.Contains(t, result.Content, "# Environment\n- Current working directory: /项目\n- Current date: 2026-10-29")
			require.NotEqual(t, original.Content, result.Content)
			require.Equal(t, len(original.Sections), len(result.Sections))
			for i, section := range result.Sections {
				require.Equal(t, original.Sections[i].Key, section.Key)
				if section.Key != "environment" {
					require.Equal(t, original.Sections[i].Hash, section.Hash)
				}
			}
			if role == "worker" {
				require.Contains(t, result.Content, "worker task")
			}
		})
	}
	// Contributions with the same key make the target ambiguous, so no native
	// prompt may silently pass through after the adapter rejects the input.
	_, err = assembleSelectedPrompt(ctx, host, promptservice.Request{Role: "main", Contributions: []promptservice.Contribution{{Key: "environment", Content: "conflict", Static: true}}}, nil)
	require.Error(t, err)
	retired, found := host.RetirePlugin(ctx, promptservice.NativeModule, errors.New("removed"))
	require.True(t, found)
	require.NoError(t, retired.Err)
	_, err = assembleSelectedPrompt(ctx, host, promptservice.Request{Role: "main"}, nil)
	require.Error(t, err)
	require.False(t, strings.TrimSpace(err.Error()) == "")
}
