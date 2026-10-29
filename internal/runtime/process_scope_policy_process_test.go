package runtime

import (
	"context"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestServiceContinuationSDKWrappedProcessAuthority(t *testing.T) {
	graph := newContinuationProcessGraph(t, "policy-authority", false, false)
	thread := &ThreadRuntime{PluginHost: graph.host, StreamRunner: &agent.StreamRunner{Tools: graph.executor}, processScopeConfig: tools.ProcessScopeConfig{Manager: graph.manager, RootDir: graph.executor.identity.cwd, ThreadID: "thread", ActorID: "actor"}}
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
	thread.StreamRunner.Tools = &processScopeToolWrapper{ToolExecutor: &processScopeToolWrapper{ToolExecutor: graph.executor}}
	ctx, cancel := context.WithTimeout(context.Background(), 15*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "thread", ExecutionID: "turn"})
	execute := func(id string) (string, error) {
		return thread.StreamRunner.Tools.Execute(ctx, providers.ToolCall{ID: id, Name: "process", Arguments: `{}`})
	}
	output, err := execute("before")
	require.NoError(t, err)
	require.Contains(t, output, "policy-authority")
	before, err := graph.manager.List()
	require.NoError(t, err)
	require.Len(t, before, 1)
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "read_only"}, false, nil)
	_, err = execute("denied")
	require.Error(t, err)
	after, err := graph.manager.List()
	require.NoError(t, err)
	require.Len(t, after, 1, "revoked call must not start a process")
	thread.ConfigureExecutionPermissions(config.ResolvedPermissions{Mode: "unconfined"}, false, nil)
	output, err = execute("allowed-again")
	require.NoError(t, err)
	require.Contains(t, output, "policy-authority")
	after, err = graph.manager.List()
	require.NoError(t, err)
	require.Len(t, after, 2)
}
