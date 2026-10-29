//go:build !windows

package runtime

import (
	"context"
	"crypto/sha256"
	"encoding/base64"
	"encoding/json"
	"fmt"
	"os"
	"os/exec"
	"path/filepath"
	"testing"
	"time"

	"github.com/blueberrycongee/wuu/internal/agent"
	"github.com/blueberrycongee/wuu/internal/agentcontrol"
	"github.com/blueberrycongee/wuu/internal/config"
	"github.com/blueberrycongee/wuu/internal/executionenv"
	"github.com/blueberrycongee/wuu/internal/loopdriver"
	pluginpkg "github.com/blueberrycongee/wuu/internal/plugin"
	"github.com/blueberrycongee/wuu/internal/providers"
	"github.com/blueberrycongee/wuu/internal/toolctx"
	"github.com/blueberrycongee/wuu/internal/toolledger"
	"github.com/blueberrycongee/wuu/internal/tools"
	"github.com/stretchr/testify/require"
)

func TestProductionWorkerFactorySessionReceiptFollowup(t *testing.T) {
	fixture, binary := os.Getenv("WUU_REMOTE_READ_FIXTURE"), os.Getenv("WUU_REMOTE_READ_WORKER_BINARY")
	if fixture == "" || binary == "" {
		t.Skip("run remote-read acceptance")
	}
	for _, scenario := range []string{"prepared", "committed-before-ack", "committed-after-ack", "cross-actor"} {
		t.Run(scenario, func(t *testing.T) {
			t.Setenv("HOME", t.TempDir())
			t.Setenv("WUU_HOME", t.TempDir())
			node, err := exec.LookPath("node")
			require.NoError(t, err)
			local, remote := t.TempDir(), t.TempDir()
			initSessionWorkerRepo(t, local)
			template, err := tools.New(local)
			require.NoError(t, err)
			schema, err := json.Marshal(template.LookupTool("read_file").Definition().InputSchema)
			require.NoError(t, err)
			require.NoError(t, os.WriteFile(filepath.Join(fixture, "schema.json"), schema, 0600))
			plugin := testRuntimePlugin("fixture.remote-pagination")
			plugin.Root, plugin.Runtime.Command, plugin.Runtime.Args = fixture, node, []string{"pagination-fixture.js"}
			plugins := []pluginpkg.Plugin{plugin}
			cfg := config.Config{DefaultProvider: "fixture", Providers: map[string]config.ProviderConfig{"fixture": {Type: "openai-compatible", BaseURL: "https://example.test/v1", APIKey: "fixture", Model: "gpt-5"}}, ExecutionEnvironments: executionenv.Config{Default: "remote", Profiles: map[string]executionenv.Profile{"remote": {Backend: "command", Command: []string{binary, "execution-worker"}, Workspace: remote}}}}
			cfg.Agent.ToolReplacements = map[string]config.ToolReplacement{"read_file": {PluginID: plugin.ID, ToolID: "read"}}
			owner, err := NewSession(Options{RootDir: local, HomeDir: t.TempDir(), WuuHome: t.TempDir(), Config: cfg, DiscoveredPlugins: &plugins, NonInteractive: true})
			require.NoError(t, err)
			defer owner.Cleanup()
			var failure *sessionResolutionFailure
			if scenario == "committed-before-ack" || scenario == "committed-after-ack" {
				failure = &sessionResolutionFailure{Documents: owner.Documents, after: scenario == "committed-after-ack"}
				owner.Documents = failure
			}
			ctx, cancel := context.WithTimeout(context.Background(), 60*time.Second)
			defer cancel()
			require.NoError(t, owner.PinExecutionEnvironment("factory-parent", ""))
			content := []byte("first\nWORKER_SECRET\nthird\n")
			digest := sha256.Sum256(content)
			raw, err := json.Marshal(map[string]any{"path": "$SESSION_DIR/file.txt", "offset": 2, "limit": 1, "expected_sha256": fmt.Sprintf("%x", digest)})
			require.NoError(t, err)
			args, err := json.Marshal(map[string]string{"continuation": base64.RawURLEncoding.EncodeToString(raw)})
			require.NoError(t, err)
			client := &sessionRecordingClient{}
			if failure != nil {
				client.streamBatches = [][]providers.StreamEvent{{{Type: providers.EventToolUseStart, ToolCall: &providers.ToolCall{ID: "native-line", Name: "read_file"}}, {Type: providers.EventToolUseDelta, Content: string(args)}, {Type: providers.EventToolUseEnd, ToolCall: &providers.ToolCall{ID: "native-line", Name: "read_file"}}, {Type: providers.EventDone}}}
			}
			owner.WorkerClient = client
			thread, err := owner.NewThreadRuntime("factory-parent")
			require.NoError(t, err)
			defer thread.AgentControl.Close()
			defer owner.ReleasePluginGeneration(thread.PluginGeneration)
			thread.Toolkit.SetPermissionMode("standard")
			require.NoError(t, os.WriteFile(filepath.Join(thread.Toolkit.SessionDir(), "file.txt"), content, 0600))
			spawn, err := thread.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "factory_worker", Prompt: "perform read", Synchronous: true})
			require.NoError(t, err)
			if failure != nil {
				require.True(t, failure.fired.Load(), "real worker did not reach lost-ACK injection")
				thread.Toolkit.SetPermissionMode("read_only")
				_, err = thread.AgentControl.FollowupTask(ctx, spawn.AgentID, "resume while revoked")
				if err == nil {
					denied, waitErr := thread.AgentControl.Wait(ctx, spawn.AgentID)
					require.NoError(t, waitErr)
					require.NotEqual(t, "completed", string(denied.Status), "%+v", denied)
				}
				require.NotContains(t, fmt.Sprint(client.LastRequest().Messages), "WORKER_SECRET")
				thread.Toolkit.SetPermissionMode("standard")
				_, err = thread.AgentControl.FollowupTask(ctx, spawn.AgentID, "resume with authority")
				require.NoError(t, err)
				resumed, err := thread.AgentControl.Wait(ctx, spawn.AgentID)
				require.NoError(t, err)
				require.Equal(t, "completed", string(resumed.Status), "%+v", resumed)
				require.Contains(t, fmt.Sprint(client.LastRequest().Messages), "WORKER_SECRET", "the recovered result must actually reach the resumed worker")
				return
			}
			var executor agent.ToolExecutor
			originalWake := workerWakeAuthority(thread.Toolkit)
			thread.AgentControl.Manager().SetWakeAuthority(func(e agent.ToolExecutor) { originalWake(e); executor = e })
			require.NoError(t, thread.AgentControl.Manager().ReconcileToolLedger(ctx, spawn.AgentID))
			thread.AgentControl.Manager().SetWakeAuthority(originalWake)
			require.NotNil(t, executor)
			callCtx := toolctx.WithSettlement(loopdriver.WithExecutionContext(ctx, loopdriver.ExecutionContext{SessionID: "factory-parent", ExecutionID: "test"}), true)
			rich, ok := executor.(agent.RichToolExecutor)
			require.True(t, ok)
			result, err := rich.ExecuteResult(callCtx, providers.ToolCall{ID: "prepared-native", Name: "read_file", Arguments: string(args)})
			require.NoError(t, err)
			require.False(t, result.IsError)
			require.Contains(t, result.TextProjection(), "WORKER_SECRET")
			prepared, err := toolctx.PrepareSettlement(callCtx, true)
			require.NoError(t, err)
			require.NotNil(t, prepared)
			require.NotNil(t, prepared.Identity)
			original := prepared.Identity
			identity := executionenv.ReadBatchIdentity{OperationID: original.OperationID, WorkerInstance: original.WorkerInstance, Actor: original.Actor, EnvironmentIdentity: original.EnvironmentIdentity, OwnerKind: original.OwnerKind}
			originalReceipt, err := workerObservationRecoverer(executor).RecoverReadBatch(ctx, identity)
			require.NoError(t, err)
			state, err := originalReceipt.Status(ctx)
			require.NoError(t, err)
			require.Equal(t, "prepared", state)
			_, err = thread.Toolkit.ObservationRecoverer().RecoverReadBatch(ctx, identity)
			require.Error(t, err, "parent toolkit must not recover its worker's receipt")
			target := spawn.AgentID
			if scenario == "cross-actor" {
				second, err := thread.AgentControl.Spawn(ctx, agentcontrol.SpawnRequest{Type: agentcontrol.DefaultSubagentType, TaskName: "other_worker", Prompt: "idle", Synchronous: true})
				require.NoError(t, err)
				target = second.AgentID
			}
			ledger, err := toolledger.New(owner.Documents, target)
			require.NoError(t, err)
			batch, err := ledger.BeginBatch(ctx, "injected", 0)
			require.NoError(t, err)
			inv, err := ledger.Prepare(ctx, batch, providers.ToolCall{ID: "prepared-native", Name: "read_file", Arguments: string(args)}, toolledger.ReplayAtMostOnce)
			require.NoError(t, err)
			require.NoError(t, ledger.FinalizeBatch(ctx, batch))
			require.NoError(t, ledger.Start(ctx, inv.ID))
			id := prepared.Identity
			intent := toolledger.ObservationIntent{OperationID: id.OperationID, WorkerInstance: id.WorkerInstance, Actor: target, EnvironmentIdentity: id.EnvironmentIdentity, OwnerKind: id.OwnerKind}
			require.NoError(t, ledger.StageObservation(ctx, inv.ID, intent, result))
			_, err = thread.AgentControl.FollowupTask(ctx, target, "resume")
			if scenario == "cross-actor" {
				require.Error(t, err)
				require.Error(t, ledger.ValidateContinuation(ctx))
				require.NoError(t, prepared.Abort(ctx))
				return
			}
			require.NoError(t, err)
			_, err = thread.AgentControl.Wait(ctx, target)
			require.NoError(t, err)
			require.NoError(t, ledger.ValidateContinuation(ctx))
		})
	}
}
