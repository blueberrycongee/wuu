package runtime

import (
	"context"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"strings"
	"sync/atomic"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	"github.com/blueberrycongee/wuu/internal/persistence"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/toolresult"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/blueberrycongee/wuu/internal/toolstate"
	"github.com/stretchr/testify/require"
)

type failingRemoteFinalizer struct {
	agent.ToolExecutor
	executionErr *error
}

func (f failingRemoteFinalizer) ExecuteResult(ctx context.Context, call providers.ToolCall) (toolresult.Result, error) {
	rich, ok := f.ToolExecutor.(agent.RichToolExecutor)
	if !ok {
		return toolresult.Result{}, fmt.Errorf("missing rich executor")
	}
	result, err := rich.ExecuteResult(ctx, call)
	*f.executionErr = err
	return result, err
}
func (f failingRemoteFinalizer) FinalizeToolOutcome(_ providers.ToolCall, _ toolresult.Result) agent.ToolFinalization {
	return agent.ToolFinalization{Result: toolresult.FromErrorText("controlled finalization failure")}
}

func TestRemoteObservationDoesNotCommitWhenFinalizationFails(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY\n"), 0600))
	base, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(base.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("finalize", ""))
	thread, err := owner.NewThreadRuntime("finalize")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("read_only")
	thread.Toolkit.SetGitAttributionEnabled(true)
	actor, _ := thread.Toolkit.ExecutionActor()
	req := executionenv.ToolRequest{Actor: actor, PermissionMode: "read_only", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared.txt"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalize", ExecutionID: "turn"})
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
	require.NoError(t, err)
	var underlyingErr error
	run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: failingRemoteFinalizer{thread.StreamRunner.Tools, &underlyingErr}, Ledger: thread.StreamRunner.ToolLedger, OperationID: "turn", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer run.Cancel()
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "finalize-fails", Name: "read_file", Arguments: req.Call.Arguments}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.True(t, messages[0].ToolResult.IsError)
	require.NoError(t, underlyingErr, "the provider must succeed before the controlled finalizer failure")
	remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
	require.True(t, ok)
	ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := ticket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "failed host finalization must retain the prior native observation")
}

func TestRemoteObservationDoesNotCommitWhenPTCParentFails(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY\n"), 0600))
	base, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(base.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.PTC.Enabled = true
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("finalize", ""))
	thread, err := owner.NewThreadRuntime("finalize")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("standard")
	thread.Toolkit.SetGitAttributionEnabled(true)
	actor, _ := thread.Toolkit.ExecutionActor()
	req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared.txt"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalize", ExecutionID: "turn"})
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
	require.NoError(t, err)
	run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "turn", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer run.Cancel()
	args, err := json.Marshal(map[string]any{"code": `const r=await tools.read_file({path:"shared.txt"}); if(!r.structured_content.text.includes("plugin:REMOTE-ONLY")) throw new Error("child did not succeed"); console.log("CHILD_OK"); throw new Error("controlled outer failure")`, "description": "failed parent"})
	require.NoError(t, err)
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "failed-parent", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.True(t, messages[0].ToolResult.IsError)
	require.Contains(t, messages[0].Content, "CHILD_OK", "the child must complete before the parent fails")
	remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
	require.True(t, ok)
	ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := ticket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "failed parent must retain the prior native observation")
}

func TestRemoteObservationCommitsWhenPTCParentSucceeds(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY\n"), 0600))
	base, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(base.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.PTC.Enabled = true
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("finalize", ""))
	thread, err := owner.NewThreadRuntime("finalize")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("standard")
	thread.Toolkit.SetGitAttributionEnabled(true)
	actor, _ := thread.Toolkit.ExecutionActor()
	req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared.txt"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalize", ExecutionID: "turn"})
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
	require.NoError(t, err)
	run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "turn", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer run.Cancel()
	args, err := json.Marshal(map[string]any{"code": `const r=await tools.read_file({path:"shared.txt"}); if(!r.structured_content.text.includes("plugin:REMOTE-ONLY")) throw new Error("child did not succeed"); console.log("CHILD_OK")`, "description": "failed parent"})
	require.NoError(t, err)
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "failed-parent", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.False(t, messages[0].ToolResult.IsError)
	require.Contains(t, messages[0].Content, "CHILD_OK", "the child must complete before the parent fails")
	remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
	require.True(t, ok)
	ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := ticket.Commit(ctx)
	require.NoError(t, err)
	require.False(t, changed, "successful parent must invalidate its captured observation")
}

func TestRemoteObservationCommitsTwoPTCChildrenAtomically(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "second.txt"), []byte("SECOND\n"), 0600))
	base, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(base.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.PTC.Enabled = true
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("finalize", ""))
	thread, err := owner.NewThreadRuntime("finalize")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("standard")
	thread.Toolkit.SetGitAttributionEnabled(true)
	actor, _ := thread.Toolkit.ExecutionActor()
	req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared.txt"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalize", ExecutionID: "turn"})
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
	require.NoError(t, err)
	secondReq := req
	secondReq.Call.Arguments = `{"path":"second.txt"}`
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, secondReq)
	require.NoError(t, err)
	run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "turn", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer run.Cancel()
	args, err := json.Marshal(map[string]any{"code": `const r=await tools.read_file({path:"shared.txt"}); const s=await tools.read_file({path:"second.txt"}); if(!r.structured_content.text.includes("plugin:REMOTE-ONLY") || !s.structured_content.text.includes("plugin:SECOND")) throw new Error("child did not succeed"); console.log("CHILD_OK")`, "description": "failed parent"})
	require.NoError(t, err)
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "failed-parent", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.False(t, messages[0].ToolResult.IsError)
	require.Contains(t, messages[0].Content, "CHILD_OK", "the child must complete before the parent fails")
	remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
	require.True(t, ok)
	ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := ticket.Commit(ctx)
	require.NoError(t, err)
	require.False(t, changed, "successful parent must invalidate its captured observation")
	ticket, err = remoteExec.PrepareUnobservedRead(ctx, secondReq)
	require.NoError(t, err)
	changed, err = ticket.Commit(ctx)
	require.NoError(t, err)
	require.False(t, changed, "both children must commit together")
}

func TestRemoteObservationRevokedChildAbortsPTCBatch(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "second.txt"), []byte("SECOND\n"), 0600))
	base, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(base.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.PTC.Enabled = true
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("finalize", ""))
	thread, err := owner.NewThreadRuntime("finalize")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("standard")
	thread.Toolkit.SetGitAttributionEnabled(true)
	actor, _ := thread.Toolkit.ExecutionActor()
	req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared.txt"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalize", ExecutionID: "turn"})
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
	require.NoError(t, err)
	secondReq := req
	secondReq.Call.Arguments = `{"path":"second.txt"}`
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, secondReq)
	require.NoError(t, err)
	var secondChecks atomic.Int32
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, "second.txt") && secondChecks.Add(1) >= 3 {
			return fmt.Errorf("second child revoked")
		}
		return nil
	})
	run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: thread.StreamRunner.ToolLedger, OperationID: "turn", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer run.Cancel()
	args, err := json.Marshal(map[string]any{"code": `const r=await tools.read_file({path:"shared.txt"}); const s=await tools.read_file({path:"second.txt"}); if(!r.structured_content.text.includes("plugin:REMOTE-ONLY") || !s.structured_content.text.includes("plugin:SECOND")) throw new Error("child did not succeed"); console.log("CHILD_OK")`, "description": "failed parent"})
	require.NoError(t, err)
	messages, err := run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "failed-parent", Name: "run_code", Arguments: string(args)}}, nil)
	require.NoError(t, err)
	require.Len(t, messages, 1)
	require.True(t, messages[0].ToolResult.IsError)
	require.Contains(t, messages[0].Content, "second child revoked")
	thread.Toolkit.SetPermissionRequestHook(nil)

	remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
	require.True(t, ok)
	ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := ticket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "revoked batch must preserve first observation")
	ticket, err = remoteExec.PrepareUnobservedRead(ctx, secondReq)
	require.NoError(t, err)
	changed, err = ticket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "revoked batch must preserve second observation")
}

// Trigger revocation only after the finalized parent intent has been staged.
type stageRevokeDocuments struct {
	persistence.Documents
	revoked *atomic.Bool
}

func (d stageRevokeDocuments) Commit(ctx context.Context, tx persistence.Transaction) (persistence.Receipt, error) {
	receipt, err := d.Documents.Commit(ctx, tx)
	if err == nil {
		for _, write := range tx.Writes {
			if write.Namespace != toolstate.Invocations {
				continue
			}
			var invocation toolstate.Invocation
			if json.Unmarshal(write.Value, &invocation) == nil && invocation.Observation != nil && invocation.Observation.Outcome == "" {
				d.revoked.Store(true)
			}
		}
	}
	return receipt, err
}

func TestRemoteObservationRevokedAfterStageAbortsPTCBatch(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote read acceptance")
	}
	t.Setenv("HOME", t.TempDir())
	t.Setenv("WUU_HOME", t.TempDir())
	node, err := exec.LookPath("node")
	require.NoError(t, err)
	local, remote := t.TempDir(), t.TempDir()
	require.NoError(t, os.WriteFile(filepath.Join(remote, "shared.txt"), []byte("REMOTE-ONLY\n"), 0600))
	require.NoError(t, os.WriteFile(filepath.Join(remote, "second.txt"), []byte("SECOND\n"), 0600))
	base, err := tools.New(local)
	require.NoError(t, err)
	schema, err := json.Marshal(base.LookupTool("read_file").Definition().InputSchema)
	require.NoError(t, err)
	require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
	plugin := testRuntimePlugin("fixture.remote-reader")
	plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"fixture.js"}
	plugins := []pluginpkg.Plugin{plugin}
	cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
	cfg.PTC.Enabled = true
	cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
	owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
	require.NoError(t, err)
	defer owner.Cleanup()
	require.NoError(t, owner.PinExecutionEnvironment("finalize", ""))
	thread, err := owner.NewThreadRuntime("finalize")
	require.NoError(t, err)
	defer owner.ReleasePluginGeneration(thread.PluginGeneration)
	defer thread.AgentControl.Close()
	thread.Toolkit.SetPermissionMode("standard")
	thread.Toolkit.SetGitAttributionEnabled(true)
	actor, _ := thread.Toolkit.ExecutionActor()
	req := executionenv.ToolRequest{Actor: actor, PermissionMode: "standard", GitAttributionEnabled: true, Call: providers.ToolCall{Name: "read_file", Arguments: `{"path":"shared.txt"}`}}
	ctx, cancel := context.WithTimeout(context.Background(), 30*time.Second)
	defer cancel()
	ctx = loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "finalize", ExecutionID: "turn"})
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, req)
	require.NoError(t, err)
	secondReq := req
	secondReq.Call.Arguments = `{"path":"second.txt"}`
	_, err = thread.Toolkit.ExecutionEnvironment().Execute(ctx, secondReq)
	require.NoError(t, err)
	var revoked atomic.Bool
	thread.Toolkit.SetPermissionRequestHook(func(_ context.Context, _ *tools.Toolkit, info tools.ToolInfo, call providers.ToolCall) error {
		if info.Name == "read_file" && strings.Contains(call.Arguments, "second.txt") && revoked.Load() {
			return fmt.Errorf("second child revoked")
		}
		return nil
	})
	ledger, err := toolledger.New(stageRevokeDocuments{Documents: owner.Documents, revoked: &revoked}, "finalize")
	require.NoError(t, err)
	run := agent.NewTurnToolRuntime(agent.ToolRuntimeConfig{Executor: thread.StreamRunner.Tools, Ledger: ledger, OperationID: "turn", RunContext: ctx, Gate: agent.NewToolExecutionGate(1)})
	defer run.Cancel()
	args, err := json.Marshal(map[string]any{"code": `const r=await tools.read_file({path:"shared.txt"}); const s=await tools.read_file({path:"second.txt"}); if(!r.structured_content.text.includes("plugin:REMOTE-ONLY") || !s.structured_content.text.includes("plugin:SECOND")) throw new Error("child did not succeed"); console.log("CHILD_OK")`, "description": "failed parent"})
	require.NoError(t, err)
	_, err = run.ExecuteFinalCalls(ctx, []providers.ToolCall{{ID: "failed-parent", Name: "run_code", Arguments: string(args)}}, nil)
	require.ErrorContains(t, err, "second child revoked")
	require.NoError(t, ledger.ValidateContinuation(ctx))
	require.True(t, revoked.Load(), "stage barrier must have revoked after durable intent")
	thread.Toolkit.SetPermissionRequestHook(nil)

	remoteExec, ok := thread.Toolkit.ExecutionEnvironment().(executionenv.PreparedReadExecutor)
	require.True(t, ok)
	ticket, err := remoteExec.PrepareUnobservedRead(ctx, req)
	require.NoError(t, err)
	changed, err := ticket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "revoked batch must preserve first observation")
	ticket, err = remoteExec.PrepareUnobservedRead(ctx, secondReq)
	require.NoError(t, err)
	changed, err = ticket.Commit(ctx)
	require.NoError(t, err)
	require.True(t, changed, "revoked batch must preserve second observation")
}
